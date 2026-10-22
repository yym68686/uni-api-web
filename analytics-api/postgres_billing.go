package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const billingColumns = "event_id,kind," + billingIdentityColumns + ",at_ms,started_ms,upstream_base,upstream_key_hash,billing_request_ids,status,upstream_error_sha256"
const billingPredicate = "kind IN ('attempt','billing')"

var (
	errBillingBackfillHeadroom   = errors.New("billing backfill requires storage headroom; wait for bounded FIFO maintenance")
	errBillingBackfillBusy       = errors.New("billing backfill deferred for live ingestion; retry preparation")
	errBillingProjectionMismatch = errors.New("billing projection differs from committed facts")
)

const billingSchema = `CREATE TABLE IF NOT EXISTS billing_facts(
 event_id TEXT NOT NULL,kind TEXT NOT NULL,source_id TEXT NOT NULL,
 instance_id TEXT,request_id TEXT,attempt_id TEXT,provider TEXT,key_id TEXT,
 model TEXT,upstream_model TEXT,endpoint TEXT,stream BOOLEAN,at_ms BIGINT NOT NULL,
 started_ms BIGINT,upstream_base TEXT,upstream_key_hash TEXT,billing_request_ids TEXT[],
 status INTEGER,upstream_error_sha256 TEXT,PRIMARY KEY(at_ms,event_id)) PARTITION BY RANGE(at_ms);
 CREATE INDEX IF NOT EXISTS billing_facts_scope ON billing_facts(source_id,key_id,model,at_ms);
 CREATE INDEX IF NOT EXISTS billing_facts_request ON billing_facts(source_id,request_id);
 CREATE INDEX IF NOT EXISTS billing_facts_receipt_ids ON billing_facts USING gin(billing_request_ids) WHERE kind='billing';`

func (e *Engine) initializeBillingProjection(ctx context.Context) error {
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(billingSchema)))
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395014)"); err != nil {
		return err
	}
	var saved string
	err = tx.QueryRowContext(ctx, "SELECT checksum FROM schema_versions WHERE version='0004'").Scan(&saved)
	if err == nil {
		if saved != checksum {
			return errors.New("billing projection schema requires explicit migration")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, billingSchema); err != nil {
		return err
	}
	var populated bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM facts)").Scan(&populated); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO meta(name,value) VALUES('billing_projection_complete',?)", fmt.Sprint(!populated)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_versions(version,checksum) VALUES('0004',?)", checksum); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureBillingPartition(ctx context.Context, tx *analyticsTx, at int64) error {
	start := at / 3600000 * 3600000
	name := fmt.Sprintf("billing_%d", start)
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT to_regclass(?) IS NOT NULL", name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s PARTITION OF billing_facts FOR VALUES FROM (%d) TO (%d)", name, start, start+3600000))
	return err
}

// Run only after every older API writer has drained. New ingestion publishes
// both forms atomically; during preparation readers continue using raw facts.
// Each migration write holds the admission lock for at most 512 source rows.
func (e *Engine) backfillBillingProjection(ctx context.Context) error {
	rows, err := e.DB.QueryContext(ctx, "SELECT start_ms,name FROM fact_partitions ORDER BY start_ms")
	if err != nil {
		return err
	}
	type part struct {
		at   int64
		name string
	}
	var parts []part
	for rows.Next() {
		var p part
		if err = rows.Scan(&p.at, &p.name); err != nil {
			rows.Close()
			return err
		}
		if p.name != fmt.Sprintf("facts_%d", p.at) {
			rows.Close()
			return errors.New("invalid billing source partition")
		}
		parts = append(parts, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range parts {
		at, id := p.at, ""
		busyRetries := 0
		for {
			// Bound additive backfill growth before any batch. Normal ingestion
			// and FIFO retain their own admission rules; maintenance cannot fill
			// the reserve or delete protected configuration to make progress.
			var room bool
			if err = e.DB.QueryRowContext(ctx, "SELECT pg_database_size(current_database())+33554432<ingest_limit_bytes FROM storage_state WHERE singleton").Scan(&room); err != nil {
				return err
			}
			if !room {
				return errBillingBackfillHeadroom
			}
			tx, err := e.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			var locked bool
			if err = tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(8395015)").Scan(&locked); err != nil {
				tx.Rollback()
				return err
			}
			if !locked {
				tx.Rollback()
				busyRetries++
				if busyRetries >= 100 {
					return errBillingBackfillBusy
				}
				if !waitStartup(ctx, 100*time.Millisecond) {
					return ctx.Err()
				}
				continue
			}
			busyRetries = 0
			err = ensureBillingPartition(ctx, tx, p.at)
			if err != nil {
				tx.Rollback()
				return err
			}
			var nextAt int64
			var nextID string
			var copied int
			err = tx.QueryRowContext(ctx, `WITH batch AS MATERIALIZED (SELECT `+billingColumns+` FROM `+p.name+` WHERE `+billingPredicate+` AND (at_ms,event_id)>(?,?) ORDER BY at_ms,event_id LIMIT 512), copied AS (INSERT INTO billing_facts(`+billingColumns+`) SELECT `+billingColumns+` FROM batch ON CONFLICT DO NOTHING RETURNING 1) SELECT at_ms,event_id,(SELECT count(*) FROM copied) FROM batch ORDER BY at_ms DESC,event_id DESC LIMIT 1`, at, id).Scan(&nextAt, &nextID, &copied)
			done := errors.Is(err, sql.ErrNoRows)
			if err != nil && !done {
				tx.Rollback()
				return err
			}
			if err = tx.Commit(); err != nil {
				return err
			}
			if done {
				break
			}
			at, id = nextAt, nextID
			if !waitStartup(ctx, 50*time.Millisecond) {
				return ctx.Err()
			}
		}
		if err = e.verifyBillingProjectionHour(ctx, p.at); err != nil {
			return err
		}
		fmt.Printf("{\"phase\":\"billing_hour_verified\",\"hour\":%d}\n", p.at)
	}
	_, err = e.DB.ExecContext(ctx, "UPDATE meta SET value='true' WHERE name='billing_projection_complete'")
	return err
}

// Include identities from BOTH tables so missing and extra rows are checked.
// Unique (at_ms,event_id) keys make 512 identities bound both complete row sets;
// unlike a whole-hour EXCEPT this cannot sort an entire busy hour to disk. Each
// statement sees one snapshot. Facts are immutable and new writers publish both
// forms atomically, so subsequent batches remain valid under concurrent ingestion.
func (e *Engine) verifyBillingProjectionHour(ctx context.Context, hour int64) error {
	sourceTable := fmt.Sprintf("facts_%d", hour)
	targetTable := fmt.Sprintf("billing_%d", hour)
	at, id := hour, ""
	for {
		query := `WITH keys AS MATERIALIZED (
 SELECT at_ms,event_id FROM (
  (SELECT at_ms,event_id FROM ` + sourceTable + ` WHERE ` + billingPredicate + ` AND (at_ms,event_id)>(?,?) ORDER BY at_ms,event_id LIMIT 512)
  UNION
  (SELECT at_ms,event_id FROM ` + targetTable + ` WHERE (at_ms,event_id)>(?,?) ORDER BY at_ms,event_id LIMIT 512)
 ) candidates ORDER BY at_ms,event_id LIMIT 512
), source AS MATERIALIZED (SELECT ` + billingColumns + ` FROM ` + sourceTable + ` JOIN keys USING(at_ms,event_id) WHERE ` + billingPredicate + `),
 target AS MATERIALIZED (SELECT ` + billingColumns + ` FROM ` + targetTable + ` JOIN keys USING(at_ms,event_id))
SELECT at_ms,event_id,EXISTS((SELECT * FROM source EXCEPT ALL SELECT * FROM target) UNION ALL (SELECT * FROM target EXCEPT ALL SELECT * FROM source)) FROM keys ORDER BY at_ms DESC,event_id DESC LIMIT 1`
		var nextAt int64
		var nextID string
		var mismatch bool
		err := e.DB.QueryRowContext(ctx, query, at, id, at, id).Scan(&nextAt, &nextID, &mismatch)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if mismatch {
			return errBillingProjectionMismatch
		}
		at, id = nextAt, nextID
		if !waitStartup(ctx, 50*time.Millisecond) {
			return ctx.Err()
		}
	}
}

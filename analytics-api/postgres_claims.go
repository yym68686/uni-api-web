package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const claimColumns = "at_ms,event_id,ordinal,upstream_base,upstream_key_hash,rid,owner_digest"
const claimDigestSQL = `sha256(convert_to(upstream_base||E'\n'||upstream_key_hash||E'\n'||rid,'UTF8'))`
const claimSchema = `CREATE TABLE IF NOT EXISTS billing_claims(
 at_ms BIGINT NOT NULL,event_id TEXT NOT NULL,ordinal SMALLINT NOT NULL CHECK(ordinal BETWEEN 1 AND 3),
 upstream_base TEXT NOT NULL,upstream_key_hash TEXT,rid TEXT,owner_digest BYTEA,
 PRIMARY KEY(at_ms,event_id,ordinal)) PARTITION BY RANGE(at_ms);
 CREATE INDEX IF NOT EXISTS billing_claims_owner ON billing_claims(owner_digest);`

// Statement transition rows keep publication atomic for BOTH old and new code
// writers, including a code rollback after preparation has completed.
var claimWriteSchema = `CREATE FUNCTION billing_claims_write_v1() RETURNS TRIGGER
 LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
 DECLARE h BIGINT; relation_name TEXT;
 BEGIN
 FOR h IN SELECT DISTINCT at_ms/3600000*3600000 FROM inserted_claim_facts WHERE kind='billing' LOOP
  relation_name := 'claims_'||h;
  IF to_regclass(format('%I.%I',TG_TABLE_SCHEMA,relation_name)) IS NULL THEN
   EXECUTE format('CREATE TABLE %I.%I PARTITION OF %I.billing_claims FOR VALUES FROM (%s) TO (%s)',TG_TABLE_SCHEMA,relation_name,TG_TABLE_SCHEMA,h,h+3600000);
  END IF;
 END LOOP;
 INSERT INTO billing_claims(` + claimColumns + `) ` + claimProjectionSelect("inserted_claim_facts") + ` ON CONFLICT DO NOTHING;
 RETURN NULL;
 END $$;
 CREATE TRIGGER billing_claims_write_v1 AFTER INSERT ON facts REFERENCING NEW TABLE AS inserted_claim_facts FOR EACH STATEMENT EXECUTE FUNCTION billing_claims_write_v1();`

var errClaimProjectionMismatch = errors.New("receipt ownership projection differs from committed facts")

// Hashes are lookup accelerators, never identity or uniqueness constraints.
// Queries also compare the complete base/credential/receipt tuple. Ordinality
// preserves repeated response headers; attribution still counts distinct events.
func claimProjectionSelect(source string) string {
	return `SELECT at_ms,event_id,ordinal::smallint,upstream_base,upstream_key_hash,rid,` + claimDigestSQL + `
 FROM (SELECT at_ms,event_id,ordinal,` + billingSiteSQL + ` AS upstream_base,upstream_key_hash,rid
 FROM ` + source + ` CROSS JOIN LATERAL unnest(billing_request_ids) WITH ORDINALITY AS ids(rid,ordinal) WHERE kind='billing') expanded`
}

func (e *Engine) initializeClaimProjection(ctx context.Context) error {
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(claimSchema+claimWriteSchema)))
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395014)"); err != nil {
		return err
	}
	var saved string
	err = tx.QueryRowContext(ctx, "SELECT checksum FROM schema_versions WHERE version='0005'").Scan(&saved)
	if err == nil {
		if saved != checksum {
			return errors.New("receipt ownership schema requires explicit migration")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, claimSchema); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, claimWriteSchema); err != nil {
		return err
	}
	// Inspect only partition metadata at startup. Historical data is prepared
	// explicitly after every older writer has drained, never on the ready path.
	var populated bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM fact_partitions)").Scan(&populated); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO meta(name,value) VALUES('claim_projection_complete',?)", fmt.Sprint(!populated)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_versions(version,checksum) VALUES('0005',?)", checksum); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureClaimPartition(ctx context.Context, tx *analyticsTx, at int64) error {
	start := at / 3600000 * 3600000
	name := fmt.Sprintf("claims_%d", start)
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT to_regclass(?) IS NOT NULL", name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s PARTITION OF billing_claims FOR VALUES FROM (%d) TO (%d)", name, start, start+3600000))
	return err
}

func projectedBillingClaimsSQL(narrow bool) string {
	query := `CREATE TEMP TABLE console_spend_claims AS
 WITH target AS MATERIALIZED (SELECT DISTINCT ` + billingSiteSQL + ` AS upstream_base,upstream_key_hash,unnest(billing_request_ids) AS rid FROM console_spend_facts),
 wanted AS MATERIALIZED (SELECT target.*,` + claimDigestSQL + ` AS owner_digest FROM target)
 SELECT t.upstream_base,t.upstream_key_hash,t.rid,count(DISTINCT c.event_id) AS n
 FROM wanted t JOIN billing_claims c USING(owner_digest,upstream_base,upstream_key_hash,rid)
 WHERE c.at_ms >= (SELECT evicted_before_ms FROM storage_state WHERE singleton)`
	if narrow {
		// This explicit restriction avoids a full history hash join when the
		// planner cannot estimate the transient target's receipt cardinality.
		query += ` AND c.owner_digest=ANY(ARRAY(SELECT owner_digest FROM wanted))`
	}
	return query + ` GROUP BY t.upstream_base,t.upstream_key_hash,t.rid`
}

// An older code rollback can drop raw hours without knowing this projection.
// Under the existing admission lock, reclaim those derived files before evicting
// additional live history. Reads already exclude them by the same FIFO floor.
func reclaimOrphanClaimPartition(ctx context.Context, tx *analyticsTx) (bool, error) {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT c.relname FROM pg_inherits h JOIN pg_class c ON c.oid=h.inhrelid CROSS JOIN storage_state s
 WHERE h.inhparent='billing_claims'::regclass AND s.singleton AND c.relname ~ '^claims_[0-9]+$'
 AND substring(c.relname FROM 8)::bigint < s.evicted_before_ms
 AND NOT EXISTS(SELECT 1 FROM fact_partitions p WHERE p.start_ms=substring(c.relname FROM 8)::bigint)
 ORDER BY c.relname LIMIT 1`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var hour int64
	if _, err = fmt.Sscanf(name, "claims_%d", &hour); err != nil || name != fmt.Sprintf("claims_%d", hour) {
		return false, errors.New("invalid orphan receipt projection partition")
	}
	_, err = tx.ExecContext(ctx, "DROP TABLE "+name)
	return true, err
}

// A batch holds admission only for <=512 identities / <=1536 claim rows. Copy
// and verification both yield to live ingestion and cannot recreate an expired
// fact partition if FIFO advances while preparation is running.
func (e *Engine) claimProjectionBatch(ctx context.Context, hour, afterAt int64, afterID string, verify bool) (nextAt int64, nextID string, done bool, err error) {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return afterAt, afterID, false, err
	}
	defer tx.Rollback()
	var locked bool
	if err = tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(8395015)").Scan(&locked); err != nil {
		return afterAt, afterID, false, err
	}
	if !locked {
		return afterAt, afterID, false, errBillingBackfillBusy
	}
	var retained bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM fact_partitions WHERE start_ms=?)", hour).Scan(&retained); err != nil {
		return afterAt, afterID, false, err
	}
	if !retained {
		// Only this derived partition can be left behind by an older FIFO
		// writer during the deployment overlap. No source facts are removed.
		if _, err = tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS claims_%d", hour)); err != nil {
			return afterAt, afterID, false, err
		}
		return afterAt, afterID, true, tx.Commit()
	}
	if !verify {
		var room bool
		if err = tx.QueryRowContext(ctx, "SELECT pg_database_size(current_database())+33554432<ingest_limit_bytes FROM storage_state WHERE singleton").Scan(&room); err != nil {
			return afterAt, afterID, false, err
		}
		if !room {
			return afterAt, afterID, false, errBillingBackfillHeadroom
		}
		if err = ensureClaimPartition(ctx, tx, hour); err != nil {
			return afterAt, afterID, false, err
		}
	}
	source, target := fmt.Sprintf("facts_%d", hour), fmt.Sprintf("claims_%d", hour)
	if verify {
		query := `WITH keys AS MATERIALIZED (
 SELECT at_ms,event_id FROM (
 (SELECT at_ms,event_id FROM ` + source + ` WHERE kind='billing' AND (at_ms,event_id)>(?,?) ORDER BY at_ms,event_id LIMIT 512)
 UNION
 (SELECT DISTINCT at_ms,event_id FROM ` + target + ` WHERE (at_ms,event_id)>(?,?) ORDER BY at_ms,event_id LIMIT 512)
 ) candidates ORDER BY at_ms,event_id LIMIT 512),
 selected_facts AS (SELECT f.* FROM ` + source + ` f JOIN keys USING(at_ms,event_id)),
 expected AS MATERIALIZED (` + claimProjectionSelect("selected_facts") + `),
 actual AS MATERIALIZED (SELECT ` + claimColumns + ` FROM ` + target + ` JOIN keys USING(at_ms,event_id))
 SELECT at_ms,event_id,EXISTS((SELECT * FROM expected EXCEPT ALL SELECT * FROM actual) UNION ALL (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)) FROM keys ORDER BY at_ms DESC,event_id DESC LIMIT 1`
		var mismatch bool
		err = tx.QueryRowContext(ctx, query, afterAt, afterID, afterAt, afterID).Scan(&nextAt, &nextID, &mismatch)
		if err == nil && mismatch {
			return afterAt, afterID, false, errClaimProjectionMismatch
		}
	} else {
		query := `WITH batch AS MATERIALIZED (SELECT event_id,kind,at_ms,upstream_base,upstream_key_hash,billing_request_ids FROM ` + source + ` WHERE kind='billing' AND (at_ms,event_id)>(?,?) ORDER BY at_ms,event_id LIMIT 512),
 copied AS (INSERT INTO billing_claims(` + claimColumns + `) ` + claimProjectionSelect("batch") + ` ON CONFLICT DO NOTHING RETURNING 1)
 SELECT at_ms,event_id,(SELECT count(*) FROM copied) FROM batch ORDER BY at_ms DESC,event_id DESC LIMIT 1`
		var copied int
		err = tx.QueryRowContext(ctx, query, afterAt, afterID).Scan(&nextAt, &nextID, &copied)
	}
	done = errors.Is(err, sql.ErrNoRows)
	if err != nil && !done {
		return afterAt, afterID, false, err
	}
	return nextAt, nextID, done, tx.Commit()
}

func (e *Engine) walkClaimProjectionHour(ctx context.Context, hour int64, verify bool) error {
	at, id, busy := hour, "", 0
	for {
		nextAt, nextID, done, err := e.claimProjectionBatch(ctx, hour, at, id, verify)
		if errors.Is(err, errBillingBackfillBusy) && busy < 100 {
			busy++
			if !waitStartup(ctx, 100*time.Millisecond) {
				return ctx.Err()
			}
			continue
		}
		if err != nil || done {
			return err
		}
		at, id, busy = nextAt, nextID, 0
		if !waitStartup(ctx, 50*time.Millisecond) {
			return ctx.Err()
		}
	}
}

func (e *Engine) backfillClaimProjection(ctx context.Context) error {
	var complete bool
	if err := e.DB.QueryRowContext(ctx, "SELECT value='true' FROM meta WHERE name='claim_projection_complete'").Scan(&complete); err != nil || complete {
		return err
	}
	// Include orphan projection hours so an old replica's FIFO during rollout
	// cannot leave historical competitors in the new retained-data projection.
	rows, err := e.DB.QueryContext(ctx, `SELECT start_ms FROM fact_partitions UNION SELECT substring(c.relname FROM 8)::bigint FROM pg_inherits h JOIN pg_class c ON c.oid=h.inhrelid WHERE h.inhparent='billing_claims'::regclass AND c.relname ~ '^claims_[0-9]+$' ORDER BY 1`)
	if err != nil {
		return err
	}
	var hours []int64
	for rows.Next() {
		var hour int64
		if err = rows.Scan(&hour); err != nil {
			rows.Close()
			return err
		}
		hours = append(hours, hour)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, hour := range hours {
		if err = e.walkClaimProjectionHour(ctx, hour, false); err != nil {
			return err
		}
		if err = e.walkClaimProjectionHour(ctx, hour, true); err != nil {
			return err
		}
		fmt.Printf("{\"phase\":\"claim_hour_verified\",\"hour\":%d}\n", hour)
	}
	// PostgreSQL does not auto-analyze a partitioned parent. Publish its initial
	// cardinality only during explicit preparation, before selecting this path.
	if _, err = e.DB.ExecContext(ctx, "ANALYZE billing_claims"); err != nil {
		return err
	}
	_, err = e.DB.ExecContext(ctx, "UPDATE meta SET value='true' WHERE name='claim_projection_complete'")
	return err
}

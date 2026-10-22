package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
)

const usageColumns = "event_id,kind,source_id,at_ms,key_id,provider,model,upstream_model,endpoint,stream,outcome,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cache_write_1h_tokens"
const usagePredicate = "kind NOT IN ('billing','trace') AND input_tokens IS NOT NULL"
const usageSchema = `CREATE TABLE IF NOT EXISTS usage_facts(
 event_id TEXT NOT NULL,kind TEXT NOT NULL,source_id TEXT NOT NULL,at_ms BIGINT NOT NULL,
 key_id TEXT,provider TEXT,model TEXT,upstream_model TEXT,endpoint TEXT,stream BOOLEAN,outcome TEXT,
 input_tokens BIGINT NOT NULL,output_tokens BIGINT,cache_read_tokens BIGINT,cache_write_tokens BIGINT,cache_write_1h_tokens BIGINT,
 PRIMARY KEY(at_ms,event_id)) PARTITION BY RANGE(at_ms);
 CREATE INDEX IF NOT EXISTS usage_facts_scope ON usage_facts(source_id,input_tokens,at_ms);`

func (e *Engine) initializeUsageProjection(ctx context.Context) error {
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(usageSchema)))
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395014)"); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM schema_versions WHERE version='0003')").Scan(&exists); err != nil {
		return err
	}
	if exists {
		var saved string
		if err = tx.QueryRowContext(ctx, "SELECT checksum FROM schema_versions WHERE version='0003'").Scan(&saved); err != nil {
			return err
		}
		if saved != checksum {
			return errors.New("usage projection schema requires explicit migration")
		}
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, usageSchema); err != nil {
		return err
	}
	var populated bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM facts)").Scan(&populated); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO meta(name,value) VALUES('usage_projection_complete',?)", fmt.Sprint(!populated)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_versions(version,checksum) VALUES('0003',?)", checksum); err != nil {
		return err
	}
	return tx.Commit()
}

func (e *Engine) usageProjectionReady(ctx context.Context) error {
	var ready bool
	if err := e.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM meta WHERE name='usage_projection_complete' AND value='true')").Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return errors.New("usage projection backfill has not been validated")
	}
	return nil
}

func ensureUsagePartition(ctx context.Context, tx *analyticsTx, at int64) error {
	start := at / 3600000 * 3600000
	name := fmt.Sprintf("usage_%d", start)
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT to_regclass(?) IS NOT NULL", name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s PARTITION OF usage_facts FOR VALUES FROM (%d) TO (%d)", name, start, start+3600000))
	return err
}

// The projection carries raw token columns, independent of editable prices.
// Every native insert updates it in the same transaction as facts and rollups.
// This one-time bounded backfill verifies complete row equality before activation.
func (e *Engine) backfillUsageProjection(ctx context.Context) error {
	rows, err := e.DB.QueryContext(ctx, "SELECT start_ms,name FROM fact_partitions ORDER BY start_ms")
	if err != nil {
		return err
	}
	type partition struct {
		at   int64
		name string
	}
	var parts []partition
	for rows.Next() {
		var p partition
		if err = rows.Scan(&p.at, &p.name); err != nil {
			rows.Close()
			return err
		}
		parts = append(parts, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range parts {
		if p.name != fmt.Sprintf("facts_%d", p.at) {
			return errors.New("invalid fact partition")
		}
		tx, err := e.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395015)"); err == nil {
			err = ensureUsagePartition(ctx, tx, p.at)
		}
		source := "SELECT " + usageColumns + " FROM " + p.name + " WHERE " + usagePredicate
		target := "SELECT " + usageColumns + " FROM " + fmt.Sprintf("usage_%d", p.at)
		if err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO usage_facts("+usageColumns+") "+source+" ON CONFLICT DO NOTHING")
		}
		var mismatch bool
		if err == nil {
			err = tx.QueryRowContext(ctx, "SELECT EXISTS(("+source+" EXCEPT ALL "+target+") UNION ALL ("+target+" EXCEPT ALL "+source+"))").Scan(&mismatch)
		}
		if err == nil && mismatch {
			err = errors.New("usage projection differs from committed facts")
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		fmt.Printf("{\"phase\":\"usage_hour_verified\",\"hour\":%d}\n", p.at)
	}
	_, err = e.DB.ExecContext(ctx, "UPDATE meta SET value='true' WHERE name='usage_projection_complete'")
	return err
}

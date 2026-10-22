package main

import (
	"context"
	"errors"
	"fmt"
)

func (e *Engine) rollupLayoutReady(ctx context.Context) error {
	var pending bool
	if err := e.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rollup_partitions r LEFT JOIN pg_partitioned_table p ON p.partrelid=to_regclass(r.name) WHERE p.partstrat IS DISTINCT FROM 'l' OR p.partattrs::text IS DISTINCT FROM '2')`).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return errors.New("rollup level layout requires pre-activation migration")
	}
	return nil
}

// This initial migration is deliberately unavailable after source activation.
// No user traffic reads these new analytics tables yet. Each hour is replaced
// transactionally and compared in both directions before its old copy is freed.
func (e *Engine) separateRollupLevels(ctx context.Context) error {
	if e.rollupLayoutReady(ctx) == nil {
		return nil
	}
	var activated bool
	if err := e.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM ingest_sources) OR EXISTS(SELECT 1 FROM meta WHERE name='legacy_migration_phase')").Scan(&activated); err != nil {
		return err
	}
	if activated {
		return errors.New("rollup layout must be prepared before activation")
	}
	rows, err := e.DB.QueryContext(ctx, "SELECT start_ms,name FROM rollup_partitions ORDER BY start_ms")
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
		if p.name != fmt.Sprintf("rollups_%d", p.at) {
			return errors.New("invalid rollup partition")
		}
		var split bool
		if err = e.DB.QueryRowContext(ctx, "SELECT relkind='p' FROM pg_class WHERE oid=to_regclass(?)", p.name).Scan(&split); err != nil {
			return err
		}
		if split {
			continue
		}
		tx, err := e.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "SET LOCAL statement_timeout='60s'; SET LOCAL lock_timeout='1s'; SET LOCAL temp_file_limit='128MB'"); err == nil {
			_, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395015)")
		}
		old := p.name + "_before_levels"
		if err == nil {
			_, err = tx.ExecContext(ctx, "ALTER TABLE rollups DETACH PARTITION "+p.name)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "ALTER TABLE "+p.name+" RENAME TO "+old)
		}
		if err == nil {
			err = createRollupHour(ctx, tx, p.at)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO "+p.name+" SELECT * FROM "+old)
		}
		var mismatch bool
		if err == nil {
			err = tx.QueryRowContext(ctx, "SELECT EXISTS((SELECT * FROM "+old+" EXCEPT ALL SELECT * FROM "+p.name+") UNION ALL (SELECT * FROM "+p.name+" EXCEPT ALL SELECT * FROM "+old+"))").Scan(&mismatch)
		}
		if err == nil && mismatch {
			err = errors.New("rollup rows changed during layout migration")
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "DROP TABLE "+old)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		fmt.Printf("{\"phase\":\"rollup_layout_verified\",\"hour\":%d}\n", p.at)
	}
	return e.rollupLayoutReady(ctx)
}

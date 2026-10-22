package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

var errHistoryCapacity = errors.New("request history capacity unavailable")

// Drop complete oldest time partitions so PostgreSQL releases their files at
// commit. DELETE alone would retain the allocated heap and cannot enforce a
// physical capacity policy. Only analytics history is eligible for eviction.
func (e *Engine) enforceStorageBudget(ctx context.Context) error {
	for count := 0; count < 48; count++ {
		var size, high, target int64
		if err := e.DB.QueryRowContext(ctx, "SELECT pg_database_size(current_database()),ingest_limit_bytes,reclaim_target_bytes FROM storage_state WHERE singleton").Scan(&size, &high, &target); err != nil {
			return err
		}
		if size < high && (count == 0 || size < target) {
			return nil
		}
		tx, err := e.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var locked bool
		if err = tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(8395015)").Scan(&locked); err != nil || !locked {
			tx.Rollback()
			return errHistoryCapacity
		}
		// Another replica may have reclaimed space while this worker waited.
		// Recheck under the same lock that serializes fact admission and eviction.
		if err = tx.QueryRowContext(ctx, "SELECT pg_database_size(current_database()),ingest_limit_bytes,reclaim_target_bytes FROM storage_state WHERE singleton").Scan(&size, &high, &target); err != nil {
			tx.Rollback()
			return err
		}
		if size < high && (count == 0 || size < target) {
			tx.Rollback()
			return nil
		}
		var start, end int64
		var name string
		err = tx.QueryRowContext(ctx, "SELECT start_ms,end_ms,name FROM fact_partitions ORDER BY start_ms LIMIT 1").Scan(&start, &end, &name)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return errHistoryCapacity
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if name != fmt.Sprintf("facts_%d", start) {
			tx.Rollback()
			return errors.New("invalid retention partition")
		}
		var events int64
		if err = e.retainEvictedReceiptClaims(ctx, tx, name); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM "+name).Scan(&events); err == nil {
			_, err = tx.ExecContext(ctx, "DROP TABLE "+name)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM fact_partitions WHERE start_ms=?", start)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS usage_%d", start))
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS billing_%d", start))
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM fact_source_ranges WHERE start_ms=?", start)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM imported_objects WHERE max_at_ms<?", end)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM rollups WHERE level IN ('minute','hour') AND period_ms>=? AND period_ms<?", start, end)
		}
		if err == nil {
			d := time.UnixMilli(start).In(e.Location)
			day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, e.Location)
			err = e.rebuildRollup(ctx, tx, "day", day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli())
			if err == nil {
				err = reclaimRollupPartitions(ctx, tx, start, day.UnixMilli(), end >= day.AddDate(0, 0, 1).UnixMilli())
			}
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "UPDATE storage_state SET evicted_before_ms=greatest(evicted_before_ms,?),evicted_events=evicted_events+?,revision=revision+1 WHERE singleton", end, events)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		log.Printf("event=analytics_history_evicted before_ms=%d events=%d database_bytes_before=%d", end, events, size)
	}
	return errHistoryCapacity
}

func reclaimRollupPartitions(ctx context.Context, tx *analyticsTx, hour, day int64, dayExpired bool) error {
	rows, err := tx.QueryContext(ctx, "SELECT start_ms,name FROM rollup_partitions WHERE end_ms<=? OR (start_ms=? AND start_ms<>?) OR (start_ms=? AND ?)", day, hour, day, day, dayExpired)
	if err != nil {
		return err
	}
	type partition struct {
		start int64
		name  string
	}
	var parts []partition
	for rows.Next() {
		var p partition
		if err = rows.Scan(&p.start, &p.name); err != nil {
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
		if p.name != fmt.Sprintf("rollups_%d", p.start) {
			return errors.New("invalid rollup retention partition")
		}
		if _, err = tx.ExecContext(ctx, "DROP TABLE "+p.name); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM rollup_partitions WHERE start_ms=?", p.start); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) retentionLoop(ctx context.Context) {
	for ctx.Err() == nil {
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := s.engine.enforceStorageBudget(call)
		if err == nil {
			err = s.trimReceiptHistory(call)
		}
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("event=analytics_capacity_unavailable class=%s", databaseErrorClass(err))
		}
		if !waitStartup(ctx, 10*time.Second) {
			return
		}
	}
}

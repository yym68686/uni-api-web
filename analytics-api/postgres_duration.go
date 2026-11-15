package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Additive metadata only: old code keeps using its explicit column lists.
// Do not change the immutable 0001 schema or scan history on the ready path.
const durationRollupSchema = `ALTER TABLE rollups
 ADD COLUMN duration_count BIGINT DEFAULT 0,
 ADD COLUMN duration_sum DOUBLE PRECISION DEFAULT 0,
 ADD COLUMN duration_bins BIGINT[],
 ADD COLUMN last_duration DOUBLE PRECISION,
 ADD COLUMN last_duration_ms BIGINT;`

const durationRollupColumns = "duration_count,duration_sum,duration_bins,last_duration,last_duration_ms"

var (
	errDurationPreparationBusy     = errors.New("duration preparation deferred for live ingestion; retry preparation")
	errDurationPreparationHeadroom = errors.New("duration preparation requires storage headroom; wait for bounded FIFO maintenance")
)

func (e *Engine) initializeDurationRollups(ctx context.Context) error {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395014)"); err != nil {
		return err
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(durationRollupSchema)))
	var saved string
	err = tx.QueryRowContext(ctx, "SELECT checksum FROM schema_versions WHERE version='0006'").Scan(&saved)
	if err == nil {
		if saved != checksum {
			return errors.New("duration rollup schema requires explicit migration")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, durationRollupSchema); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_versions(version,checksum) VALUES('0006',?)", checksum); err != nil {
		return err
	}
	return tx.Commit()
}

// Only a completed attempt owns this clock. Request durations include earlier
// retries; dispatch/trace/billing/skipped facts must never enter the histogram.
func durationFactAggregate() string {
	v := "(CASE WHEN kind='attempt' AND coalesce(outcome,'')<>'skipped' AND duration_ms>=0 AND isfinite(duration_ms) THEN duration_ms END)"
	return "count(" + v + "),coalesce(sum(" + v + "),0)," + histogramSQL(v) +
		",(max(ARRAY[at_ms::DOUBLE PRECISION," + v + "]) FILTER(WHERE " + v + " IS NOT NULL))[2],max(at_ms) FILTER(WHERE " + v + " IS NOT NULL)"
}

func durationRollupAggregate() string {
	return "coalesce(sum(duration_count),0)::BIGINT,coalesce(sum(duration_sum),0)," + mergeHistogramSQL("duration_bins") + "::BIGINT[]" +
		",(max(ARRAY[last_duration_ms::DOUBLE PRECISION,last_duration]) FILTER(WHERE last_duration IS NOT NULL AND last_duration_ms IS NOT NULL))[2],max(last_duration_ms)"
}

func durationRollupMerge() []string {
	bins := make([]string, len(histogramBounds))
	for i := range bins {
		bins[i] = fmt.Sprintf("coalesce(rollups.duration_bins[%d],0)+coalesce(excluded.duration_bins[%d],0)", i+1, i+1)
	}
	return []string{
		"duration_count=coalesce(rollups.duration_count,0)+excluded.duration_count",
		"duration_sum=coalesce(rollups.duration_sum,0)+excluded.duration_sum",
		"duration_bins=ARRAY[" + strings.Join(bins, ",") + "]",
		"last_duration=CASE WHEN excluded.last_duration_ms>coalesce(rollups.last_duration_ms,0) OR (excluded.last_duration_ms=rollups.last_duration_ms AND excluded.last_duration>=rollups.last_duration) THEN excluded.last_duration ELSE rollups.last_duration END",
		"last_duration_ms=greatest(rollups.last_duration_ms,excluded.last_duration_ms)",
	}
}

// Explicit, restartable preparation after every older writer has drained.
// Each transaction replaces ONLY duration metadata for one minute (then one
// hour/day), takes the ingest/FIFO lock non-blockingly, and has a 1s deadline.
// No request path scans facts or waits for this historical preparation.
func (e *Engine) backfillDurationRollups(ctx context.Context, pace time.Duration) error {
	rows, err := e.DB.QueryContext(ctx, "SELECT start_ms FROM fact_partitions ORDER BY start_ms DESC")
	if err != nil {
		return err
	}
	var hours []int64
	for rows.Next() {
		var at int64
		if err = rows.Scan(&at); err != nil {
			rows.Close()
			return err
		}
		hours = append(hours, at)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	days := map[int64]bool{}
	for _, hour := range hours {
		for minute := hour; minute < hour+3600000; minute += 60000 {
			if err = e.prepareDurationBucketWithRetry(ctx, "minute", minute, minute+60000); err != nil {
				return err
			}
			if !waitStartup(ctx, max(pace, 25*time.Millisecond)) {
				return ctx.Err()
			}
		}
		if err = e.prepareDurationBucketWithRetry(ctx, "hour", hour, hour+3600000); err != nil {
			return err
		}
		local := time.UnixMilli(hour).In(e.Location)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, e.Location).UnixMilli()
		days[day] = true
		fmt.Printf("{\"phase\":\"duration_hour_prepared\",\"hour\":%d}\n", hour)
	}
	for day := range days {
		end := time.UnixMilli(day).In(e.Location).AddDate(0, 0, 1).UnixMilli()
		if err = e.prepareDurationBucketWithRetry(ctx, "day", day, end); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) prepareDurationBucketWithRetry(ctx context.Context, level string, start, end int64) error {
	for attempt := 0; attempt < 100; attempt++ {
		err := e.prepareDurationBucket(ctx, level, start, end)
		if !errors.Is(err, errDurationPreparationBusy) {
			return err
		}
		if !waitStartup(ctx, 100*time.Millisecond) {
			return ctx.Err()
		}
	}
	return errDurationPreparationBusy
}

func (e *Engine) prepareDurationBucket(ctx context.Context, level string, start, end int64) error {
	if level != "minute" && level != "hour" && level != "day" {
		return errors.New("invalid duration rollup level")
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var locked bool
	if err = tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(8395015)").Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return errDurationPreparationBusy
	}
	if _, err = tx.ExecContext(ctx, "SET LOCAL statement_timeout='1s';SET LOCAL lock_timeout='100ms'"); err != nil {
		return err
	}
	// Updating a rollup creates a new tuple/WAL even though only additive
	// metadata changes. Maintenance must preserve ingestion's storage reserve.
	var room bool
	if err = tx.QueryRowContext(ctx, "SELECT pg_database_size(current_database())+33554432<ingest_limit_bytes FROM storage_state WHERE singleton").Scan(&room); err != nil {
		return err
	}
	if !room {
		return errDurationPreparationHeadroom
	}
	dims := strings.TrimPrefix(rollupDimensions, "period_ms,level,")
	selects := dims
	source := ""
	if level == "minute" {
		// COALESCE matches the writer's canonical rollup dimensions.
		expressions := []string{}
		positions := []string{}
		for i, field := range strings.Split(dims, ",") {
			expr := "coalesce(" + field + ",'')"
			if field == "stream" {
				expr = "coalesce(stream,false)"
			}
			expressions = append(expressions, expr+" AS "+field)
			positions = append(positions, fmt.Sprint(i+1))
		}
		source = "SELECT " + strings.Join(expressions, ",") + "," + durationFactAggregate() + " FROM facts WHERE kind='attempt' AND at_ms>=? AND at_ms<? GROUP BY " + strings.Join(positions, ",")
	} else {
		lower := "minute"
		if level == "day" {
			lower = "hour"
		}
		source = "SELECT " + selects + "," + durationRollupAggregate() + " FROM rollups WHERE kind='attempt' AND level='" + lower + "' AND period_ms>=? AND period_ms<? GROUP BY " + dims
	}
	// Named CTE columns keep aggregates independent of their SQL expressions.
	keys := []string{}
	for _, name := range strings.Split(dims, ",") {
		keys = append(keys, "r."+name+"=a."+name)
	}
	cols := strings.Split(durationRollupColumns, ",")
	values := make([]string, len(cols))
	for i, name := range cols {
		values[i] = "a." + name
	}
	current := make([]string, len(cols))
	for i, name := range cols {
		current[i] = "r." + name
	}
	// A restart must not rewrite already prepared tuples and consume reserve
	// again. Compare every matched identity, but update only changed metadata.
	var expected, matched, changed, updated int64
	err = tx.QueryRowContext(ctx, "WITH a("+dims+","+durationRollupColumns+") AS MATERIALIZED ("+source+"), matched AS MATERIALIZED (SELECT a.*,("+strings.Join(current, ",")+") IS DISTINCT FROM ("+strings.Join(values, ",")+") AS changed FROM a JOIN rollups r ON "+strings.Join(keys, " AND ")+" WHERE r.level=? AND r.period_ms=?), updated AS (UPDATE rollups r SET ("+durationRollupColumns+")=("+strings.Join(values, ",")+") FROM matched a WHERE a.changed AND r.level=? AND r.period_ms=? AND "+strings.Join(keys, " AND ")+" RETURNING 1) SELECT (SELECT count(*) FROM a),(SELECT count(*) FROM matched),(SELECT count(*) FROM matched WHERE changed),(SELECT count(*) FROM updated)", start, end, level, start, level, start).Scan(&expected, &matched, &changed, &updated)
	if err != nil {
		return err
	}
	if expected != matched || changed != updated {
		return errors.New("duration preparation differs from retained rollup identities")
	}
	if updated > 0 {
		if _, err = tx.ExecContext(ctx, "UPDATE storage_state SET revision=revision+1 WHERE singleton"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed postgres_schema.sql
var postgresAnalyticsSchema string

func openPostgresEngine(namespace string, cfg Config) (*Engine, error) {
	dsn := cfg.ControlDatabaseURL
	schema := "analytics"
	if dsn == "" {
		// File-style identities are retained only for isolated test fixtures.
		dsn = os.Getenv("TEST_CONTROL_DATABASE_URL")
		if dsn == "" {
			return nil, errors.New("CONTROL_DATABASE_URL is required")
		}
		schema = fmt.Sprintf("analytics_test_%x", sha256.Sum256([]byte(namespace)))[:40]
		if strings.HasPrefix(namespace, "analytics_test_") && len(namespace) == 40 {
			schema = namespace
		}
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	pc, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid analytics database configuration")
	}
	pc.RuntimeParams["search_path"] = schema + ",public"
	pc.RuntimeParams["application_name"] = "uni-api-analytics"
	pc.RuntimeParams["statement_timeout"] = "20000"
	pc.RuntimeParams["lock_timeout"] = "1000"
	// A failed analytical plan must not consume the whole managed volume with
	// temporary files. This is a per-session guard; it does not alter cluster
	// configuration or the control tables.
	pc.RuntimeParams["temp_file_limit"] = "65536"
	pc.RuntimeParams["work_mem"] = "8192"
	db := stdlib.OpenDB(*pc)
	db.SetMaxOpenConns(6)
	db.SetMaxIdleConns(3)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, errors.New("analytics database unavailable")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		db.Close()
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395014)"); err != nil {
		db.Close()
		return nil, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", schema+".storage_state").Scan(&exists); err == nil && !exists {
		ddl := strings.ReplaceAll(postgresAnalyticsSchema, "analytics", schema)
		_, err = tx.ExecContext(ctx, ddl)
		if err == nil {
			_, err = tx.ExecContext(ctx, "INSERT INTO schema_versions(version,checksum) VALUES('0001',$1)", fmt.Sprintf("%x", sha256.Sum256([]byte(postgresAnalyticsSchema))))
		}
	} else if err == nil {
		var checksum string
		err = tx.QueryRowContext(ctx, "SELECT checksum FROM schema_versions WHERE version='0001'").Scan(&checksum)
		if err == nil && checksum != fmt.Sprintf("%x", sha256.Sum256([]byte(postgresAnalyticsSchema))) {
			err = errors.New("analytics schema checksum changed; explicit migration required")
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	e := &Engine{DB: &analyticsDB{DB: db}, cfg: cfg, Location: loc, historyPath: schema, analyticalSlots: make(chan struct{}, 1), operations: make(map[*databaseOperation]string)}
	if err = e.seedDefaultPrices(); err != nil {
		db.Close()
		return nil, err
	}
	return e, nil
}

func (e *Engine) readSnapshot(ctx context.Context) (*Engine, func(), error) {
	tx, err := e.DB.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	view := &Engine{DB: &analyticsDB{DB: e.DB.DB, snapshot: tx}, cfg: e.cfg, Location: e.Location}
	var revision uint64
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM storage_state WHERE singleton").Scan(&revision); err != nil {
		tx.Rollback()
		return nil, nil, err
	}
	view.Revision.Store(revision)
	return view, func() { _ = tx.Rollback() }, nil
}

func (e *Engine) currentRevision(ctx context.Context) (uint64, error) {
	var revision uint64
	err := e.DB.QueryRowContext(ctx, "SELECT revision FROM storage_state WHERE singleton").Scan(&revision)
	if err == nil {
		e.Revision.Store(revision)
	}
	return revision, err
}

func ensureFactPartition(ctx context.Context, tx *analyticsTx, at int64) error {
	const width = int64(3600000)
	start := at / width * width
	end := start + width
	name := fmt.Sprintf("facts_%d", start)
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM fact_partitions WHERE start_ms=?)", start).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s PARTITION OF facts FOR VALUES FROM (%d) TO (%d)", name, start, end)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO fact_partitions(start_ms,end_ms,name) VALUES(?,?,?)", start, end, name)
	return err
}

const factColumns = "event_id,kind,source_id,instance_id,request_id,attempt_id,at_ms,started_ms,key_id,provider,model,upstream_model,endpoint,stream,outcome,status,duration_ms,dispatch_ms,first_output_ms,response_created_ms,first_text_ms,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cache_write_1h_tokens,actual_cost_usd,upstream_base,upstream_key_hash,billing_request_ids,upstream_error_sha256,trace_id,stage,trace_detail,transport_timing,terminal_kind,failure_reason,response_completed"

const factRecordTypes = `event_id TEXT,kind TEXT,source_id TEXT,instance_id TEXT,request_id TEXT,attempt_id TEXT,at_ms BIGINT,started_ms BIGINT,key_id TEXT,provider TEXT,model TEXT,upstream_model TEXT,endpoint TEXT,stream BOOLEAN,outcome TEXT,status INTEGER,duration_ms DOUBLE PRECISION,dispatch_ms DOUBLE PRECISION,first_output_ms DOUBLE PRECISION,response_created_ms DOUBLE PRECISION,first_text_ms DOUBLE PRECISION,input_tokens BIGINT,output_tokens BIGINT,cache_read_tokens BIGINT,cache_write_tokens BIGINT,cache_write_1h_tokens BIGINT,actual_cost_usd DOUBLE PRECISION,upstream_base TEXT,upstream_key_hash TEXT,billing_request_ids TEXT[],upstream_error_sha256 TEXT,trace_id TEXT,stage TEXT,trace_detail JSONB,transport_timing JSONB,terminal_kind TEXT,failure_reason TEXT,response_completed BOOLEAN`

func (e *Engine) insertFactPage(ctx context.Context, tx *analyticsTx, facts []Fact) (int64, error) {
	raw, err := json.Marshal(facts)
	if err != nil {
		return 0, err
	}
	columns := strings.Split(factColumns, ",")
	for i, c := range columns {
		columns[i] = "incoming." + c
		if c == "trace_detail" || c == "transport_timing" {
			columns[i] += "::text"
		}
	}
	// DISTINCT ON also prevents a corrupt retry changing an event timestamp from
	// creating a second row in another partition inside the same statement.
	incoming := "(SELECT DISTINCT ON(event_id) * FROM jsonb_to_recordset(?::jsonb) AS r(" + factRecordTypes + ") ORDER BY event_id,at_ms) incoming"
	query := "WITH inserted AS (INSERT INTO facts(" + factColumns + ") SELECT " + strings.Join(columns, ",") + " FROM " + incoming + " WHERE NOT EXISTS(SELECT 1 FROM facts known WHERE known.event_id=incoming.event_id) ON CONFLICT DO NOTHING RETURNING *), metrics AS (SELECT inserted.*,periods.* FROM inserted CROSS JOIN LATERAL (VALUES ('minute'::text,at_ms/60000*60000),('hour'::text,at_ms/3600000*3600000),('day'::text,(extract(epoch FROM (date_trunc('day',to_timestamp(at_ms/1000.0) AT TIME ZONE ?) AT TIME ZONE ?))*1000)::bigint)) periods(level,period_ms)), aggregated AS (INSERT INTO rollups(" + rollupColumns() + ") " + factRollupSelect("metrics") + rollupMerge() + " RETURNING 1), coverage AS (INSERT INTO fact_source_ranges(source_id,start_ms,first_ms,last_ms) SELECT source_id,at_ms/3600000*3600000,min(at_ms),max(at_ms) FROM inserted GROUP BY source_id,at_ms/3600000*3600000 ON CONFLICT(source_id,start_ms) DO UPDATE SET first_ms=least(fact_source_ranges.first_ms,excluded.first_ms),last_ms=greatest(fact_source_ranges.last_ms,excluded.last_ms) RETURNING 1) SELECT count(*) FROM inserted"
	args := []any{string(raw), e.Location.String(), e.Location.String()}
	metrics := false
	for _, f := range facts {
		if f.Kind != "billing" && f.Kind != "trace" {
			metrics = true
			break
		}
	}
	if !metrics {
		start := strings.Index(query, ", metrics AS (")
		end := strings.Index(query, ", coverage AS (")
		query = query[:start] + query[end:]
		args = []any{string(raw)}
	}

	var n int64
	err = tx.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

func factArgs(f Fact) []any {
	text := func(raw []byte) any {
		if len(raw) == 0 {
			return nil
		}
		return string(raw)
	}
	return []any{f.EventID, f.Kind, f.SourceID, f.InstanceID, f.RequestID, f.AttemptID, f.AtMS, f.StartedMS, f.KeyID, f.Provider, f.Model, f.UpstreamModel, f.Endpoint, f.Stream, f.Outcome, f.Status, f.DurationMS, f.DispatchMS, f.FirstOutputMS, f.ResponseCreatedMS, f.FirstTextMS, f.InputTokens, f.OutputTokens, f.CacheReadTokens, f.CacheWriteTokens, f.CacheWrite1hTokens, f.ActualCostUSD, f.UpstreamBase, f.UpstreamKeyHash, f.BillingRequestIDs, f.UpstreamErrorSHA256, f.TraceID, f.Stage, text(f.TraceDetail), text(f.TransportTiming), f.TerminalKind, f.FailureReason, f.ResponseCompleted}
}

// Serializable admission across replicas; publication of facts, rollups,
// deduplication and the shared revision is one transaction.
func (e *Engine) importPostgres(ctx context.Context, objects []FactObject) (err error) {
	tx, err := e.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395015)"); err != nil {
		return err
	}
	var size, high int64
	if err = tx.QueryRowContext(ctx, "SELECT pg_database_size(current_database()),ingest_limit_bytes FROM storage_state WHERE singleton").Scan(&size, &high); err != nil {
		return err
	}
	if size >= high {
		return errHistoryCapacity
	}
	var evicted int64
	if err = tx.QueryRowContext(ctx, "SELECT evicted_before_ms FROM storage_state WHERE singleton").Scan(&evicted); err != nil {
		return err
	}
	rollupPartitions := map[int64]bool{}
	changed := false
	partitions := map[int64]bool{}
	for _, obj := range objects {
		var old string
		err = tx.QueryRowContext(ctx, "SELECT etag FROM imported_objects WHERE object_key=?", obj.Key).Scan(&old)
		if err == nil {
			if old != obj.ETag {
				return errors.New("immutable batch changed")
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var maxAt int64
		page := make([]Fact, 0, 512)
		flush := func() error {
			if len(page) == 0 {
				return nil
			}
			inserted, err := e.insertFactPage(ctx, tx, page)
			if err != nil {
				return err
			}
			changed = changed || inserted > 0
			page = page[:0]
			return nil
		}
		for _, f := range obj.Facts {
			if f.SourceID == "" {
				f.SourceID = e.cfg.SourceID
				if f.SourceID == "" {
					f.SourceID = "primary"
				}
			}
			if err = validFact(f); err != nil {
				return err
			}
			if f.AtMS < evicted {
				continue
			}
			maxAt = max(maxAt, f.AtMS)
			hour := f.AtMS / 3600000
			if !partitions[hour] {
				if err = ensureFactPartition(ctx, tx, f.AtMS); err != nil {
					return err
				}
				partitions[hour] = true
			}
			if f.Kind != "trace" && f.Kind != "billing" {
				d := time.UnixMilli(f.AtMS).In(e.Location)
				day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, e.Location).UnixMilli()
				for _, at := range []int64{f.AtMS, day} {
					hour := at / 3600000
					if !rollupPartitions[hour] {
						if err = ensureRollupPartition(ctx, tx, at); err != nil {
							return err
						}
						rollupPartitions[hour] = true
					}
				}
			}
			reason := factFailureReason(f)
			f.Outcome = factMetricOutcome(f)
			if reason != "" {
				f.Outcome = "failed/" + reason
			}
			if f.Kind == "billing" {
				f.UpstreamBase = subBindingSite(f.UpstreamBase)
			}
			page = append(page, f)
			if len(page) == cap(page) {
				if err = flush(); err != nil {
					return err
				}
			}
		}
		if err = flush(); err != nil {
			return err
		}
		if maxAt > 0 {
			if _, err = tx.ExecContext(ctx, "INSERT INTO imported_objects(object_key,etag,events,max_at_ms) VALUES(?,?,?,?)", obj.Key, obj.ETag, len(obj.Facts), maxAt); err != nil {
				return err
			}
		}
	}

	if changed {
		if _, err = tx.ExecContext(ctx, "UPDATE storage_state SET revision=revision+1 WHERE singleton"); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// PostgreSQL does not auto-analyze a partitioned parent. Large backfills
	// must publish statistics before analytical joins plan against the new rows.
	var total int
	for _, object := range objects {
		total += len(object.Facts)
	}
	if total >= 5000 {
		if _, err = e.DB.ExecContext(ctx, "ANALYZE facts"); err != nil {
			return err
		}
	}
	_, err = e.currentRevision(ctx)
	return err
}

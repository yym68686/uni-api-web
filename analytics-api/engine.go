package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

var histogramBounds = []float64{1, 2, 5, 10, 20, 50, 100, 200, 300, 500, 750, 1000, 1500, 2000, 3000, 5000, 7500, 10000, 15000, 20000, 30000, 45000, 60000, 90000, 120000, 180000, 300000, 600000, 1200000, math.Inf(1)}

type Engine struct {
	DB       *sql.DB
	cfg      Config
	mu       sync.Mutex
	Revision atomic.Uint64
	Location *time.Location
}

func OpenEngine(path string, cfg Config) (*Engine, error) {
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	e := &Engine{DB: db, cfg: cfg, Location: location}
	if _, err = db.Exec(`SET memory_limit='512MB'; SET threads=2;
 CREATE TABLE IF NOT EXISTS facts(event_id VARCHAR PRIMARY KEY,kind VARCHAR NOT NULL,instance_id VARCHAR,request_id VARCHAR,attempt_id VARCHAR,at_ms BIGINT,started_ms BIGINT,key_id VARCHAR,provider VARCHAR,model VARCHAR,upstream_model VARCHAR,endpoint VARCHAR,stream BOOLEAN,outcome VARCHAR,status INTEGER,duration_ms DOUBLE,dispatch_ms DOUBLE,first_output_ms DOUBLE,input_tokens BIGINT,output_tokens BIGINT,cache_read_tokens BIGINT,cache_write_tokens BIGINT,cache_write_1h_tokens BIGINT,actual_cost_usd DOUBLE);
 CREATE TABLE IF NOT EXISTS imported_objects(object_key VARCHAR PRIMARY KEY,etag VARCHAR,imported_at TIMESTAMP DEFAULT current_timestamp,events BIGINT);
 CREATE TABLE IF NOT EXISTS prices(model VARCHAR PRIMARY KEY,input DOUBLE,output DOUBLE,cache_read DOUBLE,cache_write DOUBLE,cache_write_1h DOUBLE,source VARCHAR,verified BOOLEAN,effective_at TIMESTAMP);
 CREATE TABLE IF NOT EXISTS price_history(model VARCHAR,document VARCHAR,updated_at TIMESTAMP DEFAULT current_timestamp);
 CREATE TABLE IF NOT EXISTS meta(name VARCHAR PRIMARY KEY,value VARCHAR);
 CREATE TABLE IF NOT EXISTS rollups(period_ms BIGINT,level VARCHAR,kind VARCHAR,key_id VARCHAR,provider VARCHAR,model VARCHAR,upstream_model VARCHAR,endpoint VARCHAR,stream BOOLEAN,outcome VARCHAR,n BIGINT,input_tokens HUGEINT,output_tokens HUGEINT,cache_read_tokens HUGEINT,cache_write_tokens HUGEINT,cache_write_1h_tokens HUGEINT,usage_samples BIGINT,cache_samples BIGINT,actual_cost_usd DOUBLE,actual_cost_samples BIGINT,first_bins BIGINT[],dispatch_bins BIGINT[],first_count BIGINT,dispatch_count BIGINT,first_sum DOUBLE,dispatch_sum DOUBLE,last_ms BIGINT,last_first DOUBLE,last_dispatch DOUBLE);
 CREATE INDEX IF NOT EXISTS facts_at ON facts(at_ms);`); err != nil {
		db.Close()
		return nil, err
	}
	return e, nil
}
func (e *Engine) Close() error { return e.DB.Close() }
func validFact(f Fact) error {
	if f.Schema != 1 || f.EventID == "" || len(f.EventID) > 256 || f.AtMS <= 0 || f.AtMS > time.Now().Add(5*time.Minute).UnixMilli() {
		return errors.New("invalid fact identity or timestamp")
	}
	if f.Kind != "request" && f.Kind != "attempt" && f.Kind != "dispatch" {
		return errors.New("unknown fact kind")
	}
	for _, v := range []string{f.RequestID, f.AttemptID, f.KeyID, f.Provider, f.Model, f.UpstreamModel, f.Endpoint, f.InstanceID} {
		if len(v) > 512 {
			return errors.New("fact field too long")
		}
	}
	for _, v := range []*int64{f.InputTokens, f.OutputTokens, f.CacheReadTokens, f.CacheWriteTokens, f.CacheWrite1hTokens} {
		if v != nil && *v < 0 {
			return errors.New("negative token usage")
		}
	}
	for _, v := range []*float64{f.DurationMS, f.DispatchMS, f.FirstOutputMS, f.ActualCostUSD} {
		if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return errors.New("invalid fact measurement")
		}
	}
	return nil
}
func (e *Engine) Imported(ctx context.Context, key, etag string) (bool, error) {
	var stored string
	err := e.DB.QueryRowContext(ctx, "SELECT etag FROM imported_objects WHERE object_key=?", key).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stored != etag {
		return false, errors.New("immutable S3 fact object changed")
	}
	return true, nil
}
func (e *Engine) Import(ctx context.Context, key, etag string, facts []Fact) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	imported, err := e.Imported(ctx, key, etag)
	if err != nil || imported {
		return err
	}
	for _, f := range facts {
		if err = validFact(f); err != nil {
			return err
		}
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO facts VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	minutes := map[int64]bool{}
	days := map[int64]bool{}
	for _, f := range facts {
		_, err = stmt.ExecContext(ctx, f.EventID, f.Kind, f.InstanceID, f.RequestID, f.AttemptID, f.AtMS, f.StartedMS, f.KeyID, f.Provider, f.Model, f.UpstreamModel, f.Endpoint, f.Stream, f.Outcome, f.Status, f.DurationMS, f.DispatchMS, f.FirstOutputMS, f.InputTokens, f.OutputTokens, f.CacheReadTokens, f.CacheWriteTokens, f.CacheWrite1hTokens, f.ActualCostUSD)
		if err != nil {
			return err
		}
		minutes[f.AtMS/60000*60000] = true
		local := time.UnixMilli(f.AtMS).In(e.Location)
		days[time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, e.Location).UnixMilli()] = true
	}
	for minute := range minutes {
		if err = e.rebuildRollup(ctx, tx, "minute", minute, minute+60000); err != nil {
			return err
		}
	}
	for day := range days {
		end := time.UnixMilli(day).In(e.Location).AddDate(0, 0, 1).UnixMilli()
		if err = e.rebuildRollup(ctx, tx, "day", day, end); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO imported_objects(object_key,etag,events) VALUES(?,?,?)", key, etag, len(facts)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	e.Revision.Add(1)
	return nil
}
func histogramSQL(column string) string {
	parts := make([]string, len(histogramBounds))
	for i, b := range histogramBounds {
		condition := column + " IS NOT NULL"
		if !math.IsInf(b, 1) {
			condition += fmt.Sprintf(" AND %s<=%g", column, b)
		}
		if i > 0 {
			condition += fmt.Sprintf(" AND %s>%g", column, histogramBounds[i-1])
		}
		parts[i] = "count(*) FILTER (WHERE " + condition + ")"
	}
	return "[" + strings.Join(parts, ",") + "]"
}
func (e *Engine) rebuildRollup(ctx context.Context, tx *sql.Tx, level string, start, end int64) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM rollups WHERE level=? AND period_ms=?", level, start); err != nil {
		return err
	}
	// Only partitions receiving new data are rebuilt. Daily rows combine minute
	// aggregates, so long windows never scan request-level records.
	if level == "day" {
		_, err := tx.ExecContext(ctx, `INSERT INTO rollups SELECT ?, 'day',kind,key_id,provider,model,upstream_model,endpoint,stream,outcome,sum(n),sum(input_tokens),sum(output_tokens),sum(cache_read_tokens),sum(cache_write_tokens),sum(cache_write_1h_tokens),sum(usage_samples),sum(cache_samples),sum(actual_cost_usd),sum(actual_cost_samples),`+mergeHistogramSQL("first_bins")+`,`+mergeHistogramSQL("dispatch_bins")+`,sum(first_count),sum(dispatch_count),sum(first_sum),sum(dispatch_sum),max(last_ms),arg_max(last_first,last_ms),arg_max(last_dispatch,last_ms) FROM rollups WHERE level='minute' AND period_ms>=? AND period_ms<? GROUP BY kind,key_id,provider,model,upstream_model,endpoint,stream,outcome`, start, start, end)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO rollups SELECT ?, 'minute',kind,key_id,provider,model,upstream_model,endpoint,stream,outcome,count(*),sum(coalesce(input_tokens,0)),sum(coalesce(output_tokens,0)),sum(coalesce(cache_read_tokens,0)),sum(coalesce(cache_write_tokens,0)),sum(coalesce(cache_write_1h_tokens,0)),count(input_tokens),count(cache_read_tokens),sum(coalesce(actual_cost_usd,0)),count(actual_cost_usd),`+histogramSQL("first_output_ms")+`,`+histogramSQL("dispatch_ms")+`,count(first_output_ms),count(dispatch_ms),sum(coalesce(first_output_ms,0)),sum(coalesce(dispatch_ms,0)),max(at_ms),arg_max(first_output_ms,at_ms),arg_max(dispatch_ms,at_ms) FROM facts WHERE at_ms>=? AND at_ms<? GROUP BY kind,key_id,provider,model,upstream_model,endpoint,stream,outcome`, start, start, end)
	return err
}
func mergeHistogramSQL(column string) string {
	parts := make([]string, len(histogramBounds))
	for i := range parts {
		parts[i] = fmt.Sprintf("sum(%s[%d])", column, i+1)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
func (e *Engine) Prices(ctx context.Context) ([]Price, error) {
	rows, err := e.DB.QueryContext(ctx, `SELECT model,input,output,cache_read,cache_write,cache_write_1h,source,verified,effective_at FROM prices UNION ALL SELECT DISTINCT model,0,0,0,0,0,'fact-discovered',false,current_timestamp FROM facts WHERE model <> '' AND model NOT IN (SELECT model FROM prices) ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Price{}
	for rows.Next() {
		var p Price
		if err = rows.Scan(&p.Model, &p.Input, &p.Output, &p.CacheRead, &p.CacheWrite, &p.CacheWrite1h, &p.Source, &p.Verified, &p.EffectiveAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (e *Engine) SavePrice(ctx context.Context, p Price) error {
	if p.Model == "" || len(p.Model) > 512 {
		return errors.New("model required")
	}
	for _, v := range []float64{p.Input, p.Output, p.CacheRead, p.CacheWrite, p.CacheWrite1h} {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) || v > 1e9 {
			return errors.New("invalid price")
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p.EffectiveAt = time.Now().UTC()
	raw, _ := json.Marshal(p)
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO prices VALUES(?,?,?,?,?,?,?,?,?)`, p.Model, p.Input, p.Output, p.CacheRead, p.CacheWrite, p.CacheWrite1h, p.Source, p.Verified, p.EffectiveAt); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO price_history(model,document) VALUES(?,?)", p.Model, string(raw)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	e.Revision.Add(1)
	return nil
}
func (e *Engine) ExportParquet(ctx context.Context, destination string, start, end int64) error {
	path, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	_, err = e.DB.ExecContext(ctx, fmt.Sprintf("COPY (SELECT * FROM facts WHERE at_ms >= %d AND at_ms < %d) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD)", start, end, strings.ReplaceAll(path, "'", "''")))
	return err
}

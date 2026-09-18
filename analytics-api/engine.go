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
 CREATE TABLE IF NOT EXISTS facts(event_id VARCHAR PRIMARY KEY,kind VARCHAR NOT NULL,source_id VARCHAR DEFAULT 'primary',instance_id VARCHAR,request_id VARCHAR,attempt_id VARCHAR,at_ms BIGINT,started_ms BIGINT,key_id VARCHAR,provider VARCHAR,model VARCHAR,upstream_model VARCHAR,endpoint VARCHAR,stream BOOLEAN,outcome VARCHAR,status INTEGER,duration_ms DOUBLE,dispatch_ms DOUBLE,first_output_ms DOUBLE,input_tokens BIGINT,output_tokens BIGINT,cache_read_tokens BIGINT,cache_write_tokens BIGINT,cache_write_1h_tokens BIGINT,actual_cost_usd DOUBLE);
 CREATE TABLE IF NOT EXISTS imported_objects(object_key VARCHAR PRIMARY KEY,etag VARCHAR,imported_at TIMESTAMP DEFAULT current_timestamp,events BIGINT);
 CREATE TABLE IF NOT EXISTS prices(model VARCHAR PRIMARY KEY,input DOUBLE,output DOUBLE,cache_read DOUBLE,cache_write DOUBLE,cache_write_1h DOUBLE,source VARCHAR,verified BOOLEAN,effective_at TIMESTAMP);
 CREATE TABLE IF NOT EXISTS price_history(model VARCHAR,document VARCHAR,updated_at TIMESTAMP DEFAULT current_timestamp);
 CREATE TABLE IF NOT EXISTS meta(name VARCHAR PRIMARY KEY,value VARCHAR);
 CREATE TABLE IF NOT EXISTS rollups(period_ms BIGINT,level VARCHAR,kind VARCHAR,source_id VARCHAR DEFAULT 'primary',key_id VARCHAR,provider VARCHAR,model VARCHAR,upstream_model VARCHAR,endpoint VARCHAR,stream BOOLEAN,outcome VARCHAR,n BIGINT,input_tokens HUGEINT,output_tokens HUGEINT,cache_read_tokens HUGEINT,cache_write_tokens HUGEINT,cache_write_1h_tokens HUGEINT,usage_samples BIGINT,cache_samples BIGINT,actual_cost_usd DOUBLE,actual_cost_samples BIGINT,first_bins BIGINT[],dispatch_bins BIGINT[],first_count BIGINT,dispatch_count BIGINT,first_sum DOUBLE,dispatch_sum DOUBLE,last_ms BIGINT,last_first DOUBLE,last_dispatch DOUBLE);
 ALTER TABLE facts ADD COLUMN IF NOT EXISTS source_id VARCHAR DEFAULT 'primary'; ALTER TABLE rollups ADD COLUMN IF NOT EXISTS source_id VARCHAR DEFAULT 'primary'; CREATE INDEX IF NOT EXISTS facts_at ON facts(at_ms);`); err != nil {
		db.Close()
		return nil, err
	}
	// New clocks are additive: old facts and cached rollups have no samples.
	if _, err = db.Exec(`
      ALTER TABLE facts ADD COLUMN IF NOT EXISTS response_created_ms DOUBLE;
      ALTER TABLE facts ADD COLUMN IF NOT EXISTS first_text_ms DOUBLE;
      ALTER TABLE rollups ADD COLUMN IF NOT EXISTS created_bins BIGINT[];
      ALTER TABLE rollups ADD COLUMN IF NOT EXISTS created_count BIGINT DEFAULT 0;
      ALTER TABLE rollups ADD COLUMN IF NOT EXISTS created_sum DOUBLE DEFAULT 0;
      ALTER TABLE rollups ADD COLUMN IF NOT EXISTS last_created DOUBLE;
      ALTER TABLE rollups ADD COLUMN IF NOT EXISTS text_bins BIGINT[];
      ALTER TABLE rollups ADD COLUMN IF NOT EXISTS text_count BIGINT DEFAULT 0;
      ALTER TABLE rollups ADD COLUMN IF NOT EXISTS text_sum DOUBLE DEFAULT 0;
      ALTER TABLE rollups ADD COLUMN IF NOT EXISTS last_text DOUBLE;
    `); err != nil {
		db.Close()
		return nil, err
	}
	if err := e.seedDefaultPrices(); err != nil {
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
	for _, v := range []*float64{f.DurationMS, f.DispatchMS, f.FirstOutputMS, f.ResponseCreatedMS, f.FirstTextMS, f.ActualCostUSD} {
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

type FactObject struct {
	Key, ETag string
	Facts     []Fact
}

func (e *Engine) Import(ctx context.Context, key, etag string, facts []Fact) error {
	return e.ImportBatch(ctx, []FactObject{{Key: key, ETag: etag, Facts: facts}})
}

// Validate every object before the transaction. Commit facts, affected rollups
// and object checkpoints atomically, so replay after a crash is idempotent.
func (e *Engine) ImportBatch(ctx context.Context, objects []FactObject) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	pending := make([]FactObject, 0, len(objects))
	seen := map[string]string{}
	for _, obj := range objects {
		if etag, ok := seen[obj.Key]; ok {
			if etag != obj.ETag {
				return errors.New("immutable object changed")
			}
			continue
		}
		seen[obj.Key] = obj.ETag
		imported, err := e.Imported(ctx, obj.Key, obj.ETag)
		if err != nil {
			return err
		}
		if imported {
			continue
		}
		for _, f := range obj.Facts {
			if f.SourceID == "" {
				f.SourceID = e.cfg.SourceID
				if f.SourceID == "" {
					f.SourceID = "primary"
				}
			}
			if err := validFact(f); err != nil {
				return err
			}
		}
		pending = append(pending, obj)
	}
	if len(pending) == 0 {
		return nil
	}
	// A single JSON scan avoids retaining a separate INSERT execution state
	// for every event in the transaction (large batches exhausted DuckDB's
	// memory budget even when the event payload itself was small).
	file, err := os.CreateTemp(e.cfg.DataDir, "fact-batch-*.jsonl")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	encoder := json.NewEncoder(file)
	var factCount int
	for _, obj := range pending {
		for _, fact := range obj.Facts {
			if fact.SourceID == "" {
				fact.SourceID = e.cfg.SourceID
				if fact.SourceID == "" {
					fact.SourceID = "primary"
				}
			}
			if err = encoder.Encode(fact); err != nil {
				file.Close()
				return err
			}
			factCount++
		}
	}
	if err = file.Close(); err != nil {
		return err
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if factCount > 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO facts BY NAME SELECT event_id,kind,source_id,instance_id,request_id,attempt_id,at_ms,started_ms,key_id,provider,model,upstream_model,endpoint,stream,outcome,status,duration_ms,dispatch_ms,first_output_ms,response_created_ms,first_text_ms,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cache_write_1h_tokens,actual_cost_usd FROM read_json(?,format='newline_delimited',columns={schema:'INTEGER',event_id:'VARCHAR',kind:'VARCHAR',source_id:'VARCHAR',instance_id:'VARCHAR',request_id:'VARCHAR',attempt_id:'VARCHAR',at_ms:'BIGINT',started_ms:'BIGINT',key_id:'VARCHAR',provider:'VARCHAR',model:'VARCHAR',upstream_model:'VARCHAR',endpoint:'VARCHAR',stream:'BOOLEAN',outcome:'VARCHAR',status:'INTEGER',duration_ms:'DOUBLE',dispatch_ms:'DOUBLE',first_output_ms:'DOUBLE',response_created_ms:'DOUBLE',first_text_ms:'DOUBLE',input_tokens:'BIGINT',output_tokens:'BIGINT',cache_read_tokens:'BIGINT',cache_write_tokens:'BIGINT',cache_write_1h_tokens:'BIGINT',actual_cost_usd:'DOUBLE'}) ON CONFLICT DO NOTHING`, file.Name())
		if err != nil {
			return err
		}
	}
	minutes, days := map[int64]bool{}, map[int64]bool{}
	for _, obj := range pending {
		for _, f := range obj.Facts {
			minutes[f.AtMS/60000*60000] = true
			local := time.UnixMilli(f.AtMS).In(e.Location)
			days[time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, e.Location).UnixMilli()] = true
		}
	}
	for start := 0; start < len(pending); start += 256 {
		end := min(start+256, len(pending))
		values := make([]string, 0, end-start)
		args := make([]any, 0, 3*(end-start))
		for _, obj := range pending[start:end] {
			values = append(values, "(?,?,?)")
			args = append(args, obj.Key, obj.ETag, len(obj.Facts))
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO imported_objects(object_key,etag,events) VALUES "+strings.Join(values, ","), args...); err != nil {
			return err
		}
	}
	for minute := range minutes {
		if err = e.rebuildRollup(ctx, tx, "minute", minute, minute+60000); err != nil {
			return err
		}
	}
	for day := range days {
		if err = e.rebuildRollup(ctx, tx, "day", day, time.UnixMilli(day).In(e.Location).AddDate(0, 0, 1).UnixMilli()); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	e.Revision.Add(1)
	return nil
}
func (e *Engine) ImportedObjects(ctx context.Context) (map[string]string, error) {
	rows, err := e.DB.QueryContext(ctx, "SELECT object_key,etag FROM imported_objects")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var key, etag string
		if err = rows.Scan(&key, &etag); err != nil {
			return nil, err
		}
		result[key] = etag
	}
	return result, rows.Err()
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
		_, err := tx.ExecContext(ctx, `INSERT INTO rollups(period_ms,level,kind,source_id,key_id,provider,model,upstream_model,endpoint,stream,outcome,n,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cache_write_1h_tokens,usage_samples,cache_samples,actual_cost_usd,actual_cost_samples,first_bins,dispatch_bins,first_count,dispatch_count,first_sum,dispatch_sum,last_ms,last_first,last_dispatch,created_bins,created_count,created_sum,last_created,text_bins,text_count,text_sum,last_text) SELECT ?, 'day',kind,source_id,key_id,provider,model,upstream_model,endpoint,stream,outcome,sum(n),sum(input_tokens),sum(output_tokens),sum(cache_read_tokens),sum(cache_write_tokens),sum(cache_write_1h_tokens),sum(usage_samples),sum(cache_samples),sum(actual_cost_usd),sum(actual_cost_samples),`+mergeHistogramSQL("first_bins")+`,`+mergeHistogramSQL("dispatch_bins")+`,sum(first_count),sum(dispatch_count),sum(first_sum),sum(dispatch_sum),max(last_ms),arg_max(last_first,last_ms),arg_max(last_dispatch,last_ms),`+mergeHistogramSQL("created_bins")+`,sum(created_count),sum(created_sum),arg_max(last_created,last_ms),`+mergeHistogramSQL("text_bins")+`,sum(text_count),sum(text_sum),arg_max(last_text,last_ms) FROM rollups WHERE level='minute' AND period_ms>=? AND period_ms<? GROUP BY kind,source_id,key_id,provider,model,upstream_model,endpoint,stream,outcome`, start, start, end)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO rollups(period_ms,level,kind,source_id,key_id,provider,model,upstream_model,endpoint,stream,outcome,n,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cache_write_1h_tokens,usage_samples,cache_samples,actual_cost_usd,actual_cost_samples,first_bins,dispatch_bins,first_count,dispatch_count,first_sum,dispatch_sum,last_ms,last_first,last_dispatch,created_bins,created_count,created_sum,last_created,text_bins,text_count,text_sum,last_text) SELECT ?, 'minute',kind,source_id,key_id,provider,model,upstream_model,endpoint,stream,outcome,count(*),sum(coalesce(input_tokens,0)),sum(coalesce(output_tokens,0)),sum(coalesce(cache_read_tokens,0)),sum(coalesce(cache_write_tokens,0)),sum(coalesce(cache_write_1h_tokens,0)),count(input_tokens),count(cache_read_tokens),sum(coalesce(actual_cost_usd,0)),count(actual_cost_usd),`+histogramSQL("first_output_ms")+`,`+histogramSQL("dispatch_ms")+`,count(first_output_ms),count(dispatch_ms),sum(coalesce(first_output_ms,0)),sum(coalesce(dispatch_ms,0)),max(at_ms),arg_max(first_output_ms,at_ms),arg_max(dispatch_ms,at_ms),`+histogramSQL("response_created_ms")+`,count(response_created_ms),sum(coalesce(response_created_ms,0)),arg_max(response_created_ms,at_ms),`+histogramSQL("first_text_ms")+`,count(first_text_ms),sum(coalesce(first_text_ms,0)),arg_max(first_text_ms,at_ms) FROM facts WHERE at_ms>=? AND at_ms<? GROUP BY kind,source_id,key_id,provider,model,upstream_model,endpoint,stream,outcome`, start, start, end)
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
	rows, err := e.DB.QueryContext(ctx, `SELECT model,input,output,cache_read,cache_write,cache_write_1h,source,verified,effective_at FROM prices UNION ALL SELECT DISTINCT model,0,0,0,0,0,'fact-discovered',false,current_timestamp FROM rollups WHERE model <> '' AND model NOT IN (SELECT model FROM prices) ORDER BY model`)
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

// These are editable reference prices in USD per million tokens. They are
// seeded once for models whose published rate is known; provider-specific or
// synthetic model names remain unpriced until an operator confirms a value.
var defaultPrices = map[string]Price{
	"gpt-6-astra":   {Input: 10, Output: 50, CacheRead: 1, Source: "OpenAI ChatGPT rate card", Verified: true},
	"gpt-5.6-sol":   {Input: 4, Output: 20, CacheRead: 0.4, Source: "OpenAI ChatGPT rate card", Verified: true},
	"gpt-5.6-terra": {Input: 2, Output: 12, CacheRead: 0.2, Source: "OpenAI ChatGPT rate card", Verified: true},
	"gpt-5.6-luna":  {Input: 0.2, Output: 1.2, CacheRead: 0.02, Source: "OpenAI ChatGPT rate card", Verified: true},
	"gpt-5.5":       {Input: 5, Output: 30, CacheRead: 0.5, Source: "OpenAI ChatGPT rate card", Verified: true},
	"gpt-5.4":       {Input: 2.5, Output: 15, CacheRead: 0.25, Source: "OpenAI API model page", Verified: true},
	"gpt-5.4-mini":  {Input: 0.75, Output: 4.5, CacheRead: 0.075, Source: "OpenAI API model page", Verified: true},
}

func (e *Engine) seedDefaultPrices() error {
	for model, price := range defaultPrices {
		var exists bool
		if err := e.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM prices WHERE model=?)", model).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := e.DB.Exec("INSERT INTO prices(model,input,output,cache_read,cache_write,cache_write_1h,source,verified,effective_at) VALUES(?,?,?,?,?,?,?,?,current_timestamp)", model, price.Input, price.Output, price.CacheRead, price.CacheWrite, price.CacheWrite1h, price.Source, price.Verified); err != nil {
			return err
		}
	}
	return nil
}
func validatePrice(p Price) error {
	if p.Model == "" || len(p.Model) > 512 {
		return errors.New("model required")
	}
	for _, v := range []float64{p.Input, p.Output, p.CacheRead, p.CacheWrite, p.CacheWrite1h} {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) || v > 1e9 {
			return errors.New("invalid price")
		}
	}
	if len(p.Source) > 2048 {
		return errors.New("price source too long")
	}
	return nil
}
func (e *Engine) SavePrice(ctx context.Context, p Price) error {
	if err := validatePrice(p); err != nil {
		return err
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

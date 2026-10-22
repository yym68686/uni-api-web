package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var histogramBounds = []float64{1, 2, 5, 10, 20, 50, 100, 200, 300, 500, 750, 1000, 1500, 2000, 3000, 5000, 7500, 10000, 15000, 20000, 30000, 45000, 60000, 90000, 120000, 180000, 300000, 600000, 1200000, math.Inf(1)}

type Engine struct {
	DB                *analyticsDB
	analyticalSlots   chan struct{}
	analyticalWaiting atomic.Int64
	spendMu           sync.Mutex
	spendCancels      map[uint64]context.CancelCauseFunc
	nextSpendID       uint64
	criticalReads     int
	operationsMu      sync.Mutex
	operations        map[*databaseOperation]string
	historyPath       string
	cfg               Config
	mu                sync.Mutex
	Revision          atomic.Uint64
	Location          *time.Location
}

func OpenEngine(path string, cfg Config) (*Engine, error) { return openPostgresEngine(path, cfg) }
func (e *Engine) Close() error                            { return e.DB.Close() }
func validFact(f Fact) error {
	if f.Schema != 1 || f.EventID == "" || len(f.EventID) > 256 || f.AtMS <= 0 || f.AtMS > time.Now().Add(5*time.Minute).UnixMilli() {
		return errors.New("invalid fact identity or timestamp")
	}
	if f.Kind != "request" && f.Kind != "attempt" && f.Kind != "dispatch" && f.Kind != "billing" && f.Kind != "trace" {
		return errors.New("unknown fact kind")
	}
	for _, v := range []string{f.TraceID, f.Stage, f.RequestID, f.AttemptID, f.KeyID, f.Provider, f.Model, f.UpstreamModel, f.Endpoint, f.InstanceID} {
		if len(v) > 512 {
			return errors.New("fact field too long")
		}
	}
	if len(f.UpstreamBase) > 2048 || len(f.UpstreamKeyHash) > 64 || len(f.BillingRequestIDs) > 3 {
		return errors.New("invalid receipt correlation")
	}
	for _, id := range f.BillingRequestIDs {
		if id == "" || len(id) > 256 {
			return errors.New("invalid receipt identifier")
		}
	}
	if f.UpstreamErrorSHA256 != "" && !validErrorDigest(f.UpstreamErrorSHA256) {
		return errors.New("invalid upstream error digest")
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
		return false, errors.New("immutable fact batch changed")
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
	return e.importPostgres(ctx, objects)
}

// ImportedObjectPage bounds memory to the current listing page. Source-prefixed
// keys preserve the existing cross-source and late-arrival identity semantics.
func (e *Engine) ImportedObjectPage(ctx context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for start := 0; start < len(keys); start += 1000 {
		end := min(start+1000, len(keys))
		args := make([]any, 0, end-start)
		for _, key := range keys[start:end] {
			args = append(args, key)
		}
		rows, err := e.DB.QueryContext(ctx, "SELECT object_key,etag FROM imported_objects WHERE object_key IN ("+strings.TrimRight(strings.Repeat("?,", len(args)), ",")+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key, etag string
			if err = rows.Scan(&key, &etag); err != nil {
				rows.Close()
				return nil, err
			}
			result[key] = etag
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
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
	return "ARRAY[" + strings.Join(parts, ",") + "]"
}

func mergeHistogramSQL(column string) string {
	parts := make([]string, len(histogramBounds))
	for i := range parts {
		parts[i] = fmt.Sprintf("coalesce(sum(%s[%d]) FILTER (WHERE %s[%d]<>0),0)", column, i+1, column, i+1)
	}
	return "ARRAY[" + strings.Join(parts, ",") + "]"
}
func (e *Engine) Prices(ctx context.Context) ([]Price, error) {
	return e.readPrices(ctx, true)
}

func (e *Engine) readPrices(ctx context.Context, discover bool) ([]Price, error) {
	q := `SELECT model,input,output,cache_read,cache_write,cache_write_1h,source,verified,effective_at,charge_cache_write,sale_percent,long_context_premium FROM prices`
	if discover {
		q += ` UNION ALL SELECT DISTINCT model,0,0,0,0,0,'fact-discovered',false,current_timestamp,NULL::BOOLEAN,NULL::DOUBLE PRECISION,NULL::BOOLEAN FROM rollups WHERE model <> '' AND model NOT IN (SELECT model FROM prices)`
	}
	rows, err := e.DB.QueryContext(ctx, q+` ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Price{}
	for rows.Next() {
		var p Price
		if err = rows.Scan(&p.Model, &p.Input, &p.Output, &p.CacheRead, &p.CacheWrite, &p.CacheWrite1h, &p.Source, &p.Verified, &p.EffectiveAt, &p.ChargeCacheWrite, &p.SalePercent, &p.LongContextPremium); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return withCatalogPrices(out), rows.Err()
}

// These are editable reference prices in USD per million tokens. They are
// seeded once for models whose published rate is known; provider-specific or
// synthetic model names remain unpriced until an operator confirms a value.
var defaultPrices = func() map[string]Price {
	// Retain prices for legacy historical models, without offering them in the
	// detector-aligned settings list.
	prices := map[string]Price{
		"gpt-5.4":      {Input: 2.5, Output: 15, CacheRead: 0.25, Source: "OpenAI API model page", Verified: true},
		"gpt-5.4-mini": {Input: 0.75, Output: 4.5, CacheRead: 0.075, Source: "OpenAI API model page", Verified: true},
	}
	for _, price := range modelCatalog {
		prices[price.Model] = price
	}
	return prices
}()

func (e *Engine) seedDefaultPrices() error {
	ctx := context.Background()
	var ready bool
	if err := e.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM meta WHERE name='price_defaults_initialized')").Scan(&ready); err != nil {
		return err
	}
	if ready {
		return nil
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395016)"); err != nil {
		return err
	}
	var initialized bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM meta WHERE name IN ('price_defaults_initialized','legacy_price_baseline')) OR EXISTS(SELECT 1 FROM prices)").Scan(&initialized); err != nil {
		return err
	}
	if !initialized {
		for model, price := range defaultPrices {
			if _, err = tx.ExecContext(ctx, "INSERT INTO prices(model,input,output,cache_read,cache_write,cache_write_1h,source,verified,effective_at) VALUES(?,?,?,?,?,?,?,?,current_timestamp) ON CONFLICT(model) DO NOTHING", model, price.Input, price.Output, price.CacheRead, price.CacheWrite, price.CacheWrite1h, price.Source, price.Verified); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO meta(name,value) VALUES('price_defaults_initialized','true') ON CONFLICT DO NOTHING"); err != nil {
		return err
	}
	return tx.Commit()
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
	if p.SalePercent != nil && (*p.SalePercent < 0 || *p.SalePercent > 1e6 || math.IsNaN(*p.SalePercent) || math.IsInf(*p.SalePercent, 0)) {
		return errors.New("invalid sale percentage")
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
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395016)"); err != nil {
		return err
	}
	// An older client omitting the new field must not erase a saved override.
	if p.SalePercent == nil {
		err := tx.QueryRowContext(ctx, "SELECT sale_percent FROM prices WHERE model=?", p.Model).Scan(&p.SalePercent)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if p.LongContextPremium == nil {
		err := tx.QueryRowContext(ctx, "SELECT long_context_premium FROM prices WHERE model=?", p.Model).Scan(&p.LongContextPremium)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	p.EffectiveAt = time.Now().UTC()
	raw, _ := json.Marshal(p)
	if _, err = tx.ExecContext(ctx, `INSERT INTO prices(model,input,output,cache_read,cache_write,cache_write_1h,source,verified,effective_at,charge_cache_write,sale_percent,long_context_premium) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(model) DO UPDATE SET input=excluded.input,output=excluded.output,cache_read=excluded.cache_read,cache_write=excluded.cache_write,cache_write_1h=excluded.cache_write_1h,source=excluded.source,verified=excluded.verified,effective_at=excluded.effective_at,charge_cache_write=excluded.charge_cache_write,sale_percent=excluded.sale_percent,long_context_premium=excluded.long_context_premium`, p.Model, p.Input, p.Output, p.CacheRead, p.CacheWrite, p.CacheWrite1h, p.Source, p.Verified, p.EffectiveAt, p.ChargeCacheWrite, p.SalePercent, p.LongContextPremium); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO price_history(model,document) VALUES(?,?)", p.Model, string(raw)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE storage_state SET revision=revision+1 WHERE singleton"); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	_, err = e.currentRevision(ctx)
	return err
}

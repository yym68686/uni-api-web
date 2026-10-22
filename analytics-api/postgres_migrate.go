package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// Explicit one-time data migration; the normal HTTP process never imports a
// snapshot, reads S3, or performs a full archive scan on startup.
func migratePostgres(args []string) error {
	flags := flag.NewFlagSet("migrate-postgresql", flag.ContinueOnError)
	prices := flags.String("prices", "", "validated legacy price JSON document")
	merge := flags.Bool("merge-final-prices", false, "merge final legacy writes after old writers have drained")
	indexes := flags.Bool("prepare-indexes", false, "prepare the existing receipt FIFO index without blocking writes")
	readIndexes := flags.Bool("prepare-analytics-indexes", false, "prepare partitioned request-usage indexes concurrently")
	usage := flags.Bool("prepare-usage-projection", false, "backfill and verify compact transactional usage rows")
	layout := flags.Bool("prepare-rollup-layout", false, "separate rollup levels before initial activation")
	facts := flags.Bool("facts-stdin", false, "import NDJSON facts from stdin")
	analyze := flags.Bool("analyze", false, "publish parent planner statistics once after the complete backfill")
	verify := flags.Bool("verify-queries", false, "run bounded read-only analytics acceptance queries")
	claimsUnavailable := flags.Bool("mark-legacy-claims-incomplete", false, "record that legacy receipt ownership exceeds the bounded retained history")
	pace := flags.Duration("batch-delay", 0, "pause between migration batches to protect interactive work")
	phase := flags.String("phase", "", "set prepared after migration validation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg := Config{ControlDatabaseURL: env("CONTROL_DATABASE_URL", env("DATABASE_URL", "")), Timezone: env("ANALYTICS_TIMEZONE", "Asia/Shanghai"), SourceID: env("SOURCE_ID", "primary")}
	if cfg.ControlDatabaseURL == "" {
		return errors.New("migration requires PostgreSQL")
	}
	e, err := openPostgresEngine("analytics", cfg)
	if err != nil {
		return err
	}
	defer e.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if *claimsUnavailable {
		tx, err := e.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395015)"); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE storage_state SET claims_history_complete=false,revision=revision+1 WHERE singleton"); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO meta(name,value) VALUES('legacy_claims_validation','incomplete_capacity') ON CONFLICT(name) DO NOTHING"); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		fmt.Println(`{"phase":"legacy_claims_incomplete","reason":"bounded_history"}`)
	}
	if *layout {
		if err = e.separateRollupLevels(ctx); err != nil {
			return err
		}
	}
	if *usage {
		if err = e.backfillUsageProjection(ctx); err != nil {
			return err
		}
	}
	if *readIndexes {
		if err = e.prepareReadIndexes(ctx); err != nil {
			return err
		}
		fmt.Println(`{"phase":"analytics_indexes_prepared"}`)
	}
	if *indexes {
		conn, err := e.DB.DB.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		if _, err = conn.ExecContext(ctx, "SET statement_timeout='10min';SET lock_timeout='60s';SET maintenance_work_mem='32MB';SET temp_file_limit='256MB'"); err != nil {
			return err
		}
		// A cancelled concurrent build leaves an invalid index. Only repair
		// this migration-owned index; a valid existing index is never removed.
		var invalid bool
		if err = conn.QueryRowContext(ctx, "SELECT coalesce((SELECT NOT indisvalid FROM pg_index WHERE indexrelid=to_regclass('public.console_sub_spend_retention')),false)").Scan(&invalid); err != nil {
			return err
		}
		if invalid {
			if _, err = conn.ExecContext(ctx, "DROP INDEX CONCURRENTLY public.console_sub_spend_retention"); err != nil {
				return err
			}
		}
		if _, err = conn.ExecContext(ctx, "CREATE INDEX CONCURRENTLY IF NOT EXISTS console_sub_spend_retention ON public.console_sub_spend_logs(at_ms)"); err != nil {
			return err
		}
		if err = conn.QueryRowContext(ctx, "SELECT coalesce((SELECT NOT indisvalid FROM pg_index WHERE indexrelid=to_regclass('public.console_sub_usage_retention')),false)").Scan(&invalid); err != nil {
			return err
		}
		if invalid {
			if _, err = conn.ExecContext(ctx, "DROP INDEX CONCURRENTLY public.console_sub_usage_retention"); err != nil {
				return err
			}
		}
		if _, err = conn.ExecContext(ctx, "CREATE INDEX CONCURRENTLY IF NOT EXISTS console_sub_usage_retention ON public.console_sub_usage(account_id,started_at) WHERE status<>'pending'"); err != nil {
			return err
		}
		fmt.Println(`{"phase":"receipt_index_prepared"}`)
	}
	if *prices != "" {
		file, err := os.Open(*prices)
		if err != nil {
			return errors.New("price file unavailable")
		}
		defer file.Close()
		var doc struct {
			Schema int     `json:"schema"`
			Prices []Price `json:"prices"`
		}
		decoder := json.NewDecoder(io.LimitReader(file, 32<<20))
		if err = decoder.Decode(&doc); err != nil || doc.Schema != 1 {
			return errors.New("invalid migration price document")
		}
		if err = e.migrateLegacyPrices(ctx, doc.Prices, *merge); err != nil {
			return err
		}
		if _, err = e.DB.ExecContext(ctx, "INSERT INTO meta(name,value) VALUES('legacy_prices_imported','true') ON CONFLICT(name) DO UPDATE SET value=excluded.value"); err != nil {
			return err
		}
		fmt.Printf("{\"phase\":\"prices_imported\",\"models\":%d}\n", len(doc.Prices))
	}
	if *facts {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		page := make([]Fact, 0, 512)
		var count int64
		flush := func() error {
			if len(page) == 0 {
				return nil
			}
			if err := e.enforceStorageBudget(ctx); err != nil {
				return err
			}
			raw, err := json.Marshal(page)
			if err != nil {
				return err
			}
			digest := tokenHash(string(raw))
			if err = e.Import(ctx, "migration::"+digest, digest, page); err != nil {
				return err
			}
			count += int64(len(page))
			page = page[:0]
			if *pace > 0 && !waitStartup(ctx, *pace) {
				return ctx.Err()
			}
			return nil
		}
		for scanner.Scan() {
			var f Fact
			if err = json.Unmarshal(scanner.Bytes(), &f); err != nil {
				return errors.New("invalid migration fact")
			}
			page = append(page, f)
			if len(page) == cap(page) {
				if err = flush(); err != nil {
					return err
				}
			}
		}
		if err = scanner.Err(); err != nil {
			return err
		}
		if err = flush(); err != nil {
			return err
		}
		fmt.Printf("{\"phase\":\"facts_imported\",\"events\":%d}\n", count)
	}
	if *analyze {
		conn, err := e.DB.DB.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		// Migration maintenance has its own bounded budget. Interactive sessions
		// keep their existing timeout. Do this once, not after every hourly file.
		if _, err = conn.ExecContext(ctx, "SET statement_timeout='60s'"); err != nil {
			return err
		}
		for _, table := range []string{"facts", "rollups", "usage_facts"} {
			began := time.Now()
			if _, err = conn.ExecContext(ctx, "ANALYZE "+table); err != nil {
				return err
			}
			fmt.Printf("{\"phase\":\"analyzed\",\"table\":%q,\"elapsed_ms\":%d}\n", table, time.Since(began).Milliseconds())
		}
	}
	if *verify {
		for _, window := range []string{"3h", "24h", "all"} {
			for _, chart := range []bool{false, true} {
				call, cancel := context.WithTimeout(ctx, 10*time.Second)
				result, err := e.Query(call, QueryFilter{Range: window, SourceID: cfg.SourceID, Timeseries: chart})
				cancel()
				if err != nil {
					return err
				}
				fmt.Printf("{\"phase\":\"query_verified\",\"range\":%q,\"timeseries\":%t,\"channels\":%d,\"query_ms\":%.2f,\"coverage\":%q}\n", window, chart, len(result.Data), result.DurationMS, result.Coverage)
			}
		}
	}
	if *phase != "" {
		var claimsValidated bool
		if err = e.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM meta WHERE name='legacy_claims_validation')").Scan(&claimsValidated); err != nil || !claimsValidated {
			return errors.New("legacy receipt ownership coverage must be explicitly validated")
		}
		if err = e.rollupLayoutReady(ctx); err != nil {
			return err
		}
		if err = e.usageProjectionReady(ctx); err != nil {
			return err
		}
		if err = e.readIndexesReady(ctx); err != nil {
			return err
		}
		if *phase != "prepared" && *phase != "complete" {
			return errors.New("invalid migration phase")
		}
		var imported bool
		if err = e.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM meta WHERE name='legacy_prices_imported' AND value='true')").Scan(&imported); err != nil || !imported {
			return errors.New("saved prices must be migrated first")
		}
		var conflicts int
		if err = e.DB.QueryRowContext(ctx, "SELECT count(*) FROM price_migration_conflicts").Scan(&conflicts); err != nil {
			return err
		}
		if conflicts != 0 {
			return errors.New("price migration conflicts require resolution before activation")
		}
		var hasFacts bool
		if err = e.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM facts)").Scan(&hasFacts); err != nil {
			return err
		}
		if !hasFacts {
			return errors.New("retained request history must be migrated before activation")
		}
		var indexReady bool
		if err = e.DB.QueryRowContext(ctx, "SELECT count(*)=2 FROM pg_index WHERE indisvalid AND indexrelid IN (to_regclass('public.console_sub_spend_retention'),to_regclass('public.console_sub_usage_retention'))").Scan(&indexReady); err != nil || !indexReady {
			return errors.New("receipt retention index must be prepared before activation")
		}
		if _, err = e.DB.ExecContext(ctx, "INSERT INTO meta(name,value) VALUES('legacy_migration_phase',?) ON CONFLICT(name) DO UPDATE SET value=excluded.value", *phase); err != nil {
			return err
		}
		fmt.Printf("{\"phase\":%q,\"at\":%q}\n", *phase, time.Now().UTC().Format(time.RFC3339))
	}
	var bytes int64
	if err = e.DB.QueryRowContext(ctx, "SELECT pg_database_size(current_database())").Scan(&bytes); err != nil {
		return err
	}
	fmt.Printf("{\"phase\":\"storage\",\"database_bytes\":%d}\n", bytes)
	return nil
}

// A baseline permits a three-way merge after old replicas drain. Concurrent
// edits are preserved as explicit conflicts, never resolved by clock guesses.
func (e *Engine) migrateLegacyPrices(ctx context.Context, prices []Price, final bool) error {
	seen := map[string]bool{}
	for _, p := range prices {
		if err := validatePrice(p); err != nil {
			return err
		}
		if seen[p.Model] {
			return errors.New("duplicate migration model price")
		}
		seen[p.Model] = true
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395016)"); err != nil {
		return err
	}
	var baselineRaw string
	baselineErr := tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE name='legacy_price_baseline'").Scan(&baselineRaw)
	if !final && baselineErr == nil {
		return errors.New("price baseline already imported; use final merge without replacing current edits")
	}
	if final && baselineErr != nil {
		return errors.New("price baseline missing")
	}
	var baseline []Price
	if baselineErr == nil {
		if err = json.Unmarshal([]byte(baselineRaw), &baseline); err != nil {
			return errors.New("invalid price baseline")
		}
	}
	if !final {
		var edited bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM price_history)").Scan(&edited); err != nil {
			return err
		}
		if edited {
			return errors.New("operator price edits exist before legacy baseline; explicit reconciliation required")
		}
		// The legacy document owns the complete saved set, including absence.
		// Fresh bootstrap defaults must not be promoted into saved operator intent.
		if _, err = tx.ExecContext(ctx, "DELETE FROM prices"); err != nil {
			return err
		}
	}
	current := map[string]Price{}
	rows, err := tx.QueryContext(ctx, `SELECT model,input,output,cache_read,cache_write,cache_write_1h,source,verified,effective_at,charge_cache_write,sale_percent,long_context_premium FROM prices`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p Price
		if err = rows.Scan(&p.Model, &p.Input, &p.Output, &p.CacheRead, &p.CacheWrite, &p.CacheWrite1h, &p.Source, &p.Verified, &p.EffectiveAt, &p.ChargeCacheWrite, &p.SalePercent, &p.LongContextPremium); err != nil {
			rows.Close()
			return err
		}
		current[p.Model] = p
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	before := map[string]Price{}
	for _, p := range baseline {
		before[p.Model] = p
	}
	equivalent := func(a, b Price) bool {
		a.EffectiveAt = time.Time{}
		b.EffectiveAt = time.Time{}
		a.PromptPriceTier = nil
		b.PromptPriceTier = nil
		return mustJSON(a) == mustJSON(b)
	}
	sort.Slice(prices, func(i, j int) bool { return prices[i].Model < prices[j].Model })
	conflicts := 0
	for _, p := range prices {
		old, wasPresent := before[p.Model]
		now, isPresent := current[p.Model]
		if final && wasPresent && equivalent(old, p) {
			continue
		}
		if final && isPresent && !equivalent(now, p) && (!wasPresent || !equivalent(old, now)) {
			if _, err = tx.ExecContext(ctx, `INSERT INTO price_migration_conflicts(model,baseline,current_price,legacy_price) VALUES(?,?::jsonb,?::jsonb,?::jsonb) ON CONFLICT(model) DO UPDATE SET baseline=excluded.baseline,current_price=excluded.current_price,legacy_price=excluded.legacy_price`, p.Model, mustJSON(old), mustJSON(now), mustJSON(p)); err != nil {
				return err
			}
			conflicts++
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO prices(model,input,output,cache_read,cache_write,cache_write_1h,source,verified,effective_at,charge_cache_write,sale_percent,long_context_premium) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(model) DO UPDATE SET input=excluded.input,output=excluded.output,cache_read=excluded.cache_read,cache_write=excluded.cache_write,cache_write_1h=excluded.cache_write_1h,source=excluded.source,verified=excluded.verified,effective_at=excluded.effective_at,charge_cache_write=excluded.charge_cache_write,sale_percent=excluded.sale_percent,long_context_premium=excluded.long_context_premium`, p.Model, p.Input, p.Output, p.CacheRead, p.CacheWrite, p.CacheWrite1h, p.Source, p.Verified, p.EffectiveAt, p.ChargeCacheWrite, p.SalePercent, p.LongContextPremium); err != nil {
			return err
		}
	}
	if !final {
		if _, err = tx.ExecContext(ctx, "INSERT INTO meta(name,value) VALUES('legacy_price_baseline',?)", mustJSON(prices)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE storage_state SET revision=revision+1 WHERE singleton"); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if conflicts > 0 {
		return errors.New("concurrent price migration conflicts preserved for review")
	}
	return nil
}

func (e *Engine) verifyLegacyMigration(ctx context.Context) error {
	if err := e.rollupLayoutReady(ctx); err != nil {
		return err
	}
	if err := e.usageProjectionReady(ctx); err != nil {
		return err
	}
	if err := e.readIndexesReady(ctx); err != nil {
		return err
	}
	if os.Getenv("STATE_S3_BUCKET") == "" && os.Getenv("S3_BUCKET") == "" {
		return nil
	}
	var phase string
	if err := e.DB.QueryRowContext(ctx, "SELECT value FROM meta WHERE name='legacy_migration_phase'").Scan(&phase); err != nil {
		return errors.New("PostgreSQL migration has not been validated; retaining previous serving release")
	}
	if phase != "prepared" && phase != "complete" {
		return errors.New("PostgreSQL migration is not ready")
	}
	return nil
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func postgresTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := OpenEngine(filepath.Join(t.TempDir(), "postgres"), Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func stateTestEngine(t *testing.T) *Engine { return postgresTestEngine(t) }
func replicaTestEngine(t *testing.T, source *Engine) *Engine {
	t.Helper()
	e, err := OpenEngine(source.historyPath, source.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func TestPostgresFactsSurviveReplicaRestart(t *testing.T) {
	e := postgresTestEngine(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute).UnixMilli()
	f := Fact{Schema: 1, EventID: "one", SourceID: "primary", Kind: "attempt", AtMS: at, Provider: "channel", Model: "m", Outcome: "success"}
	if err := e.Import(ctx, "batch", "hash", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	if err := e.Import(ctx, "batch", "hash", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	result, err := e.Query(ctx, QueryFilter{Range: "all", Timeseries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 {
		t.Fatalf("unexpected rows: %+v", result)
	}
	if err := e.SavePrice(ctx, Price{Model: "m", Input: 12, Verified: true}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.DB.QueryRow("SELECT count(*) FROM facts").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestPostgresParametersPreserveQuotedQuestionMarks(t *testing.T) {
	got := postgresQuery(`SELECT '?' AS text,"?" AS name,?,'it''s ?' WHERE x=?`)
	if got != `SELECT '?' AS text,"?" AS name,$1,'it''s ?' WHERE x=$2` {
		t.Fatal(got)
	}
}

func TestDisjointRollupSlicesMatchMinuteTruthAcrossTimezones(t *testing.T) {
	for _, zone := range []string{"UTC", "Asia/Shanghai", "Asia/Kathmandu", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			e, err := OpenEngine(filepath.Join(t.TempDir(), "slices"), Config{Timezone: zone})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			ctx := context.Background()
			to := time.Now().UTC().Truncate(time.Minute)
			var facts []Fact
			for i := 0; i < 200; i++ {
				facts = append(facts, Fact{Schema: 1, EventID: fmt.Sprint(i), Kind: "request", SourceID: "primary", AtMS: to.Add(-time.Duration(i) * 23 * time.Minute).UnixMilli(), Model: "m", Outcome: "success", KeyID: "key"})
			}
			if err = e.Import(ctx, "slices", "v1", facts); err != nil {
				t.Fatal(err)
			}
			for _, window := range []string{"5m", "1h", "24h", "7d", "today", "all"} {
				f := QueryFilter{Range: window, To: to.Unix(), SourceID: "primary", KeyID: "key"}
				_, start, _, _, err := e.rollupWindow(f)
				if err != nil {
					t.Fatal(err)
				}
				var want int64
				if err = e.DB.QueryRowContext(ctx, "SELECT coalesce(sum(n),0)::bigint FROM rollups WHERE level='minute' AND kind='request' AND period_ms>=? AND period_ms<=?", start, to.UnixMilli()).Scan(&want); err != nil {
					t.Fatal(err)
				}
				got, err := e.Query(ctx, f)
				if err != nil {
					t.Fatal(window, err)
				}
				if got.Total["requests"] != float64(want) {
					t.Fatalf("%s: requests=%v, minute truth=%d", window, got.Total["requests"], want)
				}
			}
		})
	}
}

func TestReceiptFIFORemovesFinalizedHistoryAndPreservesPendingTasks(t *testing.T) {
	s, account, _ := attributionFixture(t)
	ctx := context.Background()
	floor := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := s.engine.DB.Exec("UPDATE storage_state SET evicted_before_ms=?", floor); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id, status string
		at         int64
	}{{"done-old", "complete", floor/1000 - 60}, {"pending-old", "pending", floor/1000 - 60}, {"done-new", "complete", floor/1000 + 60}} {
		if _, err := s.control.db.Exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status) VALUES($1,$2,7,42,'m',$3,'[]',$4)`, account+row.id, account, row.at, row.status); err != nil {
			t.Fatal(err)
		}
	}
	// An older receipt can belong to a later key: the eligibility probe must
	// not mistake the first key's recent receipt for the whole account's floor.
	for _, row := range []struct{ key, at int64 }{{1, floor + 60}, {42, floor}, {99, floor - 60}} {
		if _, err := s.control.db.Exec(`INSERT INTO console_sub_spend_logs(account_id,key_id,log_id,at_ms,actual_cost,request_id,model) VALUES($1,$2,1,$3,1,'fixture','m')`, account, row.key, row.at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.control.db.Exec("UPDATE console_sub_accounts SET usage_lease_until=now()+interval '1 hour' WHERE id=$1", account); err != nil {
		t.Fatal(err)
	}
	if err := s.trimReceiptHistory(ctx); err != nil {
		t.Fatal(err)
	}
	var protected int
	if err := s.control.db.QueryRow("SELECT count(*) FROM console_sub_spend_logs WHERE account_id=$1", account).Scan(&protected); err != nil || protected != 3 {
		t.Fatal("active account receipts were removed", protected, err)
	}
	if err := s.control.db.QueryRow("SELECT count(*) FROM console_sub_usage WHERE account_id=$1", account).Scan(&protected); err != nil || protected != 3 {
		t.Fatal("active account tasks were removed", protected, err)
	}
	if _, err := s.control.db.Exec("UPDATE console_sub_accounts SET usage_lease_until=now()-interval '1 second' WHERE id=$1", account); err != nil {
		t.Fatal(err)
	}
	if err := s.trimReceiptHistory(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.control.db.QueryRow("SELECT count(*) FROM console_sub_usage WHERE account_id=$1", account).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if err := s.control.db.QueryRow("SELECT count(*) FROM console_sub_usage WHERE account_id=$1 AND status='pending'", account).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := s.control.db.QueryRow("SELECT count(*) FROM console_sub_spend_logs WHERE account_id=$1 AND at_ms>=$2", account, floor).Scan(&n); err != nil || n != 2 {
		t.Fatal("removed a current receipt or exact floor", n, err)
	}
	if err := s.control.db.QueryRow("SELECT count(*) FROM console_sub_spend_logs WHERE account_id=$1 AND at_ms<$2", account, floor).Scan(&n); err != nil || n != 0 {
		t.Fatal("missed the expired receipt on a later key", n, err)
	}
	// No expired receipt/task remains; the next pass must be harmless.
	if err := s.trimReceiptHistory(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresFIFOReclaimsFilesPreservesPricesAndRejectsExpiredReplay(t *testing.T) {
	// Capacity is database-wide. Other fixtures' vacuum/temp cleanup must not
	// alter this test's deliberately tiny threshold between its two readings.
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		t.Fatal("isolated PostgreSQL URL required")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "analytics_budget_" + tokenHash(t.TempDir())[:16]
	if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
	u.Path = "/" + name
	e, err := OpenEngine("analytics", Config{ControlDatabaseURL: u.String(), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	old := Fact{Schema: 1, EventID: "old", Kind: "request", AtMS: now.Add(-3 * time.Hour).UnixMilli(), Provider: "p", Model: "m", Outcome: "success"}
	fresh := old
	fresh.EventID = "fresh"
	fresh.AtMS = now.Add(-time.Hour).UnixMilli()
	for _, f := range []Fact{old, fresh} {
		if err := e.Import(ctx, f.EventID, "hash", []Fact{f}); err != nil {
			t.Fatal(err)
		}
	}
	oldBill, freshBill := old, fresh
	oldBill.Kind, oldBill.EventID = "billing", "old-billing"
	freshBill.Kind, freshBill.EventID = "billing", "fresh-billing"
	if err := e.Import(ctx, "billing-retention", "one", []Fact{oldBill, freshBill}); err != nil {
		t.Fatal(err)
	}
	if err := e.SavePrice(ctx, Price{Model: "operator", Input: 77, Verified: true}); err != nil {
		t.Fatal(err)
	}
	var size, oldBytes int64
	if err := e.DB.QueryRow("SELECT pg_database_size(current_database())").Scan(&size); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow("SELECT pg_total_relation_size(name::regclass) FROM fact_partitions ORDER BY start_ms LIMIT 1").Scan(&oldBytes); err != nil {
		t.Fatal(err)
	}
	if oldBytes < 8192 {
		t.Fatal("partition fixture empty")
	}
	if _, err := e.DB.Exec("UPDATE storage_state SET ingest_limit_bytes=?,reclaim_target_bytes=?", size-1, size-oldBytes/2); err != nil {
		t.Fatal(err)
	}
	if err := e.enforceStorageBudget(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	var price float64
	if err := e.DB.QueryRow("SELECT count(*) FROM facts WHERE event_id='old'").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := e.DB.QueryRow("SELECT count(*) FROM billing_facts").Scan(&n); err != nil || n != 1 {
		t.Fatal("billing projection did not follow FIFO", n, err)
	}
	if err := e.DB.QueryRow("SELECT input FROM prices WHERE model='operator'").Scan(&price); err != nil || price != 77 {
		t.Fatal(price, err)
	}
	if err := e.Import(ctx, "expired-retry", "same", []Fact{old}); err != nil {
		t.Fatal(err)
	}
	result, err := e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total["requests"] != float64(1) {
		t.Fatalf("evicted aggregates returned or fresh history lost: %+v", result.Total)
	}
}

func TestPostgresReplicaSeesCommittedPriceRevision(t *testing.T) {
	e := stateTestEngine(t)
	replica := replicaTestEngine(t, e)
	ctx := context.Background()
	before, err := replica.currentRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flag := true
	if err = e.SavePrice(ctx, Price{Model: "replica", Input: 9, LongContextPremium: &flag}); err != nil {
		t.Fatal(err)
	}
	if err = replica.SavePrice(ctx, Price{Model: "replica", Input: 10}); err != nil {
		t.Fatal(err)
	}
	var saved bool
	if err = e.DB.QueryRow("SELECT long_context_premium FROM prices WHERE model='replica'").Scan(&saved); err != nil || !saved {
		t.Fatal(saved, err)
	}
	after, err := replica.currentRevision(ctx)
	if err != nil || after <= before {
		t.Fatal(before, after, err)
	}
}

func TestPostgresEvictedReceiptStillPreventsDoubleAttribution(t *testing.T) {
	s, account, owner := attributionFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	old := attributedFact("evicted-claim", "caller-old", now.Add(-3*time.Hour))
	old.BillingRequestIDs = []string{"client:shared"}
	fresh := attributedFact("current-claim", "caller", now.Add(-time.Hour))
	fresh.BillingRequestIDs = []string{"client:shared"}
	for _, f := range []Fact{old, fresh} {
		if err := s.engine.Import(ctx, f.EventID, "v1", []Fact{f}); err != nil {
			t.Fatal(err)
		}
	}
	storeAttributedLog(t, s, account, "shared", "1.25", 1)
	tx, err := s.engine.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var partition string
	if err = tx.QueryRowContext(ctx, "SELECT name FROM fact_partitions ORDER BY start_ms LIMIT 1").Scan(&partition); err != nil {
		t.Fatal(err)
	}
	if err = s.engine.retainEvictedReceiptClaims(ctx, tx, partition); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "DROP TABLE "+partition); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := s.attributedChannelSpend(ctx, owner, QueryFilter{SourceID: "source", KeyID: "caller"}, now.Add(-2*time.Hour).Unix(), now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Ambiguous != 1 || rows[0].Amount != nil {
		t.Fatalf("eviction created false ownership: %+v", rows)
	}
}

func TestPostgresLegacyPriceMergePreservesConcurrentEdits(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	baseline := []Price{{Model: "unchanged", Input: 1}, {Model: "pg-edited", Input: 2}, {Model: "legacy-edited", Input: 3}, {Model: "both-edited", Input: 4}}
	if err := e.migrateLegacyPrices(ctx, baseline, false); err != nil {
		t.Fatal(err)
	}
	if err := e.migrateLegacyPrices(ctx, baseline, false); err == nil {
		t.Fatal("baseline overwritten")
	}
	if err := e.SavePrice(ctx, Price{Model: "pg-edited", Input: 20}); err != nil {
		t.Fatal(err)
	}
	if err := e.SavePrice(ctx, Price{Model: "both-edited", Input: 40}); err != nil {
		t.Fatal(err)
	}
	latest := []Price{{Model: "unchanged", Input: 1}, {Model: "pg-edited", Input: 2}, {Model: "legacy-edited", Input: 30}, {Model: "both-edited", Input: 44}}
	if err := e.migrateLegacyPrices(ctx, latest, true); err == nil {
		t.Fatal("conflicting writes silently merged")
	}
	for model, want := range map[string]float64{"unchanged": 1, "pg-edited": 20, "legacy-edited": 30, "both-edited": 40} {
		var got float64
		if err := e.DB.QueryRow("SELECT input FROM prices WHERE model=?", model).Scan(&got); err != nil || got != want {
			t.Fatal(model, got, want, err)
		}
	}
	var conflicts int
	if err := e.DB.QueryRow("SELECT count(*) FROM price_migration_conflicts WHERE model='both-edited'").Scan(&conflicts); err != nil || conflicts != 1 {
		t.Fatal(conflicts, err)
	}
}

func TestPostgresIncrementalRollupsMatchRebuildWithLateEventsAndRetries(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute).UnixMilli()
	f := Fact{Schema: 1, SourceID: "primary", EventID: "new", Kind: "attempt", AtMS: at + 1000, Model: "m", Provider: "p", Outcome: "success"}
	v := 100.0
	f.FirstOutputMS = &v
	if err := e.Import(ctx, "a", "a", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	f.EventID = "old"
	f.AtMS = at
	v2 := 50.0
	f.FirstOutputMS = &v2
	if err := e.Import(ctx, "b", "b", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	duplicate := f
	duplicate.AtMS = at + 2000
	if err := e.Import(ctx, "c", "c", []Fact{duplicate, f}); err != nil {
		t.Fatal(err)
	}
	f.EventID = "null"
	f.AtMS = at + 3000
	f.FirstOutputMS = nil
	if err := e.Import(ctx, "d", "d", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var raw string
		if err := e.DB.QueryRow("SELECT json_agg(r ORDER BY level,period_ms)::text FROM rollups r").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot()
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = e.rebuildRollup(ctx, tx, "minute", at, at+60000); err != nil {
		t.Fatal(err)
	}
	day := time.UnixMilli(at).UTC().Truncate(24 * time.Hour).UnixMilli()
	if err = e.rebuildRollup(ctx, tx, "day", day, day+86400000); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); before != after {
		t.Fatalf("incremental differs from retained fact rebuild:\n%s\n%s", before, after)
	}
	var n int
	var last float64
	if err = e.DB.QueryRow("SELECT n,last_first FROM rollups WHERE level='day'").Scan(&n, &last); err != nil || n != 3 || last != 100 {
		t.Fatal(n, last, err)
	}
}

func TestPostgresDayRebuildUsesEquivalentHoursWithoutScanningMinutes(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	day := time.Now().UTC().Add(-48 * time.Hour).Truncate(24 * time.Hour)
	var facts []Fact
	for i := 0; i < 72; i++ {
		at := day.Add(time.Duration(i%24)*time.Hour + time.Duration(i)*time.Minute)
		latency, tokens := float64(i%7)*13, int64(i*11)
		f := Fact{Schema: 1, EventID: fmt.Sprintf("day-hierarchy-%d", i), Kind: "attempt", AtMS: at.UnixMilli(), SourceID: "primary", Provider: fmt.Sprint(i % 2), Model: fmt.Sprint(i % 3), Outcome: "success", InputTokens: &tokens, FirstOutputMS: &latency, DispatchMS: &latency, FirstTextMS: &latency, ResponseCreatedMS: &latency}
		if i%4 == 0 {
			f.FirstOutputMS, f.FirstTextMS = nil, nil
		}
		facts = append(facts, f)
	}
	if err := e.Import(ctx, "day-hierarchy", "one", facts); err != nil {
		t.Fatal(err)
	}
	if err := e.Import(ctx, "day-hierarchy-retry", "two", facts); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var raw string
		if err := e.DB.QueryRow("SELECT json_agg(r ORDER BY period_ms,kind,provider,model)::text FROM rollups r WHERE level='day'").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot()
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Removing minute detail must not change a day rebuilt from the exact
	// hourly projections. The previous FIFO path scanned these rows instead.
	if _, err = tx.ExecContext(ctx, "DELETE FROM rollups WHERE level='minute'"); err != nil {
		t.Fatal(err)
	}
	if err = e.rebuildRollup(ctx, tx, "day", day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); before != after {
		t.Fatalf("hourly day rebuild differs from transactional daily projection:\n%s\n%s", before, after)
	}
}

func TestPostgresLegacyPriceSetSurvivesReplicaStartupExactly(t *testing.T) {
	e := stateTestEngine(t)
	if err := e.migrateLegacyPrices(context.Background(), []Price{{Model: "saved-only", Input: 7}}, false); err != nil {
		t.Fatal(err)
	}
	other := replicaTestEngine(t, e)
	var n int
	if err := other.DB.QueryRow("SELECT count(*) FROM prices").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
func TestPostgresLegacyBaselineDoesNotOverwriteExistingOperatorEdits(t *testing.T) {
	e := stateTestEngine(t)
	if err := e.SavePrice(context.Background(), Price{Model: "operator", Input: 11}); err != nil {
		t.Fatal(err)
	}
	if err := e.migrateLegacyPrices(context.Background(), []Price{{Model: "other", Input: 7}}, false); err == nil {
		t.Fatal("existing operator edits overwritten")
	}
	var v float64
	if err := e.DB.QueryRow("SELECT input FROM prices WHERE model='operator'").Scan(&v); err != nil || v != 11 {
		t.Fatal(v, err)
	}
}

func TestPostgresUsageIndexBackfillPreservesFactsAndCoversNewPartitions(t *testing.T) {
	e := stateTestEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Hour)
	tokens := int64(500000)
	for i, at := range []time.Time{now.Add(-3 * time.Hour), now.Add(-time.Hour)} {
		f := Fact{Schema: 1, EventID: fmt.Sprint(i), Kind: "request", AtMS: at.UnixMilli(), Provider: strings.Repeat("p", 512), Model: strings.Repeat("m", 512), UpstreamModel: strings.Repeat("u", 512), Endpoint: strings.Repeat("e", 512), InputTokens: &tokens}
		if err := e.Import(ctx, f.EventID, "hash", []Fact{f}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.DB.Exec("DROP INDEX facts_long_usage; DELETE FROM schema_versions WHERE version='0002'"); err != nil {
		t.Fatal(err)
	}
	if e.readIndexesReady(ctx) == nil {
		t.Fatal("missing index accepted")
	}
	if err := e.prepareReadIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	f := Fact{Schema: 1, EventID: "later", Kind: "request", AtMS: now.Add(-2 * time.Hour).UnixMilli(), InputTokens: &tokens}
	if err := e.Import(ctx, "later", "hash", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	if err := e.readIndexesReady(ctx); err != nil {
		t.Fatal(err)
	}
	var count, indexed int
	if err := e.DB.QueryRow("SELECT count(*) FROM facts").Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	if err := e.DB.QueryRow("SELECT count(*) FROM pg_inherits WHERE inhparent='facts_long_usage'::regclass").Scan(&indexed); err != nil || indexed != 3 {
		t.Fatal(indexed, err)
	}
}

func TestPostgresRollupLayoutMigrationPreservesEveryColumn(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour).UnixMilli()
	name := fmt.Sprintf("rollups_%d", at)
	if _, err := e.DB.Exec(fmt.Sprintf("CREATE TABLE %s PARTITION OF rollups FOR VALUES FROM (%d) TO (%d)", name, at, at+3600000)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("INSERT INTO rollup_partitions(start_ms,end_ms,name) VALUES(?,?,?)", at, at+3600000, name); err != nil {
		t.Fatal(err)
	}
	tokens := int64(400000)
	latency := 12.5
	f := Fact{Schema: 1, EventID: "old", Kind: "request", AtMS: at + 1000, Provider: "p", Model: "gpt-6-sol", InputTokens: &tokens, FirstTextMS: &latency}
	if err := e.Import(ctx, "old", "hash", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var s string
		if err := e.DB.QueryRow("SELECT json_agg(r ORDER BY period_ms,level,kind)::text FROM rollups r").Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	if e.rollupLayoutReady(ctx) == nil {
		t.Fatal("flat layout accepted")
	}
	if err := e.separateRollupLevels(ctx); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); after != before {
		t.Fatal("rollup content changed")
	}
	if err := e.rollupLayoutReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.separateRollupLevels(ctx); err != nil {
		t.Fatal(err)
	}
	f.EventID = "new"
	f.AtMS = at + 3600000
	if err := e.Import(ctx, "new", "hash", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	if err := e.rollupLayoutReady(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryLimitedAmountIsNotAdvertisedToOlderClients(t *testing.T) {
	for _, status := range []string{"history_limited", "complete"} {
		b, err := json.Marshal(attributedSpend{Status: status, MatchedAmount: 0, Matched: 1})
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err = json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		_, present := got["matched_cost_usd"]
		if present != (status == "complete") {
			t.Fatal(status, got)
		}
	}
}

func TestExpiredBillingArrivalPreservesOwnershipWithoutResurrectingFacts(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	floor := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := e.DB.Exec("UPDATE storage_state SET evicted_before_ms=?", floor); err != nil {
		t.Fatal(err)
	}
	f := Fact{Schema: 1, EventID: "late", Kind: "billing", AtMS: floor - 1000, UpstreamBase: "https://example.test/v1/alpha/search", UpstreamKeyHash: "key-hash", BillingRequestIDs: []string{"client:receipt"}}
	for _, key := range []string{"delivery", "retry"} {
		if err := e.Import(ctx, key, "v1", []Fact{f}); err != nil {
			t.Fatal(err)
		}
	}
	var raw, claims int
	if err := e.DB.QueryRow("SELECT count(*) FROM facts").Scan(&raw); err != nil || raw != 0 {
		t.Fatal(raw, err)
	}
	if err := e.DB.QueryRow("SELECT claims FROM receipt_claim_history WHERE digest=?", tokenHash("https://example.test\nkey-hash\nclient:receipt")).Scan(&claims); err != nil || claims != 1 {
		t.Fatal(claims, err)
	}
}

package main

import (
	"context"
	"path/filepath"
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

func TestPostgresFIFOReclaimsFilesPreservesPricesAndRejectsExpiredReplay(t *testing.T) {
	e := stateTestEngine(t)
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

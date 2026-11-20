package main

import (
	"context"
	"database/sql"
	"math"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestPriceTierRollupsMatchRawAndAcceptLateUsage(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	input, output, read, write, hourWrite := int64(100001), int64(15), int64(65000), int64(12000), int64(1000)
	makeFact := func(id string, at time.Time) Fact {
		return Fact{Schema: 1, EventID: id, Kind: "request", SourceID: "site", Provider: "p", Model: "alias", UpstreamModel: "claude-haiku-5-5", KeyID: "caller", Endpoint: "/v1/messages", Stream: true, Outcome: "success", AtMS: at.UnixMilli(), InputTokens: &input, OutputTokens: &output, CacheReadTokens: &read, CacheWriteTokens: &write, CacheWrite1hTokens: &hourWrite}
	}
	filter := QueryFilter{Range: "1h", To: hour.Add(80 * time.Minute).Unix(), SourceID: "site", KeyID: "caller", Model: "alias", Stream: "true"}
	// An hour-wide rolling query has partial edges; check both that path and an
	// aligned complete hour while the same facts change status from raw to seeded.
	full := filter
	full.Range = "24h"
	importFacts := func(batch string, facts ...Fact) {
		t.Helper()
		if err := e.Import(ctx, batch, "v1", facts); err != nil {
			t.Fatal(err)
		}
	}
	covered := makeFact("covered", hour.Add(25*time.Minute))
	covered.Provider, covered.Endpoint, covered.Stream, covered.Outcome = "", "", false, ""
	importFacts("initial", makeFact("before-hour", hour.Add(-time.Minute)), covered, makeFact("edge", hour.Add(65*time.Minute)))
	cost := func(f QueryFilter, raw bool) float64 {
		t.Helper()
		ready := e.priceTiersReady
		e.priceTiersReady = !raw && ready
		defer func() { e.priceTiersReady = ready }()
		r, err := e.Query(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := r.Total["estimated_cost_usd"].(float64)
		if !ok || got <= 0 {
			t.Fatalf("missing expected price: %v", r.Total)
		}
		return got
	}
	if !e.priceTiersReady {
		t.Fatal("optional projection not installed")
	}
	for i := 0; i < 3; i++ {
		if seeded, err := e.seedPriceTierHour(ctx); err != nil || !seeded {
			t.Fatalf("seeded=%v err=%v", seeded, err)
		}
	}
	var summary int64
	if err := e.DB.QueryRow(`SELECT sum(ordinary_tokens) FROM price_tier_rollups WHERE period_ms=? AND threshold=100000`, hour.UnixMilli()).Scan(&summary); err != nil || summary != input-read-write {
		t.Fatalf("initial summary=%d err=%v", summary, err)
	}
	late := makeFact("late", hour.Add(30*time.Minute))
	boundary := makeFact("not-premium", hour.Add(35*time.Minute))
	exact := int64(100000)
	boundary.InputTokens = &exact
	importFacts("late", late, boundary)
	if err := e.DB.QueryRow(`SELECT sum(ordinary_tokens) FROM price_tier_rollups WHERE period_ms=? AND threshold=100000`, hour.UnixMilli()).Scan(&summary); err != nil || summary != 2*(input-read-write) {
		t.Fatalf("updated summary=%d err=%v", summary, err)
	}
	for _, f := range []QueryFilter{filter, full} {
		if a, b := cost(f, false), cost(f, true); math.Abs(a-b) > 1e-12 {
			t.Fatalf("%+v: incremental=%v raw=%v", f, a, b)
		}
	}
}

func TestSpendSlicesMatchExactAndInvalidateAcrossCompletionWindow(t *testing.T) {
	s, account, owner := attributionFixture(t)
	ctx := context.Background()
	hour := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
	from, to := hour.Unix(), hour.Add(2*time.Hour).Unix()
	f := QueryFilter{KeyID: "caller", SourceID: "source"}
	a := attributedFact("old-receipt", "caller", hour.Add(6*time.Minute))
	b := attributedFact("new-receipt", "caller", hour.Add(70*time.Minute))
	storeAttributedLog(t, s, account, a.EventID, "0.1", 1)
	storeAttributedLog(t, s, account, b.EventID, "0.2", 2)
	if err := s.engine.Import(ctx, "both", "v1", []Fact{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := s.queueAccountSpendWindow(ctx, account, hour.Add(-time.Minute).UnixMilli(), to*1000); err != nil {
		t.Fatal(err)
	}
	bindings, err := s.receiptBindings(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	scope, binding := spendSliceScope(owner, f), spendBindingDigest(bindings)
	job := spendSliceJob{spendSliceWindow{from, from + spendSliceHour}, owner, scope, binding, f, false}
	if err := s.warmSpendSlice(ctx, job); err != nil {
		t.Fatal(err)
	}
	s.spendSliceJobs = make(chan spendSliceJob, 4)
	s.spendSliceNext, s.spendSliceVerified = map[string]time.Time{}, map[string]bool{}
	rows, ok := s.composedChannelSpend(ctx, owner, f, from, to, true)
	if !ok || len(rows) != 1 || rows[0].Amount == nil || *rows[0].Amount != .3 {
		t.Fatalf("not composed: %+v ok=%v", rows, ok)
	}
	job.spendSliceWindow.to = to
	if err := s.verifySpendSlices(ctx, job); err != nil || !s.spendSliceVerified[scope+binding] {
		t.Fatalf("shadow read did not match: %v", err)
	}
	completion := a
	completion.Kind, completion.EventID, completion.AtMS, completion.BillingRequestIDs = "attempt", "late-completion", hour.Add(72*time.Minute).UnixMilli(), nil
	if err := s.engine.Import(ctx, "completion", "v1", []Fact{completion}); err != nil {
		t.Fatal(err)
	}
	if _, slices, err := s.spendSliceSnapshot(ctx, scope, binding, from, to); err != nil || len(slices) != 0 {
		t.Fatalf("stale first hour still present: %v %v", slices, err)
	}
	first, err := s.readAttributedChannelSpend(ctx, owner, f, from, from+spendSliceHour, nil)
	if err != nil || len(first) != 0 {
		t.Fatalf("completion left the same receipt in its old hour: %+v %v", first, err)
	}
	second, err := s.readAttributedChannelSpend(ctx, owner, f, from+spendSliceHour, to, nil)
	if err != nil || len(second) != 1 || second[0].Amount == nil || *second[0].Amount != .3 {
		t.Fatalf("completion not included in new hour: %+v %v", second, err)
	}
}

func TestSpendSlicesReceiptCorrectionAndCompetingClaim(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		t.Fatal("isolated PostgreSQL URL required")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := "analytics_spend_" + tokenHash(t.TempDir())[:16]
	if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE " + name + " WITH (FORCE)") })
	u.Path = "/" + name
	t.Setenv("TEST_CONTROL_DATABASE_URL", u.String())
	s, account, owner := attributionFixture(t)
	e, err := OpenEngine("analytics", Config{ControlDatabaseURL: u.String(), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	s.engine = e
	ctx := context.Background()
	hour := time.Now().UTC().Truncate(time.Hour).Add(-4 * time.Hour)
	from, to := hour.Unix(), hour.Add(2*time.Hour).Unix()
	f := QueryFilter{KeyID: "caller", SourceID: "source"}
	b := attributedFact("settled", "caller", hour.Add(6*time.Minute))
	storeAttributedLog(t, s, account, b.EventID, "0.3", 1)
	if err = e.Import(ctx, "settled", "v1", []Fact{b}); err != nil {
		t.Fatal(err)
	}
	if err = s.queueAccountSpendWindow(ctx, account, from*1000, to*1000); err != nil {
		t.Fatal(err)
	}
	bindings, err := s.receiptBindings(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	scope, binding := spendSliceScope(owner, f), spendBindingDigest(bindings)
	job := spendSliceJob{spendSliceWindow{from, from + spendSliceHour}, owner, scope, binding, f, false}
	assertSlices := func(want int) {
		t.Helper()
		if _, stored, err := s.spendSliceSnapshot(ctx, scope, binding, from, to); err != nil || len(stored) != want {
			t.Fatalf("stored=%d, want=%d, err=%v", len(stored), want, err)
		}
	}
	assertAmount := func(want float64) {
		t.Helper()
		r, ok := s.composedChannelSpend(ctx, owner, f, from, to, true)
		if !ok || len(r) != 1 || r[0].Amount == nil || *r[0].Amount != want {
			t.Fatalf("amount want=%v: %+v ok=%v", want, r, ok)
		}
	}
	if err = s.warmSpendSlice(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertSlices(1)
	assertAmount(.3)
	if _, err = s.control.db.Exec(`UPDATE console_sub_accounts SET rate_coefficient=2 WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	assertSlices(1)
	assertAmount(.6)
	if _, err = s.control.db.Exec(`UPDATE console_sub_spend_logs SET actual_cost=.4 WHERE account_id=$1 AND log_id=1`, account); err != nil {
		t.Fatal(err)
	}
	assertSlices(0)
	if err = s.warmSpendSlice(ctx, job); err != nil {
		t.Fatal(err)
	}
	assertSlices(1)
	assertAmount(.8)
	competitor := attributedFact("late-claim", "other-key", hour.Add(3*time.Hour))
	competitor.SourceID, competitor.BillingRequestIDs = "another-source", b.BillingRequestIDs
	if err = e.Import(ctx, "competitor", "v1", []Fact{competitor}); err != nil {
		t.Fatal(err)
	}
	assertSlices(0)
	r, err := s.readAttributedChannelSpend(ctx, owner, f, from, to, nil)
	if err != nil || len(r) != 1 || r[0].Amount != nil || r[0].Ambiguous != 1 {
		t.Fatalf("late conflict was not detected: %+v %v", r, err)
	}
}

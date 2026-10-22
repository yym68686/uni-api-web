package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func assertClaimProjectionEqual(t *testing.T, e *Engine) {
	t.Helper()
	var different bool
	source := claimProjectionSelect("facts")
	target := "SELECT " + claimColumns + " FROM billing_claims"
	if err := e.DB.QueryRow("SELECT EXISTS((" + source + " EXCEPT ALL " + target + ") UNION ALL (" + target + " EXCEPT ALL " + source + "))").Scan(&different); err != nil || different {
		t.Fatal("receipt projection differs from immutable facts", different, err)
	}
}

func TestClaimProjectionAtomicPublicationAndPreparation(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	hour := time.Now().Add(-time.Hour).Truncate(time.Hour)
	facts := make([]Fact, 1100)
	for i := range facts {
		facts[i] = attributedFact(fmt.Sprintf("receipt-%04d", i), "caller", hour.Add(time.Duration(i)*time.Millisecond))
		facts[i].BillingRequestIDs = []string{"client:duplicate", "client:duplicate", fmt.Sprint(i)}
	}
	if err := e.Import(ctx, "initial", "one", facts); err != nil {
		t.Fatal(err)
	}
	if err := e.Import(ctx, "retry", "one", facts); err != nil {
		t.Fatal(err)
	}
	assertClaimProjectionEqual(t, e)
	var count int
	if err := e.DB.QueryRow("SELECT count(*) FROM billing_claims").Scan(&count); err != nil || count != 3300 {
		t.Fatal("duplicate batch multiplied claims", count, err)
	}
	// A failed fact transaction cannot publish an ownership claim separately.
	bad := facts[0]
	bad.EventID, bad.BillingRequestIDs = "aborted", []string{"client:aborted"}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.insertFactPage(ctx, tx, []Fact{bad}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	tx.Rollback()
	if err = e.DB.QueryRow("SELECT count(*) FROM billing_claims WHERE event_id='aborted'").Scan(&count); err != nil || count != 0 {
		t.Fatal("rolled back ownership became visible", count, err)
	}
	// Simulate preparation of an existing database with live new writers.
	if _, err = e.DB.Exec("DELETE FROM billing_claims;UPDATE meta SET value='false' WHERE name='claim_projection_complete'"); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- e.backfillClaimProjection(ctx) }()
	fresh := attributedFact("fresh-hour", "caller", time.Now().Add(-time.Minute))
	if err = e.Import(ctx, "live", "one", []Fact{fresh}); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	assertClaimProjectionEqual(t, e)
	var complete bool
	if err = e.DB.QueryRow("SELECT value='true' FROM meta WHERE name='claim_projection_complete'").Scan(&complete); err != nil || !complete {
		t.Fatal("verified projection not published", complete, err)
	}
}

func TestProjectedClaimsPreserveGlobalCompetitorsAndCheckFullTuple(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().Add(-time.Minute)
	a := attributedFact("selected", "caller", now)
	a.BillingRequestIDs = []string{"client:shared", "client:shared", "client:unique"}
	b := a
	b.EventID, b.SourceID, b.KeyID, b.Provider = "foreign", "hidden", "other", "other-channel"
	b.AtMS = now.Add(-48 * time.Hour).UnixMilli()
	b.BillingRequestIDs = []string{"client:shared"}
	b.UpstreamBase += "/v1/alpha/search/"
	c := b
	c.EventID, c.UpstreamKeyHash = "different-credential", "different"
	if err := e.Import(ctx, "owners", "one", []Fact{a, b, c}); err != nil {
		t.Fatal(err)
	}
	// Even an artificial digest collision must not change exact tuple matching.
	if _, err := e.DB.Exec("UPDATE billing_claims SET owner_digest=(SELECT owner_digest FROM billing_claims WHERE event_id='selected' AND ordinal=1) WHERE event_id='different-credential'"); err != nil {
		t.Fatal(err)
	}
	for _, narrow := range []bool{false, true} {
		t.Run(fmt.Sprint(narrow), func(t *testing.T) {
			tx, err := e.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.Exec("CREATE TEMP TABLE console_spend_facts AS SELECT * FROM facts WHERE event_id='selected'"); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(billingClaimsSQL + ";ALTER TABLE console_spend_claims RENAME TO expected"); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(projectedBillingClaimsSQL(narrow)); err != nil {
				t.Fatal(err)
			}
			var different bool
			if err = tx.QueryRow(`SELECT EXISTS((SELECT * FROM expected EXCEPT ALL SELECT * FROM console_spend_claims) UNION ALL (SELECT * FROM console_spend_claims EXCEPT ALL SELECT * FROM expected))`).Scan(&different); err != nil || different {
				t.Fatal("projected ownership changed global distinct-event counts", different, err)
			}
			var shared, unique int
			if err = tx.QueryRow("SELECT max(n) FILTER(WHERE rid='client:shared'),max(n) FILTER(WHERE rid='client:unique') FROM console_spend_claims").Scan(&shared, &unique); err != nil || shared != 2 || unique != 1 {
				t.Fatal("duplicate headers/hidden competitor miscounted", shared, unique, err)
			}
		})
	}
}

func TestClaimPreparationChecksEveryColumnAndBothIdentitySets(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	hour := time.Now().Add(-time.Hour).Truncate(time.Hour)
	facts := make([]Fact, 1100)
	for i := range facts {
		facts[i] = attributedFact(fmt.Sprintf("verify-%04d", i), "caller", hour.Add(time.Duration(i)*time.Millisecond))
	}
	if err := e.Import(ctx, "fixture", "one", facts); err != nil {
		t.Fatal(err)
	}
	verify := func() error { return e.walkClaimProjectionHour(ctx, hour.UnixMilli(), true) }
	if err := verify(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("UPDATE billing_claims SET rid='altered' WHERE event_id='verify-1000'"); err != nil {
		t.Fatal(err)
	}
	if err := verify(); !errors.Is(err, errClaimProjectionMismatch) {
		t.Fatal("later-page mismatch missed", err)
	}
	if _, err := e.DB.Exec("DELETE FROM billing_claims WHERE event_id='verify-1000'"); err != nil {
		t.Fatal(err)
	}
	if err := verify(); !errors.Is(err, errClaimProjectionMismatch) {
		t.Fatal("missing row missed", err)
	}
	if err := e.walkClaimProjectionHour(ctx, hour.UnixMilli(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("UPDATE billing_claims SET event_id='zz-extra' WHERE event_id='verify-1000';DELETE FROM facts WHERE event_id='verify-1000'"); err != nil {
		t.Fatal(err)
	}
	if err := verify(); !errors.Is(err, errClaimProjectionMismatch) {
		t.Fatal("extra identity outside source keyset missed", err)
	}
}

func TestClaimPreparationDefersWithoutHeadroomAndRemovesOnlyOrphanProjection(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	hour := time.Now().Add(-time.Hour).Truncate(time.Hour).UnixMilli()
	if err := e.Import(ctx, "fixture", "one", []Fact{attributedFact("one", "caller", time.UnixMilli(hour+1))}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("DELETE FROM billing_claims;UPDATE meta SET value='false' WHERE name='claim_projection_complete';UPDATE storage_state SET ingest_limit_bytes=1 WHERE singleton"); err != nil {
		t.Fatal(err)
	}
	if err := e.backfillClaimProjection(ctx); !errors.Is(err, errBillingBackfillHeadroom) {
		t.Fatal("preparation exceeded storage reserve", err)
	}
	var complete bool
	if err := e.DB.QueryRow("SELECT value='true' FROM meta WHERE name='claim_projection_complete'").Scan(&complete); err != nil || complete {
		t.Fatal("incomplete projection published", complete, err)
	}
	if _, err := e.DB.Exec("UPDATE storage_state SET ingest_limit_bytes=7500000000 WHERE singleton"); err != nil {
		t.Fatal(err)
	}
	if err := e.walkClaimProjectionHour(ctx, hour, false); err != nil {
		t.Fatal(err)
	}
	// Emulate an older writer evicting a raw hour during the deployment overlap.
	if _, err := e.DB.Exec(fmt.Sprintf("DROP TABLE facts_%d;DELETE FROM fact_partitions WHERE start_ms=%d", hour, hour)); err != nil {
		t.Fatal(err)
	}
	if err := e.backfillClaimProjection(ctx); err != nil {
		t.Fatal(err)
	}
	var present bool
	if err := e.DB.QueryRow("SELECT to_regclass(?) IS NOT NULL", fmt.Sprintf("claims_%d", hour)).Scan(&present); err != nil || present {
		t.Fatal("expired projection was retained/resurrected", present, err)
	}
}

func TestClaimProjectionFallbackAndActivationPreserveAttribution(t *testing.T) {
	s, account, owner := attributionFixture(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	visible := attributedFact("visible", "caller", now.Add(-time.Minute))
	hidden := visible
	hidden.EventID, hidden.SourceID, hidden.Provider = "competitor", "hidden", "hidden-provider"
	paid := attributedFact("paid", "caller", now.Add(-time.Minute))
	if err := s.engine.Import(ctx, "fixture", "one", []Fact{visible, hidden, paid}); err != nil {
		t.Fatal(err)
	}
	storeAttributedLog(t, s, account, "visible", "9.9", 1)
	storeAttributedLog(t, s, account, "paid", "0.25", 2)
	// Missing historical ownership must continue suppressing Amount and the
	// wire lower bound, regardless of projection activation.
	if _, err := s.engine.DB.Exec("DELETE FROM billing_claims;UPDATE meta SET value='false' WHERE name='claim_projection_complete';UPDATE storage_state SET claims_history_complete=false WHERE singleton"); err != nil {
		t.Fatal(err)
	}
	f := QueryFilter{SourceID: "source", KeyID: "caller", Provider: "shared"}
	before, err := s.attributedChannelSpend(ctx, owner, f, now.Add(-time.Hour).Unix(), now.Unix())
	if err != nil || len(before) != 1 || before[0].Ambiguous != 1 || before[0].Matched != 1 || before[0].Amount != nil || before[0].Status != "history_limited" {
		t.Fatal("incomplete projection did not use the exact raw fallback", before, err)
	}
	if err = s.engine.backfillClaimProjection(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.attributedChannelSpend(ctx, owner, f, now.Add(-time.Hour).Unix(), now.Unix())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("activation changed financial evidence", before, after, err)
	}
}

func TestClaimTriggerSupportsOldWritersAndOldFIFODuringRollback(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	current := time.Now().Truncate(time.Hour).UnixMilli()
	old := current - 2*3600000
	// The old binary creates only raw partitions and inserts into facts. It
	// knows nothing about the derived table, including future-hour partitions.
	for _, hour := range []int64{old, current} {
		tx, err := e.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = ensureFactPartition(ctx, tx, hour+1); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO facts(event_id,kind,source_id,at_ms,upstream_base,upstream_key_hash,billing_request_ids) VALUES(?,'billing','old-writer',?,'https://site.example/v1/alpha/search/','credential',ARRAY['client:shared','client:shared'])`, fmt.Sprint(hour), hour+1)
		}
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	assertClaimProjectionEqual(t, e)
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.retainEvictedReceiptClaims(ctx, tx, fmt.Sprintf("facts_%d", old)); err == nil {
		_, err = tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE facts_%d;DELETE FROM fact_partitions WHERE start_ms=%d;UPDATE storage_state SET evicted_before_ms=%d WHERE singleton", old, old, old+3600000))
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = e.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("CREATE TEMP TABLE console_spend_facts AS SELECT * FROM facts"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(projectedBillingClaimsSQL(true)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = tx.QueryRow("SELECT n FROM console_spend_claims").Scan(&n); err != nil || n != 1 {
		t.Fatal("old FIFO orphan was counted as retained ownership", n, err)
	}
	owners := map[string]int{receiptOwnerKey("https://site.example", "credential", "client:shared"): n}
	if err = e.addRetiredClaims(ctx, tx, owners); err != nil || owners[receiptOwnerKey("https://site.example", "credential", "client:shared")] != 2 {
		t.Fatal("retired competitor lost or counted twice", owners, err)
	}
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock(8395015)"); err != nil {
		t.Fatal(err)
	}
	if reclaimed, err := reclaimOrphanClaimPartition(ctx, tx); err != nil || !reclaimed {
		t.Fatal("orphan not reclaimed", reclaimed, err)
	}
	var present bool
	if err = tx.QueryRow("SELECT to_regclass(?) IS NOT NULL", fmt.Sprintf("claims_%d", current)).Scan(&present); err != nil || !present {
		t.Fatal("retained projection removed", present, err)
	}
}

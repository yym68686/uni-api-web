package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func assertBillingProjectionEqual(t *testing.T, e *Engine) {
	t.Helper()
	var different bool
	source := "SELECT " + billingColumns + " FROM facts WHERE " + billingPredicate
	target := "SELECT " + billingColumns + " FROM billing_facts"
	if err := e.DB.QueryRow("SELECT EXISTS((" + source + " EXCEPT ALL " + target + ") UNION ALL (" + target + " EXCEPT ALL " + source + "))").Scan(&different); err != nil || different {
		t.Fatal("billing projection differs from immutable facts", different, err)
	}
}

func TestBillingProjectionPublishesAtomicallyAndBackfillsWithNewWriters(t *testing.T) {
	e := postgresTestEngine(t)
	ctx := context.Background()
	now := time.Now().Add(-time.Hour).Truncate(time.Hour)
	var facts []Fact
	for i := 0; i < 1100; i++ {
		b := attributedFact(fmt.Sprintf("project-%04d", i), "caller", now.Add(time.Duration(i)*time.Millisecond))
		if i%2 == 0 {
			b.Kind = "attempt"
		}
		facts = append(facts, b)
	}
	if err := e.Import(ctx, "projection", "one", facts); err != nil {
		t.Fatal(err)
	}
	if err := e.Import(ctx, "projection-retry", "two", facts); err != nil {
		t.Fatal(err)
	}
	assertBillingProjectionEqual(t, e)
	// Reproduce upgrading a populated pre-projection database. New code remains
	// on its legacy read path until the explicit backfill publishes readiness.
	if _, err := e.DB.Exec("DELETE FROM billing_facts;UPDATE meta SET value='false' WHERE name='billing_projection_complete'"); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- e.backfillBillingProjection(ctx) }()
	fresh := attributedFact("new-hour", "caller", time.Now().Add(-time.Minute))
	if err := e.Import(ctx, "live-writer", "one", []Fact{fresh}); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	assertBillingProjectionEqual(t, e)
	var ready bool
	if err := e.DB.QueryRow("SELECT value='true' FROM meta WHERE name='billing_projection_complete'").Scan(&ready); err != nil || !ready {
		t.Fatal(ready, err)
	}
}

func TestBillingBackfillPreservesIncompleteMarkerWhenStorageHasNoReserve(t *testing.T) {
	e := postgresTestEngine(t)
	ctx := context.Background()
	if err := e.Import(ctx, "fixture", "one", []Fact{attributedFact("reserve", "caller", time.Now().Add(-time.Minute))}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("DELETE FROM billing_facts;UPDATE meta SET value='false' WHERE name='billing_projection_complete';UPDATE storage_state SET ingest_limit_bytes=1 WHERE singleton"); err != nil {
		t.Fatal(err)
	}
	if err := e.backfillBillingProjection(ctx); err == nil {
		t.Fatal("backfill ignored reserve")
	}
	var n int
	var complete bool
	if err := e.DB.QueryRow("SELECT count(*) FROM billing_facts").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := e.DB.QueryRow("SELECT value='true' FROM meta WHERE name='billing_projection_complete'").Scan(&complete); err != nil || complete {
		t.Fatal(complete, err)
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestBillingProviderIndexIsPreparedSafelyAndInheritedByNewPartitions(t *testing.T) {
	e := postgresTestEngine(t)
	ctx := context.Background()
	started := time.Now().UTC().Truncate(time.Hour)
	facts := make([]Fact, 1200)
	for i := range facts {
		fact := attributedFact(fmt.Sprintf("provider-index-%04d", i), "caller", started.Add(time.Duration(i)*time.Millisecond))
		fact.Provider = fmt.Sprintf("provider-%02d", i%12)
		facts[i] = fact
	}
	if err := e.Import(ctx, "provider-index-fixture", "one", facts); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := e.DB.QueryRow("SELECT count(*) FROM billing_facts").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := e.prepareBillingProviderIndex(ctx); err != nil {
		t.Fatal(err)
	}
	var parentReady bool
	if err := e.DB.QueryRow("SELECT indisvalid FROM pg_index WHERE indexrelid='billing_facts_provider_scope'::regclass").Scan(&parentReady); err != nil || !parentReady {
		t.Fatal("partitioned billing index is not ready", parentReady, err)
	}
	var children, indexed int
	if err := e.DB.QueryRow(`SELECT count(*),count(*) FILTER(WHERE i.indisvalid)
FROM pg_inherits h JOIN pg_class c ON c.oid=h.inhrelid
LEFT JOIN pg_index i ON i.indrelid=c.oid AND i.indexrelid=to_regclass(c.relname || '_provider_scope')
WHERE h.inhparent='billing_facts'::regclass`).Scan(&children, &indexed); err != nil || children == 0 || indexed != children {
		t.Fatal("existing billing partitions are not all indexed", children, indexed, err)
	}
	// New hourly partitions must inherit the attached parent index automatically.
	futureStart := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Hour).UnixMilli()
	futureName := fmt.Sprintf("billing_%d", futureStart)
	if _, err := e.DB.Exec(fmt.Sprintf("CREATE TABLE %s PARTITION OF billing_facts FOR VALUES FROM (%d) TO (%d)", futureName, futureStart, futureStart+3600000)); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_inherits h
JOIN pg_index i ON i.indexrelid=h.inhrelid
WHERE h.inhparent='billing_facts_provider_scope'::regclass
AND i.indrelid=to_regclass(?) AND i.indisvalid)`, futureName).Scan(&parentReady); err != nil || !parentReady {
		t.Fatal("new billing partition did not inherit its provider index", parentReady, err)
	}
	var after int
	if err := e.DB.QueryRow("SELECT count(*) FROM billing_facts").Scan(&after); err != nil || after != before {
		t.Fatal("index preparation changed billing facts", before, after, err)
	}
	// An already valid index needs no additional storage, even if live facts
	// subsequently consume the maintenance reserve.
	if _, err := e.DB.Exec("UPDATE storage_state SET ingest_limit_bytes=1 WHERE singleton"); err != nil {
		t.Fatal(err)
	}
	if err := e.prepareBillingProviderIndex(ctx); err != nil {
		t.Fatal("valid provider index preparation is not idempotent", err)
	}
}

func TestBillingProviderIndexDefersWhenStorageReserveIsInsufficient(t *testing.T) {
	e := postgresTestEngine(t)
	ctx := context.Background()
	if err := e.Import(ctx, "provider-index-headroom", "one", []Fact{attributedFact("provider-index-headroom", "caller", time.Now().Add(-time.Minute))}); err != nil {
		t.Fatal(err)
	}
	var databaseBytes, heapBytes int64
	if err := e.DB.QueryRow(`SELECT pg_database_size(current_database()),
COALESCE((SELECT sum(pg_relation_size(h.inhrelid)) FROM pg_inherits h WHERE h.inhparent='billing_facts'::regclass),0)`).Scan(&databaseBytes, &heapBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("UPDATE storage_state SET ingest_limit_bytes=? WHERE singleton", databaseBytes+heapBytes+(64<<20)); err != nil {
		t.Fatal(err)
	}
	if err := e.prepareBillingProviderIndex(ctx); !errors.Is(err, errBillingProviderIndexHeadroom) {
		t.Fatal("provider index did not defer at its reserve boundary", err)
	}
	var exists bool
	if err := e.DB.QueryRow("SELECT to_regclass('billing_facts_provider_scope') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatal("headroom refusal left a partial index", exists, err)
	}
	var rows int
	if err := e.DB.QueryRow("SELECT count(*) FROM billing_facts").Scan(&rows); err != nil || rows != 1 {
		t.Fatal("headroom refusal changed billing facts", rows, err)
	}
}

func TestBillingProviderIndexRequiresCompletedProjection(t *testing.T) {
	e := postgresTestEngine(t)
	if _, err := e.DB.Exec("UPDATE meta SET value='false' WHERE name='billing_projection_complete'"); err != nil {
		t.Fatal(err)
	}
	if err := e.prepareBillingProviderIndex(context.Background()); err == nil {
		t.Fatal("provider index preparation ignored an incomplete billing projection")
	}
	var exists bool
	if err := e.DB.QueryRow("SELECT to_regclass('billing_facts_provider_scope') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatal("incomplete projection left a partial index", exists, err)
	}
}

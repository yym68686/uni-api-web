package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestImportIsIdempotentAndRejectsChangedImmutableObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.duckdb")
	e, err := OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	at := time.Now().UTC().Add(-time.Second).UnixMilli()
	in := int64(4)
	out := int64(7)
	f := Fact{Schema: 1, EventID: "evt-1", Kind: "request", InstanceID: "i", RequestID: "req", AtMS: at, Provider: "p", Model: "m", UpstreamModel: "u", Endpoint: "/v1/responses", InputTokens: &in, OutputTokens: &out, Outcome: "success"}
	if err = e.Import(context.Background(), "facts/one.jsonl", "etag-1", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	if err = e.Import(context.Background(), "facts/one.jsonl", "etag-1", []Fact{f}); err != nil {
		t.Fatal("same immutable object should be idempotent", err)
	}
	if err = e.Import(context.Background(), "facts/one.jsonl", "etag-2", []Fact{f}); err == nil {
		t.Fatal("changed immutable object accepted")
	}
	var count int
	if err = e.DB.QueryRow("SELECT count(*) FROM facts").Scan(&count); err != nil || count != 1 {
		t.Fatalf("facts=%d err=%v", count, err)
	}
}
func TestRangeStartUsesLocalCalendarBoundaries(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 9, 15, 1, 2, 3, 0, loc)
	start, err := rangeStart("today", now.UTC(), loc)
	if err != nil {
		t.Fatal(err)
	}
	if start.In(loc).Hour() != 0 || start.In(loc).Day() != 15 {
		t.Fatalf("start=%v", start)
	}
	start, err = rangeStart("month", now.UTC(), loc)
	if err != nil || start.In(loc).Day() != 1 {
		t.Fatal(start, err)
	}
}

func TestQueryImportedFacts(t *testing.T) {
	e, err := OpenEngine(filepath.Join(t.TempDir(), "query.duckdb"), Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	f := Fact{Schema: 1, EventID: "request-1", Kind: "request", AtMS: time.Now().Add(-time.Second).UnixMilli(), Provider: "p", Model: "m", UpstreamModel: "m", Outcome: "success"}
	a := f
	a.EventID = "attempt-1"
	a.Kind = "attempt"
	if err = e.Import(context.Background(), "test.jsonl", "etag", []Fact{f, a}); err != nil {
		t.Fatal(err)
	}
	result, err := e.Query(context.Background(), QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total["requests"] != float64(1) || len(result.Data) != 1 {
		t.Fatalf("unexpected totals %+v", result)
	}
}

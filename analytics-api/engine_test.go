package main

import (
	"context"
	"fmt"
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

func TestOpenEngineSeedsPublishedReferencePricesWithoutOverwritingEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.duckdb")
	e, err := OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	var input, output float64
	var verified bool
	if err = e.DB.QueryRow("SELECT input,output,verified FROM prices WHERE model='gpt-5.4'").Scan(&input, &output, &verified); err != nil {
		t.Fatal(err)
	}
	if input != 2.5 || output != 15 || !verified {
		t.Fatalf("unexpected seeded price: %v %v %v", input, output, verified)
	}
	if err = e.SavePrice(context.Background(), Price{Model: "gpt-5.4", Input: 9, Output: 8, Verified: true}); err != nil {
		t.Fatal(err)
	}
	e.Close()
	e, err = OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err = e.DB.QueryRow("SELECT input,output FROM prices WHERE model='gpt-5.4'").Scan(&input, &output); err != nil {
		t.Fatal(err)
	}
	if input != 9 || output != 8 {
		t.Fatalf("operator price overwritten: %v %v", input, output)
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

func TestQueryTimeseriesUsesPreaggregatedBuckets(t *testing.T) {
	e, err := OpenEngine(filepath.Join(t.TempDir(), "trend.duckdb"), Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	at := time.Now().UTC().Add(-2 * time.Minute).UnixMilli()
	facts := []Fact{
		{Schema: 1, EventID: "trend-success", Kind: "attempt", AtMS: at, Provider: "p", Model: "m", UpstreamModel: "u", Endpoint: "/v1/responses", Outcome: "success"},
		{Schema: 1, EventID: "trend-failed", Kind: "attempt", AtMS: at, Provider: "p", Model: "m", UpstreamModel: "u", Endpoint: "/v1/responses", Outcome: "failed"},
	}
	if err = e.Import(context.Background(), "trend.jsonl", "etag", facts); err != nil {
		t.Fatal(err)
	}
	result, err := e.Query(context.Background(), QueryFilter{Range: "1h", Endpoint: "/v1/responses", Stream: "all", Timeseries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 || len(result.Data[0].Points) != 1 {
		t.Fatalf("unexpected points: %+v", result.Data)
	}
	point := result.Data[0].Points[0]
	if point["success"] != int64(1) || point["failed"] != int64(1) {
		t.Fatalf("unexpected point: %+v", point)
	}
}

func TestQueryRangesRemainBoundedWithHistoricalRollups(t *testing.T) {
	e, err := OpenEngine(filepath.Join(t.TempDir(), "range-performance.duckdb"), Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	now := time.Now().UTC()
	objects := make([]FactObject, 0, 120)
	for day := 0; day < 120; day++ {
		at := now.Add(-time.Duration(day) * 24 * time.Hour).UnixMilli()
		objects = append(objects, FactObject{Key: fmt.Sprintf("range/%03d.jsonl", day), ETag: fmt.Sprintf("etag-%d", day), Facts: []Fact{{Schema: 1, EventID: fmt.Sprintf("range-%d", day), Kind: "attempt", AtMS: at, Provider: "provider", Model: "model", UpstreamModel: "upstream", Endpoint: "/v1/responses", Outcome: "success"}}})
	}
	if err = e.ImportBatch(context.Background(), objects); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"today", "week", "month", "year", "all"} {
		started := time.Now()
		result, queryErr := e.Query(context.Background(), QueryFilter{Range: name, Timeseries: true})
		if queryErr != nil {
			t.Fatalf("range %s: %v", name, queryErr)
		}
		if elapsed := time.Since(started); elapsed >= time.Second {
			t.Fatalf("range %s exceeded 1s: %s (reported %.1fms)", name, elapsed, result.DurationMS)
		}
		if len(result.Data) != 1 || len(result.Data[0].Points) == 0 {
			t.Fatalf("range %s missing bounded points: %+v", name, result.Data)
		}
	}
}

func TestBatchImportAtomicAndDimensionAggregation(t *testing.T) {
	e, err := OpenEngine(filepath.Join(t.TempDir(), "batch.duckdb"), Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx := context.Background()
	at := time.Now().Add(-time.Minute).UnixMilli()
	objects := []FactObject{}
	for i := 0; i < 4; i++ {
		f := Fact{Schema: 1, Kind: "attempt", EventID: fmt.Sprint(i), AtMS: at, Provider: "route", Model: "model", UpstreamModel: "upstream", Endpoint: []string{"/v1/messages", "/v1/responses"}[i%2], Stream: i < 2, Outcome: "success"}
		objects = append(objects, FactObject{Key: fmt.Sprint(i), ETag: "a", Facts: []Fact{f}})
	}
	if err = e.ImportBatch(ctx, objects); err != nil {
		t.Fatal(err)
	}
	if err = e.ImportBatch(ctx, objects); err != nil {
		t.Fatal(err)
	}
	all, err := e.Query(ctx, QueryFilter{Range: "1h", Endpoint: "all", Stream: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Data) != 1 || all.Data[0].Stats["success"] != float64(4) || all.Data[0].Stream != nil || all.Data[0].Endpoint != "all" {
		t.Fatalf("bad aggregate %+v", all.Data)
	}
	filtered, err := e.Query(ctx, QueryFilter{Range: "1h", Endpoint: "/v1/messages", Stream: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Data) != 1 || filtered.Data[0].Stats["success"] != float64(1) {
		t.Fatalf("bad filter %+v", filtered.Data)
	}
	bad := objects[0]
	bad.Key = "new"
	bad.Facts = []Fact{{Schema: 0, EventID: "bad", AtMS: at}}
	fresh := objects[0]
	fresh.Key = "fresh"
	fresh.Facts = []Fact{objects[0].Facts[0]}
	fresh.Facts[0].EventID = "fresh"
	if err = e.ImportBatch(ctx, []FactObject{fresh, bad}); err == nil {
		t.Fatal("invalid batch committed")
	}
	known, err := e.ImportedObjects(ctx)
	if err != nil || len(known) != 4 {
		t.Fatalf("checkpoint changed on failed batch: %v %v", known, err)
	}
}

func BenchmarkObjectImports(b *testing.B) {
	for _, batched := range []bool{false, true} {
		b.Run(fmt.Sprint("batch=", batched), func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				e, err := OpenEngine(filepath.Join(b.TempDir(), "bench.duckdb"), Config{Timezone: "UTC"})
				if err != nil {
					b.Fatal(err)
				}
				objects := []FactObject{}
				for i := 0; i < 64; i++ {
					objects = append(objects, FactObject{Key: fmt.Sprint(i), ETag: "a", Facts: []Fact{{Schema: 1, EventID: fmt.Sprint(i), Kind: "attempt", AtMS: time.Now().Add(-time.Minute).UnixMilli(), Provider: "route", Model: "m", Outcome: "success"}}})
				}
				start := time.Now()
				if batched {
					err = e.ImportBatch(context.Background(), objects)
				} else {
					for _, o := range objects {
						if err = e.Import(context.Background(), o.Key, o.ETag, o.Facts); err != nil {
							break
						}
					}
				}
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(time.Since(start).Milliseconds()), "import_ms")
				e.Close()
			}
		})
	}
}

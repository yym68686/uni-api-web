package main

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestCacheHistoryWeightedBucketsAndFilters(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	at := time.Now().Add(-10*time.Minute).UnixMilli() / 60000 * 60000
	ptr := func(n int64) *int64 { return &n }
	facts := []Fact{}
	add := func(id, source, key, model, upstream, endpoint string, stream bool, input, cached *int64, when int64) {
		facts = append(facts, Fact{Schema: 1, EventID: id, SourceID: source, Kind: "request", AtMS: when, Provider: "same", KeyID: key, Model: model, UpstreamModel: upstream, Endpoint: endpoint, Stream: stream, Outcome: "success", InputTokens: input, CacheReadTokens: cached})
	}
	add("one", "primary", "key", "m", "u", "/v1/responses", true, ptr(100), ptr(100), at)
	add("two", "primary", "key", "m", "u", "/v1/responses", true, ptr(900), ptr(0), at)
	add("unknown", "primary", "key", "m", "u", "/v1/responses", true, ptr(100), nil, at+5*60000)
	// Retry attempts must not duplicate request usage.
	facts = append(facts, Fact{Schema: 1, EventID: "attempt", SourceID: "primary", Kind: "attempt", AtMS: at, Provider: "same", KeyID: "key", Model: "m", UpstreamModel: "u", Endpoint: "/v1/responses", Stream: true, Outcome: "success", InputTokens: ptr(5000), CacheReadTokens: ptr(5000)})
	add("other-source", "secondary", "key", "m", "u", "/v1/responses", true, ptr(5000), ptr(5000), at)
	add("other-key", "primary", "other", "m", "u", "/v1/responses", true, ptr(5000), ptr(5000), at)
	add("other-model", "primary", "key", "other", "u", "/v1/responses", true, ptr(5000), ptr(5000), at)
	add("other-upstream", "primary", "key", "m", "other", "/v1/responses", true, ptr(5000), ptr(5000), at)
	add("other-endpoint", "primary", "key", "m", "u", "/v1/messages", true, ptr(5000), ptr(5000), at)
	add("nonstream", "primary", "key", "m", "u", "/v1/responses", false, ptr(5000), ptr(5000), at)
	if err := e.Import(ctx, "cache-history", "etag", facts); err != nil {
		t.Fatal(err)
	}
	r, err := e.Query(ctx, QueryFilter{Range: "2h", SourceID: "primary", Provider: "same", KeyID: "key", Model: "m", UpstreamModel: "u", Endpoint: "/v1/responses", Stream: "true", Timeseries: true})
	if err != nil || len(r.Data) != 1 || len(r.Data[0].Points) != 2 {
		t.Fatal(r.Data, err)
	}
	points := r.Data[0].Points
	if points[0]["cache_rate"] != .1 || points[0]["input_tokens"] != int64(1000) || points[0]["cache_read_tokens"] != int64(100) || points[1]["cache_rate"] != nil {
		t.Fatal(points)
	}
}

func TestCacheHistoryIncludesPartialDaysWithoutDoubleCounting(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	facts := []Fact{}
	input, cached := int64(100), int64(25)
	for i := 0; i < 90; i++ {
		facts = append(facts, Fact{Schema: 1, EventID: fmt.Sprint(i), SourceID: "primary", Kind: "request", AtMS: time.Now().Add(-time.Duration(i) * time.Hour).UnixMilli(), Provider: "p", Model: "m", Outcome: "success", InputTokens: &input, CacheReadTokens: &cached})
	}
	if err := e.Import(ctx, "cache-partial", "etag", facts); err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"7d", "month", "all"} {
		r, err := e.Query(ctx, QueryFilter{Range: window, Timeseries: true})
		if err != nil {
			t.Fatal(err)
		}
		var total int64
		for _, p := range r.Data[0].Points {
			total += p["input_tokens"].(int64)
			if math.Abs(p["cache_rate"].(float64)-.25) > 1e-10 {
				t.Fatal(p)
			}
		}
		if float64(total) != r.Total["input_tokens"] {
			t.Fatalf("%s lost partial day or doubled rollups: %d vs %v", window, total, r.Total["input_tokens"])
		}
		if len(r.Data[0].Points) > 181 {
			t.Fatal("unbounded history")
		}
	}
}

func TestEveryHourlyRange(t *testing.T) {
	now := time.Date(2026, 9, 18, 9, 42, 0, 0, time.UTC)
	for hours := 1; hours <= 24; hours++ {
		start, err := rangeStart(fmt.Sprintf("%dh", hours), now, time.UTC)
		if err != nil || now.Sub(start) != time.Duration(hours)*time.Hour {
			t.Fatal(hours, start, err)
		}
	}
	for _, invalid := range []string{"0h", "25h", "1.5h", "-2h", "02h"} {
		if _, err := rangeStart(invalid, now, time.UTC); err == nil {
			t.Fatal(invalid)
		}
	}
}

package main

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestCacheWriteBillingDefaultsOverridesAndSuffixCosts(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	input, output, read, write, hour := int64(1e6), int64(2e5), int64(3e5), int64(1e5), int64(2e4)
	for _, model := range []string{"gpt-6-astra", "claude-fable-5"} {
		f := Fact{Schema: 1, EventID: model, Kind: "request", AtMS: time.Now().Add(-time.Second).UnixMilli(), Provider: "p", Model: model + "-test", Outcome: "success", InputTokens: &input, OutputTokens: &output, CacheReadTokens: &read, CacheWriteTokens: &write, CacheWrite1hTokens: &hour}
		if err := e.Import(ctx, model+".jsonl", "etag", []Fact{f}); err != nil {
			t.Fatal(err)
		}
		price := Price{Model: model, Input: 4, Output: 9, CacheRead: .3, CacheWrite: 6, CacheWrite1h: 8, Verified: true}
		no, yes := false, true
		for _, flag := range []*bool{nil, &no, &yes, &no} {
			price.ChargeCacheWrite = flag
			if err := e.SavePrice(ctx, price); err != nil {
				t.Fatal(err)
			}
			result, err := e.Query(ctx, QueryFilter{Range: "all", Model: model + "-test"})
			if err != nil {
				t.Fatal(err)
			}
			want := 4.29 // ordinary input + output + cache reads
			enabled := model != "gpt-6-astra"
			if flag != nil {
				enabled = *flag
			}
			if enabled {
				want += .64
			} // both the 5m and 1h writes
			got, ok := result.Total["estimated_cost_usd"].(float64)
			if !ok || math.Abs(got-want) > 1e-9 {
				t.Fatalf("%s flag=%v: cost=%v want=%v", model, flag, got, want)
			}
			if result.Total["cache_write_tokens"] != float64(write) || result.Total["cache_read_tokens"] != float64(read) {
				t.Fatal("toggle changed usage", result.Total)
			}
		}
	}
}

func TestCacheWriteSettingSurvivesReplicaReplacement(t *testing.T) {
	ctx := context.Background()
	e := stateTestEngine(t)
	for _, model := range []string{"gpt-6-astra", "claude-fable-5"} {
		enabled := model == "gpt-6-astra"
		if err := e.SavePrice(ctx, Price{Model: model, Input: 4, CacheWrite: 6, CacheWrite1h: 8, ChargeCacheWrite: &enabled, Verified: true}); err != nil {
			t.Fatal(err)
		}
	}
	other := replicaTestEngine(t, e)
	prices, err := other.Prices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prices {
		if p.Model == "gpt-6-astra" || p.Model == "claude-fable-5" {
			if p.ChargeCacheWrite == nil || *p.ChargeCacheWrite != (p.Model == "gpt-6-astra") || p.CacheWrite != 6 || p.CacheWrite1h != 8 {
				t.Fatal("lost override", p)
			}
		}
	}
}

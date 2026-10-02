package main

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestMappedUsageUsesRecordedUpstreamPriceAndPolicy(t *testing.T) {
	ctx := context.Background()
	e := stateTestEngine(t)
	now := time.Now().UTC().Truncate(time.Minute)
	yes, no := true, false
	upstreamSale, aliasSale := 15.0, 2.5
	for _, p := range []Price{
		{Model: "gpt-5.4", Input: 2.5, Output: 15, CacheRead: .25, Verified: true, LongContextPremium: &no, ChargeCacheWrite: &no, SalePercent: &aliasSale},
		{Model: "gpt-5.5", Input: 5, Output: 30, CacheRead: .5, Verified: true, LongContextPremium: &yes, ChargeCacheWrite: &no, SalePercent: &upstreamSale},
		{Model: "unconfirmed-upstream", Input: 999, Verified: false},
	} {
		if err := e.SavePrice(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	type sample struct {
		id, upstream string
		input, read  int64
		days         int
		want         float64
		priced       bool
		priceModel   string
		sale         float64
	}
	samples := []sample{
		{"mapped-long", "gpt-5.5", 300000, 200000, 0, 1.245, true, "gpt-5.5", 15},
		{"mapped-boundary", "gpt-5.5", 272000, 200000, 0, .49, true, "gpt-5.5", 15},
		{"mapped-short-a", "gpt-5.5", 200000, 100000, 0, .58, true, "gpt-5.5", 15},
		{"mapped-short-b", "gpt-5.5", 200000, 100000, 0, .58, true, "gpt-5.5", 15},
		{"historical-direct", "gpt-5.4", 300000, 200000, 3, .315, true, "gpt-5.4", 2.5},
		{"legacy", "", 300000, 200000, 0, .315, true, "gpt-5.4", 2.5},
		{"upstream-suffix", "gpt-5.5-20260901", 300000, 200000, 0, 1.245, true, "gpt-5.5", 15},
		{"unknown", "not-in-price-catalog", 300000, 200000, 0, 0, false, "not-in-price-catalog", 2.5},
		{"unconfirmed", "unconfirmed-upstream", 300000, 200000, 0, 0, false, "unconfirmed-upstream", 2.5},
	}
	facts := []Fact{}
	for _, s := range samples {
		input, read, output, actual := s.input, s.read, int64(1000), .01
		facts = append(facts, Fact{Schema: 1, EventID: s.id, Kind: "request", SourceID: "s1", KeyID: "k1", Provider: "route", Model: "gpt-5.4", UpstreamModel: s.upstream, Endpoint: "/v1/responses", Stream: true, Outcome: "success", AtMS: now.Add(-time.Duration(s.days)*24*time.Hour - 10*time.Minute).UnixMilli(), InputTokens: &input, OutputTokens: &output, CacheReadTokens: &read, ActualCostUSD: &actual})
	}
	if err := e.Import(ctx, "mapped-history", "v1", facts); err != nil {
		t.Fatal(err)
	}
	// Existing checkpoint history is repriced on read; no route/config rewrite or
	// fact migration is needed, including the old day-bucket mapping.
	objects := &fakeStateObjects{}
	checkpoint := newCheckpointStore(objects, Config{StateBucket: "test", Timezone: "UTC"})
	if err := checkpoint.save(ctx, e); err != nil {
		t.Fatal(err)
	}
	restored := stateTestEngine(t)
	if ok, err := checkpoint.restore(ctx, restored); err != nil || !ok {
		t.Fatal(ok, err)
	}
	prices, err := e.Prices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = restored.applyPrices(ctx, prices); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []QueryFilter{
		{Range: "all", Model: "gpt-5.4"}, {Range: "24h", Model: "gpt-5.4", SourceID: "s1", KeyID: "k1", Provider: "route", Endpoint: "/v1/responses", Stream: "true"},
		{Range: "all", Model: "gpt-5.4", UpstreamModel: "gpt-5.5"},
	} {
		filter.To = now.Unix()
		result, err := restored.Query(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		expected := map[string]float64{}
		pricing := map[string]sample{}
		total := 0.0
		count := 0
		for _, s := range samples {
			if filter.Range == "24h" && s.days > 0 || filter.UpstreamModel != "" && filter.UpstreamModel != s.upstream {
				continue
			}
			pricing[s.upstream] = s
			count++
			if s.priced {
				expected[s.upstream] += s.want
				total += s.want
			}
		}
		if len(result.Data) != len(pricing) {
			t.Fatalf("lost mapping dimensions: %+v", result.Data)
		}
		for _, row := range result.Data {
			s := pricing[row.UpstreamModel]
			if row.Model != "gpt-5.4" || row.Stats["pricing_model"] != s.priceModel || row.Stats["sale_percent"] != s.sale {
				t.Fatalf("wrong identity or policy: %+v", row)
			}
			got, ok := row.Stats["estimated_cost_usd"].(float64)
			if s.priced {
				if !ok || math.Abs(got-expected[row.UpstreamModel]) > 1e-9 {
					t.Fatalf("%s got %v want %v", row.UpstreamModel, got, expected[row.UpstreamModel])
				}
			} else if ok {
				t.Fatalf("unknown upstream borrowed alias price: %+v", row)
			}
		}
		if got, ok := result.Total["estimated_cost_usd"].(float64); !ok || math.Abs(got-total) > 1e-9 {
			t.Fatalf("total=%v want %v", result.Total, total)
		}
		if len(result.Models) != 1 || result.Models[0]["model"] != "gpt-5.4" || math.Abs(result.Models[0]["estimated_cost_usd"].(float64)-total) > 1e-9 {
			t.Fatal("public model summary changed", result.Models)
		}
		if math.Abs(result.Total["actual_cost_usd"].(float64)-float64(count)*.01) > 1e-9 {
			t.Fatal("upstream charge changed", result.Total)
		}
	}
}

func TestMappedLongContextUsesUpstreamCacheWriteAndPremiumSwitches(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	yes, no := true, false
	input, read, write, hour, output := int64(300000), int64(200000), int64(50000), int64(20000), int64(2000)
	if err := e.Import(ctx, "mapped-caches", "v1", []Fact{{Schema: 1, EventID: "alias", Kind: "request", Model: "gpt-5.4", UpstreamModel: "claude-fable-5", Provider: "p", Outcome: "success", AtMS: time.Now().Add(-10 * time.Minute).UnixMilli(), InputTokens: &input, OutputTokens: &output, CacheReadTokens: &read, CacheWriteTokens: &write, CacheWrite1hTokens: &hour}}); err != nil {
		t.Fatal(err)
	}
	// The alias premium is deliberately on: it cannot override the upstream off.
	if err := e.SavePrice(ctx, Price{Model: "gpt-5.4", Input: 999, Verified: true, LongContextPremium: &yes, ChargeCacheWrite: &no}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		premium, write *bool
		want           float64
	}{{&yes, &yes, 1.47}, {&yes, &no, .63}, {&no, &yes, .74}, {&no, &no, .32}} {
		if err := e.SavePrice(ctx, Price{Model: "claude-fable-5", Input: 4, Output: 10, CacheRead: .5, CacheWrite: 6, CacheWrite1h: 12, Verified: true, LongContextPremium: test.premium, ChargeCacheWrite: test.write}); err != nil {
			t.Fatal(err)
		}
		result, err := e.Query(ctx, QueryFilter{Range: "24h", Model: "gpt-5.4"})
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := result.Total["estimated_cost_usd"].(float64); !ok || math.Abs(got-test.want) > 1e-9 {
			t.Fatalf("premium=%v write=%v got=%v want=%v", *test.premium, *test.write, result.Total, test.want)
		}
	}
}

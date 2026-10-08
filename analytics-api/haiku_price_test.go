package main

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestHaikuPromptTierPerRequestAndMappedHistory(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	type sample struct {
		id, model, upstream, source string
		input, read, write, hour    int64
		days                        int
		want                        float64
	}
	// Expected dollar values use the published two-tier price table, including
	// both cache-write TTLs. input is already cache-inclusive in analytics facts.
	samples := []sample{
		{"exact-boundary", "claude-haiku-5-5", "", "s1", 100000, 0, 0, 0, 0, .0105},
		{"over-boundary", "claude-haiku-5-5", "", "s1", 100001, 0, 0, 0, 0, .0525005},
		{"short-a", "claude-haiku-5-5", "", "s1", 60000, 0, 0, 0, 0, .0065},
		{"short-b", "claude-haiku-5-5", "", "s1", 60000, 0, 0, 0, 0, .0065},
		{"cached-boundary", "claude-haiku-5-5", "", "s1", 100000, 80000, 15000, 5000, 0, .00405},
		{"cached-over", "claude-haiku-5-5", "", "s1", 100001, 80000, 15000, 5000, 0, .0202505},
		{"mapped-haiku", "public-alias", "claude-haiku-5-5-thinking", "s1", 100001, 80000, 15000, 5000, 3, .0202505},
		{"mapped-other", "claude-haiku-5-5", "claude-haiku-4-5-20251001", "s1", 100001, 0, 0, 0, 0, .105001},
		{"other-source", "claude-haiku-5-5", "", "s2", 100001, 0, 0, 0, 0, .0525005},
	}
	facts := []Fact{}
	for _, s := range samples {
		input, read, write, hour, output, actual := s.input, s.read, s.write, s.hour, int64(1000), .123
		facts = append(facts, Fact{Schema: 1, EventID: s.id, Kind: "request", SourceID: s.source, Provider: "provider", Model: s.model, UpstreamModel: s.upstream, KeyID: "key", Endpoint: "/v1/messages", Stream: true, Outcome: "success", AtMS: now.Add(-time.Duration(s.days)*24*time.Hour - time.Minute).UnixMilli(), InputTokens: &input, OutputTokens: &output, CacheReadTokens: &read, CacheWriteTokens: &write, CacheWrite1hTokens: &hour, ActualCostUSD: &actual})
	}
	if err := e.Import(ctx, "haiku-mixed", "v1", facts); err != nil {
		t.Fatal(err)
	}
	// The tier applies to restored facts too, without changing checkpoint format.
	objects := &fakeStateObjects{}
	store := newCheckpointStore(objects, Config{StateBucket: "test", Timezone: "UTC"})
	if err := store.save(ctx, e); err != nil {
		t.Fatal(err)
	}
	restored := stateTestEngine(t)
	if ok, err := store.restore(ctx, restored); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, filter := range []QueryFilter{
		{Range: "all"},
		{Range: "24h", SourceID: "s1", Model: "claude-haiku-5-5", KeyID: "key", Endpoint: "/v1/messages", Stream: "true"},
		{Range: "all", Model: "public-alias", UpstreamModel: "claude-haiku-5-5-thinking"},
		{Range: "all", SourceIDs: []string{"s2"}},
	} {
		filter.To = now.Unix()
		got, err := restored.Query(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		want, count := 0.0, 0
		for _, s := range samples {
			if filter.Range == "24h" && s.days > 0 || filter.SourceID != "" && s.source != filter.SourceID || filter.Model != "" && s.model != filter.Model || filter.SourceIDs != nil && s.source != filter.SourceIDs[0] {
				continue
			}
			want += s.want
			count++
		}
		cost, ok := got.Total["estimated_cost_usd"].(float64)
		if !ok || math.Abs(cost-want) > 1e-9 {
			t.Fatalf("%+v cost=%v want=%v", filter, got.Total, want)
		}
		if math.Abs(got.Total["actual_cost_usd"].(float64)-float64(count)*.123) > 1e-9 {
			t.Fatal("actual ledger charge changed", got.Total)
		}
		rowsTotal := 0.0
		for _, row := range got.Data {
			rowsTotal += row.Stats["estimated_cost_usd"].(float64)
		}
		if math.Abs(rowsTotal-want) > 1e-9 {
			t.Fatalf("channel costs=%v want=%v", rowsTotal, want)
		}
	}
}

func TestHaikuTierPreservesOverridesAndDoesNotStack272k(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	// 300k total: 50k ordinary, 200k reads, 30k 5m writes, 20k 1h writes.
	input, output, read, write, hour := int64(300000), int64(2000), int64(200000), int64(50000), int64(20000)
	if err := e.Import(ctx, "haiku-long", "v1", []Fact{{Schema: 1, EventID: "haiku-long", Kind: "request", Model: "claude-haiku-5-5", Provider: "p", Outcome: "success", AtMS: time.Now().Add(-time.Minute).UnixMilli(), InputTokens: &input, OutputTokens: &output, CacheReadTokens: &read, CacheWriteTokens: &write, CacheWrite1hTokens: &hour}}); err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	for _, tc := range []struct {
		write, premium, verified *bool
		want                     float64
	}{
		{&yes, &no, &yes, .1575}, {&yes, &yes, &yes, .1575}, {&no, &yes, &yes, .08}, {&yes, &no, &no, 0},
	} {
		// Custom base rates (twice official) survive save and authoritative reload.
		custom := Price{Model: "claude-haiku-5-5", Input: .2, Output: 1, CacheRead: .02, CacheWrite: .25, CacheWrite1h: .4, Source: "manual", Verified: *tc.verified, ChargeCacheWrite: tc.write, LongContextPremium: tc.premium}
		if err := e.SavePrice(ctx, custom); err != nil {
			t.Fatal(err)
		}
		if err := e.applyPrices(ctx, []Price{custom}); err != nil {
			t.Fatal(err)
		}
		prices, err := e.Prices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, p := range prices {
			if p.Model == custom.Model {
				found = true
				if p.Input != .2 || p.Verified != *tc.verified || p.PromptPriceTier == nil || p.PromptPriceTier.ThresholdTokens != 100000 {
					t.Fatalf("override or derived tier lost: %+v", p)
				}
			}
		}
		if !found {
			t.Fatal("Haiku missing")
		}
		result, err := e.Query(ctx, QueryFilter{Range: "all"})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := result.Total["estimated_cost_usd"].(float64)
		if *tc.verified && (!ok || math.Abs(got-tc.want) > 1e-9) || !*tc.verified && ok {
			t.Fatalf("got=%v want=%v verified=%v", result.Total, tc.want, *tc.verified)
		}
	}
}

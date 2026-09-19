package main

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestCatalogFallbackPreservesPersistedOperatorSettings(t *testing.T) {
	e, err := OpenEngine(filepath.Join(t.TempDir(), "prices.duckdb"), Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx := context.Background()
	// An older authoritative S3 document replaces the local seeded table.
	if err = e.applyPrices(ctx, []Price{
		{Model: "gemini-3.1-pro", Source: "fact-discovered"},
		{Model: "glm-5.3", Input: 9, Output: 8, Source: "manual", Verified: false},
	}); err != nil {
		t.Fatal(err)
	}
	prices, err := e.Prices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byModel := map[string]Price{}
	for _, p := range prices {
		byModel[p.Model] = p
	}
	if p := byModel["gemini-3.1-pro"]; p.Input != 2 || p.Output != 12 || !p.Verified {
		t.Fatal(p)
	}
	if p := byModel["glm-5.3"]; p.Input != 9 || p.Verified {
		t.Fatal("manual price changed", p)
	}
	if p := byModel["codex-auto-review"]; p.Verified {
		t.Fatal("unknown price marked verified")
	}
	if len(byModel) != len(subModels) {
		t.Fatal("missing catalog entries", len(byModel))
	}
	var persisted float64
	if err = e.DB.QueryRow("SELECT input FROM prices WHERE model='gemini-3.1-pro'").Scan(&persisted); err != nil || persisted != 0 {
		t.Fatal("read changed authoritative state", err, persisted)
	}
}

func TestSuffixCostsFollowCurrentBaseIncludingCacheAndNeverUseStaleOverride(t *testing.T) {
	e, err := OpenEngine(filepath.Join(t.TempDir(), "costs.duckdb"), Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx := context.Background()
	base := Price{Model: "gemini-3.1-pro", Input: 4, Output: 9, CacheRead: .3, CacheWrite: 6, CacheWrite1h: 8, Verified: true, Source: "manual"}
	for _, p := range []Price{base, {Model: "gemini-3.1-pro-search", Input: 999, Output: 999, Verified: true}} {
		if err = e.SavePrice(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	input, output, read, write, hour := int64(1e6), int64(2e5), int64(3e5), int64(1e5), int64(2e4)
	f := Fact{Schema: 1, EventID: "suffix-cost", Kind: "request", AtMS: time.Now().Add(-time.Second).UnixMilli(), Provider: "p", Model: "gemini-3.1-pro-search", UpstreamModel: "gemini-3.1-pro", Outcome: "success", InputTokens: &input, OutputTokens: &output, CacheReadTokens: &read, CacheWriteTokens: &write, CacheWrite1hTokens: &hour}
	if err = e.Import(ctx, "suffix.jsonl", "etag", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	result, err := e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := result.Total["estimated_cost_usd"].(float64)
	if !ok || math.Abs(got-4.93) > 1e-9 {
		t.Fatalf("cost=%v summary=%v", got, result.Total)
	}
	base.Verified = false
	if err = e.SavePrice(ctx, base); err != nil {
		t.Fatal(err)
	}
	result, err = e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total["estimated_cost_usd"] != nil {
		t.Fatal("unconfirmed base fell back to suffix", result.Total)
	}
}

func TestPricePrefixBoundaries(t *testing.T) {
	for input, want := range map[string]string{"glm-5.3-flash-thinking": "glm-5.3-flash", "claude-fable-5-1-high": "claude-fable-5-1", "gpt-5.50": "gpt-5.50", "gemini-3.1-pro-search": "gemini-3.1-pro"} {
		if got := canonicalPriceModel(input); got != want {
			t.Errorf("%s: got %s want %s", input, got, want)
		}
	}
}

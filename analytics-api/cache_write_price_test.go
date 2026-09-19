package main

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
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

func TestCacheWriteSettingSurvivesS3AndLegacyDatabaseUpgrade(t *testing.T) {
	ctx := context.Background()
	objects := &fakeStateObjects{}
	e := stateTestEngine(t)
	state := newStateStore(objects, Config{StateBucket: "test", StatePrefix: "v1"})
	if err := state.sync(ctx, e); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"gpt-6-astra", "claude-fable-5"} {
		enabled := model == "gpt-6-astra" // override each model's default
		if err := state.save(ctx, e, Price{Model: model, Input: 4, CacheWrite: 6, CacheWrite1h: 8, ChargeCacheWrite: &enabled, Verified: true}); err != nil {
			t.Fatal(err)
		}
	}
	var doc priceState
	if err := json.Unmarshal(objects.body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, p := range doc.Prices {
		if p.Model == "claude-fable-5" && (p.ChargeCacheWrite == nil || *p.ChargeCacheWrite) {
			t.Fatal("explicit false omitted from S3")
		}
	}
	other := stateTestEngine(t)
	if err := newStateStore(objects, Config{StateBucket: "test", StatePrefix: "v1"}).sync(ctx, other); err != nil {
		t.Fatal(err)
	}
	prices, err := other.Prices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prices {
		if p.Model == "gpt-6-astra" || p.Model == "claude-fable-5" {
			if p.ChargeCacheWrite == nil || *p.ChargeCacheWrite != (p.Model == "gpt-6-astra") || p.CacheWrite != 6 || p.CacheWrite1h != 8 {
				t.Fatal("lost override or rates", p)
			}
		}
	}
	path := filepath.Join(t.TempDir(), "legacy.duckdb")
	legacy, err := OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.DB.Exec("ALTER TABLE prices DROP COLUMN charge_cache_write"); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	legacy, err = OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	prices, err = legacy.Prices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prices {
		if p.Model == "gpt-6-astra" && p.chargesCacheWrite() {
			t.Fatal("legacy GPT did not get default")
		}
		if p.Model == "claude-fable-5" && !p.chargesCacheWrite() {
			t.Fatal("legacy Claude did not get default")
		}
	}
}

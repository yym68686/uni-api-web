package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLongContextPremiumPerRequestBoundariesAndFilters(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	type sample struct {
		fact        Fact
		base, extra float64
	}
	samples := []sample{}
	add := func(id string, input, output, read, write int64, base, extra float64) {
		samples = append(samples, sample{fact: Fact{Schema: 1, EventID: id, Kind: "request", AtMS: now.Add(-10 * time.Minute).UnixMilli(), SourceID: "s1", KeyID: "k1", Provider: "p", Model: "gpt-6-sol-test", UpstreamModel: "up", Endpoint: "/v1/responses", Stream: true, Outcome: "success", InputTokens: &input, OutputTokens: &output, CacheReadTokens: &read, CacheWriteTokens: &write}, base: base, extra: extra})
	}
	// Multiple short requests in the same rollup must not trip the threshold.
	add("short-a", 200000, 1000, 0, 0, .81, 0)
	add("short-b", 200000, 1000, 0, 0, .81, 0)
	add("boundary", 272000, 1000, 0, 0, 1.098, 0)
	add("long", 272001, 1000, 0, 0, 1.098004, 1.093004)
	// Total prompt includes cached tokens, but cache rates are independent.
	add("cached", 300000, 2000, 200000, 50000, .62, .21)
	add("day-bucket", 300000, 1000, 0, 0, 1.21, 1.205)
	samples[len(samples)-1].fact.AtMS = now.Add(-72 * time.Hour).UnixMilli()
	add("other-source", 300000, 1000, 0, 0, 1.21, 1.205)
	samples[len(samples)-1].fact.SourceID = "s2"
	add("other-key", 300000, 1000, 0, 0, 1.21, 1.205)
	samples[len(samples)-1].fact.KeyID = "k2"
	add("nonstream", 300000, 1000, 0, 0, 1.21, 1.205)
	samples[len(samples)-1].fact.Stream = false
	add("other-endpoint", 300000, 1000, 0, 0, 1.21, 1.205)
	samples[len(samples)-1].fact.Endpoint = "/v1/chat/completions"
	add("other-model", 300000, 1000, 0, 0, 1.21, 0)
	samples[len(samples)-1].fact.Model = "gpt-6-luna"
	facts := []Fact{}
	for _, s := range samples {
		facts = append(facts, s.fact)
	}
	if err := e.Import(ctx, "requests", "v1", facts); err != nil {
		t.Fatal(err)
	}
	// Unknown usage must stay unknown, even with output present.
	unknown := facts[0]
	unknown.EventID = "unknown"
	unknown.Provider = "unknown"
	unknown.InputTokens = nil
	unknown.OutputTokens = nil
	unknown.CacheReadTokens = nil
	unknown.CacheWriteTokens = nil
	if err := e.Import(ctx, "unknown", "v1", []Fact{unknown}); err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		if err := e.SavePrice(ctx, Price{Model: model, Input: 4, Output: 10, CacheRead: .5, CacheWrite: 6, Verified: true, ChargeCacheWrite: &yes}); err != nil {
			t.Fatal(err)
		}
	}
	// A stale suffix price must not override its base model's setting/rates.
	if err := e.SavePrice(ctx, Price{Model: "gpt-6-sol-test", Input: 999, Verified: true, LongContextPremium: &no}); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []*bool{nil, &yes, &no} {
		if err := e.SavePrice(ctx, Price{Model: "gpt-6-sol", Input: 4, Output: 10, CacheRead: .5, CacheWrite: 6, Verified: true, ChargeCacheWrite: &yes, LongContextPremium: flag}); err != nil {
			t.Fatal(err)
		}
		for _, filter := range []QueryFilter{
			{Range: "all"}, {Range: "24h"}, {Range: "all", SourceID: "s1", KeyID: "k1", Stream: "true", Endpoint: "/v1/responses", Model: "gpt-6-sol-test", UpstreamModel: "up", Provider: "p"},
			{Range: "all", SourceIDs: []string{"s2"}}, {Range: "all", SourceIDs: []string{}}, {Range: "all", Model: "gpt-6-luna"},
		} {
			filter.To = now.Unix()
			result, err := e.Query(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			want, count := 0.0, 0
			for _, s := range samples {
				f := s.fact
				if filter.Range == "24h" && f.AtMS < now.Add(-24*time.Hour).UnixMilli() {
					continue
				}
				if filter.SourceID != "" && f.SourceID != filter.SourceID || filter.KeyID != "" && f.KeyID != filter.KeyID || filter.Endpoint != "" && f.Endpoint != filter.Endpoint || filter.Model != "" && f.Model != filter.Model || filter.Stream == "true" && !f.Stream {
					continue
				}
				if filter.SourceIDs != nil && (len(filter.SourceIDs) == 0 || f.SourceID != filter.SourceIDs[0]) {
					continue
				}
				want += s.base
				count++
				if flag != nil && *flag {
					want += s.extra
				}
			}
			got, ok := result.Total["estimated_cost_usd"].(float64)
			if count == 0 {
				if ok {
					t.Fatal("empty scope priced", got)
				}
				continue
			}
			if !ok || math.Abs(got-want) > 1e-9 {
				t.Fatalf("flag=%v filter=%+v cost=%v want=%v", flag, filter, result.Total["estimated_cost_usd"], want)
			}
			rowTotal := 0.0
			for _, row := range result.Data {
				if cost, ok := row.Stats["estimated_cost_usd"].(float64); ok {
					rowTotal += cost
				} else if row.Provider != "unknown" {
					t.Fatal("priced row lost cost", row)
				}
			}
			if math.Abs(rowTotal-want) > 1e-9 {
				t.Fatal("channel and request estimates diverged", rowTotal, want)
			}
		}
	}
}

func TestLongContextPremiumPersistsAndLegacyClientsPreserveIt(t *testing.T) {
	ctx := context.Background()
	e := stateTestEngine(t)
	objects := &fakeStateObjects{}
	state := newStateStore(objects, Config{StateBucket: "settings"})
	if err := state.sync(ctx, e); err != nil {
		t.Fatal(err)
	}
	svc := &Service{engine: e, state: state}
	svc.stateReady.Store(true)
	save := func(body string) {
		r := httptest.NewRequest("PUT", "/v1/prices/gpt-6-sol", strings.NewReader(body))
		r.SetPathValue("model", "gpt-6-sol")
		w := httptest.NewRecorder()
		svc.savePrice(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	save(`{"input":4,"output":10,"verified":true,"long_context_premium":true}`)
	save(`{"input":4,"output":10,"verified":true}`)
	other := stateTestEngine(t)
	syncState := newStateStore(objects, Config{StateBucket: "settings"})
	assert := func(want bool) {
		t.Helper()
		if err := syncState.sync(ctx, other); err != nil {
			t.Fatal(err)
		}
		prices, err := other.Prices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range prices {
			if p.Model == "gpt-6-sol" && (p.LongContextPremium == nil || *p.LongContextPremium != want) {
				t.Fatal("lost setting", p)
			}
		}
	}
	assert(true)
	if err := other.SavePrice(ctx, Price{Model: "gpt-6-sol", Input: 4, Output: 10, Verified: true}); err != nil {
		t.Fatal(err)
	}
	prices, _ := other.Prices(ctx)
	for _, p := range prices {
		if p.Model == "gpt-6-sol" && !p.chargesLongContextPremium() {
			t.Fatal("legacy local save cleared setting")
		}
	}
	save(`{"input":4,"output":10,"verified":true,"long_context_premium":false}`)
	assert(false)
	var doc priceState
	if err := json.Unmarshal(objects.body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, p := range doc.Prices {
		if p.Model == "gpt-6-sol" && (p.LongContextPremium == nil || *p.LongContextPremium) {
			t.Fatal("explicit off missing in S3")
		}
	}
}

func TestLongContextPremiumLegacyDatabaseDefaultsOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.duckdb")
	e, err := OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.DB.Exec("ALTER TABLE prices DROP COLUMN long_context_premium"); err != nil {
		t.Fatal(err)
	}
	e.Close()
	e, err = OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	prices, err := e.Prices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prices {
		if p.LongContextPremium == nil || *p.LongContextPremium {
			t.Fatal("legacy/default model must be off", p.Model)
		}
	}
}

func TestLongContextPremiumRepricesRestoredFactsAndHonorsCacheWriteToggle(t *testing.T) {
	ctx := context.Background()
	source := stateTestEngine(t)
	input, output, read, write := int64(300000), int64(2000), int64(200000), int64(50000)
	at := time.Now().Add(-48 * time.Hour).UnixMilli()
	if err := source.Import(ctx, "historical", "v1", []Fact{{Schema: 1, EventID: "cached-long", Kind: "request", AtMS: at, Model: "gpt-6-sol", Provider: "p", Outcome: "success", InputTokens: &input, OutputTokens: &output, CacheReadTokens: &read, CacheWriteTokens: &write}}); err != nil {
		t.Fatal(err)
	}
	objects := &fakeStateObjects{}
	store := newCheckpointStore(objects, Config{StateBucket: "test", Timezone: "UTC"})
	if err := store.save(ctx, source); err != nil {
		t.Fatal(err)
	}
	restored := stateTestEngine(t)
	if ok, err := store.restore(ctx, restored); err != nil || !ok {
		t.Fatal(ok, err)
	}
	yes, no := true, false
	if err := restored.SavePrice(ctx, Price{Model: "gpt-6-sol", Input: 4, Output: 10, CacheRead: .5, CacheWrite: 6, Verified: true, LongContextPremium: &yes, ChargeCacheWrite: &no}); err != nil {
		t.Fatal(err)
	}
	result, err := restored.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	// 50k ordinary input * $8 + 2k output * $15 + 200k cache read * $0.5.
	if cost, ok := result.Total["estimated_cost_usd"].(float64); !ok || math.Abs(cost-.53) > 1e-9 {
		t.Fatal("restored history/cache billing incorrect", result.Total)
	}
}

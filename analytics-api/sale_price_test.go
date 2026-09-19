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

func TestSalePercentDefaultsValidationAndLegacyUpgrade(t *testing.T) {
	for model, want := range map[string]float64{"gpt-6-astra": 2.5, "claude-fable-5": 15, "gemini-3.1-pro-search": 15, "glm-5.3": 2.5} {
		if got := (Price{Model: model}).salePercent(); got != want {
			t.Fatal(model, got, want)
		}
	}
	for _, value := range []float64{-1, math.Inf(1), math.NaN(), 1e6 + 1} {
		if err := validatePrice(Price{Model: "m", SalePercent: &value}); err == nil {
			t.Fatal("invalid percent accepted", value)
		}
	}
	for _, value := range []float64{0, 2.5, 15, 125} {
		if err := validatePrice(Price{Model: "m", SalePercent: &value}); err != nil {
			t.Fatal(value, err)
		}
	}
	path := filepath.Join(t.TempDir(), "legacy.duckdb")
	e, err := OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.DB.Exec("ALTER TABLE prices DROP COLUMN sale_percent"); err != nil {
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
		if p.SalePercent == nil {
			t.Fatal("missing effective percentage", p.Model)
		}
		if p.Model == "claude-fable-5" && *p.SalePercent != 15 {
			t.Fatal(p)
		}
	}
}

func TestSalePercentPersistsThroughAPIAndS3WithoutChangingReferenceCosts(t *testing.T) {
	ctx := context.Background()
	e := stateTestEngine(t)
	objects := &fakeStateObjects{}
	state := newStateStore(objects, Config{StateBucket: "settings"})
	if err := state.sync(ctx, e); err != nil {
		t.Fatal(err)
	}
	s := &Service{engine: e, state: state}
	s.stateReady.Store(true)
	save := func(model, body string, want int) {
		t.Helper()
		r := httptest.NewRequest("PUT", "/v1/prices/"+model, strings.NewReader(body))
		r.SetPathValue("model", model)
		w := httptest.NewRecorder()
		s.savePrice(w, r)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	save("gemini-3.1-pro", `{"input":4,"output":9,"verified":true,"sale_percent":8,"charge_cache_write":false}`, 200)
	// Old clients can still change official rates without silently resetting sales.
	save("gemini-3.1-pro", `{"input":4,"output":9,"verified":true,"charge_cache_write":false}`, 200)
	save("gpt-6-astra", `{"input":4,"verified":true,"sale_percent":0}`, 200)
	save("claude-fable-5", `{"input":4,"verified":true,"sale_percent":18.75}`, 200)
	save("gemini-3.1-pro", `{"input":4,"verified":true,"sale_percent":-1}`, 400)
	var doc priceState
	if err := json.Unmarshal(objects.body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, p := range doc.Prices {
		if want, ok := map[string]float64{"gemini-3.1-pro": 8, "gpt-6-astra": 0, "claude-fable-5": 18.75}[p.Model]; ok && (p.SalePercent == nil || *p.SalePercent != want) {
			t.Fatal("lost setting", p)
		}
	}
	replica := stateTestEngine(t)
	if err := newStateStore(objects, Config{StateBucket: "settings"}).sync(ctx, replica); err != nil {
		t.Fatal(err)
	}
	// Stale suffix overrides cannot supersede the base model's sale percentage.
	suffixSale := 99.0
	if err := replica.SavePrice(ctx, Price{Model: "gemini-3.1-pro-search", Input: 999, SalePercent: &suffixSale, Verified: true}); err != nil {
		t.Fatal(err)
	}
	input := int64(1e6)
	facts := []Fact{}
	for _, model := range []string{"gemini-3.1-pro-search", "gpt-6-astra", "claude-fable-5"} {
		facts = append(facts, Fact{Schema: 1, Kind: "request", EventID: model, AtMS: time.Now().Add(-time.Second).UnixMilli(), Provider: "p", Model: model, Outcome: "success", InputTokens: &input})
	}
	if err := replica.Import(ctx, "sales-facts", "v1", facts); err != nil {
		t.Fatal(err)
	}
	result, err := replica.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]float64{"gemini-3.1-pro-search": 8, "gpt-6-astra": 0, "claude-fable-5": 18.75}
	for _, row := range result.Data {
		if row.Stats["sale_percent"] != wants[row.Model] || row.Stats["estimated_cost_usd"] != float64(4) {
			t.Fatal("incorrect sales or discounted reference estimate", row)
		}
	}
	// Local-cache saves also preserve an explicit zero for old clients.
	if err := replica.SavePrice(ctx, Price{Model: "gpt-6-astra", Input: 4, Verified: true}); err != nil {
		t.Fatal(err)
	}
	prices, err := replica.Prices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prices {
		if p.Model == "gpt-6-astra" && (p.SalePercent == nil || *p.SalePercent != 0) {
			t.Fatal("zero lost", p)
		}
	}
}

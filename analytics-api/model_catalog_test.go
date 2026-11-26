package main

import (
	"context"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestGPT62OfficialPricesAndManagementScope(t *testing.T) {
	for _, tc := range []struct {
		model, inherited           string
		input, output, read, write float64
	}{
		{"gpt-6.2-astra", "gpt-6-astra", 8, 40, .4, 10},
		{"gpt-6.2-sol", "gpt-6.1-sol", 2, 10, .1, 2.5},
	} {
		t.Run(tc.model, func(t *testing.T) {
			prices := withCatalogPrices(nil)
			find := func(prices []Price, model string) Price {
				t.Helper()
				found := []Price{}
				for _, p := range prices {
					if p.Model == model {
						found = append(found, p)
					}
				}
				if len(found) != 1 {
					t.Fatalf("missing or duplicate model %s: %d", model, len(found))
				}
				return found[0]
			}
			p, inherited := find(prices, tc.model), find(prices, tc.inherited)
			if p.Input != tc.input || p.Output != tc.output || p.CacheRead != tc.read || p.CacheWrite != tc.write || p.CacheWrite1h != tc.write || !p.Verified || p.Source != "https://developers.openai.com/api/docs/pricing" {
				t.Fatal("price differs from the official Standard reference", p)
			}
			if p.chargesCacheWrite() || !p.chargesLongContextPremium() || p.salePercent() != 2.5 || canonicalPriceModel(tc.model+"-high") != tc.model || canonicalPriceModel(tc.inherited+"-high") != tc.inherited {
				t.Fatal("incorrect GPT defaults or price identity", p)
			}
			if !subModelAllowed(tc.model) || subModelProtocol(tc.model) != "responses" ||
				!slices.Contains(managementPageModelCatalog(managementQueryInput{}, ""), tc.model) ||
				!slices.Contains(managementPageDetectionModels(nil), tc.model) {
				t.Fatal("model missing from management/probe catalogs")
			}
			manual, oldManual := p, inherited
			manual.Input, manual.Source, manual.Verified = 7, "manual", false
			oldManual.Input, oldManual.Source, oldManual.Verified = 9, "manual", true
			preserved := withCatalogPrices([]Price{manual, oldManual})
			if !reflect.DeepEqual(find(preserved, tc.model), manual) || !reflect.DeepEqual(find(preserved, tc.inherited), oldManual) {
				t.Fatal("manual prices overwritten or model identities merged")
			}
			hydrated := withCatalogPrices([]Price{{Model: tc.model, Source: "fact-discovered"}})
			if !reflect.DeepEqual(find(hydrated, tc.model), p) {
				t.Fatal("discovered placeholder did not receive the official reference")
			}
			noPremium := false
			explicit := withCatalogPrices([]Price{{Model: tc.model, Source: "fact-discovered", LongContextPremium: &noPremium}})
			if find(explicit, tc.model).chargesLongContextPremium() {
				t.Fatal("explicit operator opt-out overwritten")
			}
		})
	}
	if subModels[0] != "gpt-6-astra" {
		t.Fatal("existing default model changed")
	}
}

func TestGPT62OfficialCostsAtContextBoundaryAndCachedInput(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	for _, tc := range []struct {
		model string
		costs []float64
	}{
		{"gpt-6.2-astra", []float64{2.216, 4.412016, 1.82}},
		{"gpt-6.2-sol", []float64{.554, 1.103004, .455}},
	} {
		for _, price := range withCatalogPrices(nil) {
			if price.Model == tc.model {
				if err := e.SavePrice(ctx, price); err != nil {
					t.Fatal(err)
				}
			}
		}
		for i, usage := range []struct{ input, read int64 }{{272000, 0}, {272001, 0}, {300000, 200000}} {
			id := tc.model + time.Duration(i).String()
			output, actual := int64(1000), .123
			fact := Fact{Schema: 1, EventID: id, Kind: "request", AtMS: time.Now().Add(-time.Minute).UnixMilli(), Provider: id, Model: "public-alias", UpstreamModel: tc.model + "-high", Outcome: "success", InputTokens: &usage.input, OutputTokens: &output, CacheReadTokens: &usage.read, ActualCostUSD: &actual}
			if err := e.Import(ctx, id, "v1", []Fact{fact}); err != nil {
				t.Fatal(err)
			}
			result, err := e.Query(ctx, QueryFilter{Range: "all", Provider: id})
			if err != nil {
				t.Fatal(err)
			}
			cost, ok := result.Total["estimated_cost_usd"].(float64)
			if !ok || math.Abs(cost-tc.costs[i]) > 1e-9 {
				t.Fatalf("%s input=%d read=%d cost=%v want=%v", tc.model, usage.input, usage.read, result.Total, tc.costs[i])
			}
			if result.Total["actual_cost_usd"] != actual {
				t.Fatal("reference price changed actual ledger charge", result.Total)
			}
		}
	}
}

func TestNeonCatalogPreservesPricesAndManagementScope(t *testing.T) {
	const model = "gemini-4-neon"
	find := func(prices []Price) Price {
		t.Helper()
		index := slices.IndexFunc(prices, func(p Price) bool { return p.Model == model })
		if index < 0 || slices.IndexFunc(prices[index+1:], func(p Price) bool { return p.Model == model }) >= 0 {
			t.Fatal("Neon missing or duplicated")
		}
		return prices[index]
	}
	p := find(withCatalogPrices([]Price{{Model: model, Source: "fact-discovered"}}))
	if p.Input != 3 || p.Output != 15 || p.CacheRead != .15 || p.CacheWrite != 3 || p.CacheWrite1h != 3 ||
		!p.Verified || p.Source != "https://aistudio.google.com/docs/pricing#gemini-4-neon" || p.promptPriceTier() != nil {
		t.Fatal("incorrect Neon Standard price", p)
	}
	p.Input, p.Source, p.Verified = 7, "manual", false
	if saved := find(withCatalogPrices([]Price{p})); !reflect.DeepEqual(saved, p) {
		t.Fatal("Neon operator price overwritten", saved)
	}
	if canonicalPriceModel(model+"-high") != model || !subModelAllowed(model) || subModelProtocol(model) != "gemini" ||
		!slices.Contains(managementPageModelCatalog(managementQueryInput{}, ""), model) ||
		!slices.Contains(managementPageDetectionModels(nil), model) {
		t.Fatal("Neon missing from price, management, or detection model list")
	}
}

func TestHydrogenCatalogPreservesPricesAndManagementScope(t *testing.T) {
	const model = "gemini-4-hydrogen"
	prices := withCatalogPrices([]Price{{Model: model, Source: "fact-discovered"}})
	index := slices.IndexFunc(prices, func(p Price) bool { return p.Model == model })
	if index < 0 || slices.IndexFunc(prices[index+1:], func(p Price) bool { return p.Model == model }) >= 0 {
		t.Fatal("Hydrogen missing or duplicated")
	}
	p := prices[index]
	if p.Input != 1.5 || p.Output != 7.5 || p.CacheRead != .075 || p.CacheWrite != 1.5 || p.CacheWrite1h != 1.5 || !p.Verified {
		t.Fatal("incorrect Hydrogen reference", p)
	}
	if !p.chargesCacheWrite() || p.chargesLongContextPremium() || p.promptPriceTier() != nil || p.salePercent() != 15 || canonicalPriceModel(model+"-high") != model {
		t.Fatal("incorrect Gemini defaults or model identity", p)
	}
	for _, p := range withCatalogPrices([]Price{{Model: model, Input: 7, Source: "manual", Verified: false}, {Model: "gemini-4-carbon", Input: 9, Source: "manual", Verified: true}}) {
		if p.Model == model && (p.Input != 7 || p.Verified || p.Source != "manual") {
			t.Fatal("Hydrogen operator price overwritten", p)
		}
		if p.Model == "gemini-4-carbon" && p.Input != 9 {
			t.Fatal("Carbon price overwritten", p)
		}
	}
	if !subModelAllowed(model) || subModelProtocol(model) != "gemini" ||
		!slices.Contains(managementPageModelCatalog(managementQueryInput{}, ""), model) ||
		!slices.Contains(managementPageDetectionModels(nil), model) {
		t.Fatal("Hydrogen missing from management/probe catalogs")
	}
}

func TestFable52CatalogPreservesPricesAndManagementScope(t *testing.T) {
	const model = "claude-fable-5-2"
	prices := withCatalogPrices([]Price{{Model: model, Source: "fact-discovered"}})
	index := slices.IndexFunc(prices, func(p Price) bool { return p.Model == model })
	if index < 0 {
		t.Fatal("Fable 5.2 missing")
	}
	p := prices[index]
	if p.Input != 8 || p.Output != 40 || p.CacheRead != .2 || p.CacheWrite != 10 || p.CacheWrite1h != 16 || !p.Verified {
		t.Fatal("incorrect Fable 5.2 price", p)
	}
	if !p.chargesCacheWrite() || p.promptPriceTier() != nil || p.salePercent() != 15 {
		t.Fatal("incorrect Claude billing defaults", p)
	}
	if canonicalPriceModel(model+"-thinking") != model || canonicalPriceModel("claude-fable-5-1-thinking") != "claude-fable-5-1" {
		t.Fatal("Fable versions share a price identity")
	}
	for _, p := range withCatalogPrices([]Price{{Model: model, Input: 9, Source: "manual", Verified: false}}) {
		if p.Model == model && (p.Input != 9 || p.Verified || p.Source != "manual") {
			t.Fatal("operator price overwritten", p)
		}
		if p.Model == "claude-fable-5-1" && (p.Input != 10 || p.Output != 50 || p.CacheRead != .25) {
			t.Fatal("older Fable price changed", p)
		}
	}
	if !subModelAllowed(model) || subModelProtocol(model) != "messages" ||
		!slices.Contains(managementPageModelCatalog(managementQueryInput{}, ""), model) ||
		!slices.Contains(managementPageDetectionModels(nil), model) {
		t.Fatal("Fable 5.2 missing from management/probe catalogs")
	}
}

func TestCarbonCatalogPreservesPricesAndManagementScope(t *testing.T) {
	const model = "gemini-4-carbon"
	prices := withCatalogPrices([]Price{{Model: model, Source: "fact-discovered"}})
	index := slices.IndexFunc(prices, func(p Price) bool { return p.Model == model })
	if index < 0 {
		t.Fatal("Carbon missing")
	}
	p := prices[index]
	if p.Input != 2 || p.Output != 10 || p.CacheRead != .1 || p.CacheWrite != 2 || p.CacheWrite1h != 2 || !p.Verified {
		t.Fatal("incorrect Carbon reference", p)
	}
	if !p.chargesCacheWrite() || p.chargesLongContextPremium() || p.salePercent() != 15 || canonicalPriceModel(model+"-high") != model {
		t.Fatal("incorrect Gemini defaults or model identity", p)
	}
	for _, p := range withCatalogPrices([]Price{{Model: model, Input: 7, Source: "manual", Verified: false}, {Model: "gemini-4-argon", Input: 9, Source: "manual", Verified: true}}) {
		if p.Model == model && (p.Input != 7 || p.Verified || p.Source != "manual") {
			t.Fatal("Carbon operator price overwritten", p)
		}
		if p.Model == "gemini-4-argon" && p.Input != 9 {
			t.Fatal("Argon price overwritten", p)
		}
	}
	if !subModelAllowed(model) || subModelProtocol(model) != "gemini" ||
		!slices.Contains(managementPageModelCatalog(managementQueryInput{}, ""), model) ||
		!slices.Contains(managementPageDetectionModels(nil), model) {
		t.Fatal("Carbon missing from management/probe catalogs")
	}
}

func TestGPT61TerraCatalogAndOperatorPrices(t *testing.T) {
	const model = "gpt-6.1-terra"
	found := 0
	for _, p := range withCatalogPrices(nil) {
		if p.Model != model {
			continue
		}
		found++
		if p.Input != 1 || p.Output != 5 || p.CacheRead != .05 || p.CacheWrite != 1.25 || p.CacheWrite1h != 1.25 || !p.Verified {
			t.Fatalf("wrong Terra reference: %+v", p)
		}
		if p.chargesCacheWrite() || p.chargesLongContextPremium() || p.salePercent() != 2.5 {
			t.Fatal("GPT operator defaults changed", p)
		}
	}
	if found != 1 || !subModelAllowed(model) || canonicalPriceModel(model+"-high") != model {
		t.Fatal("missing or incorrectly canonicalized Terra", found)
	}
	for _, p := range withCatalogPrices([]Price{{Model: model, Input: 7, Source: "manual", Verified: false}, {Model: "gpt-5.6-terra", Input: 9, Source: "manual", Verified: true}}) {
		if p.Model == model && (p.Input != 7 || p.Verified || p.Source != "manual") {
			t.Fatal("Terra operator price replaced", p)
		}
		if p.Model == "gpt-5.6-terra" && p.Input != 9 {
			t.Fatal("previous Terra price replaced", p)
		}
	}
	for _, p := range withCatalogPrices([]Price{{Model: model, Source: "fact-discovered"}}) {
		if p.Model == model && (p.Input != 1 || !p.Verified) {
			t.Fatal("unconfirmed discovery not hydrated", p)
		}
	}
}

func TestKimi31ProCatalogRemainsIndependentAndUnpricedUntilConfirmed(t *testing.T) {
	const model = "kimi-k3.1-pro"
	found := 0
	for _, p := range withCatalogPrices(nil) {
		if p.Model == model {
			found++
			if p.Verified || p.Input != 0 || p.Output != 0 || p.CacheRead != 0 || p.CacheWrite != 0 || p.CacheWrite1h != 0 {
				t.Fatal("unverified Kimi price invented", p)
			}
		}
	}
	if found != 1 || !subModelAllowed(model) || canonicalPriceModel(model+"-thinking") != model {
		t.Fatal("Kimi 3.1 missing or collapsed into K3")
	}
	for _, p := range withCatalogPrices([]Price{{Model: model, Input: 7, Output: 11, Source: "manual", Verified: true}}) {
		if p.Model == model && (p.Input != 7 || p.Output != 11 || !p.Verified || p.Source != "manual") {
			t.Fatal("operator price replaced", p)
		}
	}
}

func TestGLM54CatalogAndUnconfirmedCost(t *testing.T) {
	const model = "glm-5.4"
	if !slices.Contains(managementPageModelCatalog(managementQueryInput{}, ""), model) ||
		!slices.Contains(managementPageDetectionModels(nil), model) {
		t.Fatal("model missing from management or detection catalog")
	}
	e := stateTestEngine(t)
	ctx := context.Background()
	input, output := int64(1_000_000), int64(1_000_000)
	fact := Fact{
		Schema: 1, EventID: "glm54-cost", Kind: "request",
		AtMS: time.Now().Add(-time.Second).UnixMilli(), Provider: "fixture",
		Model: "glm-5.3", UpstreamModel: model + "-thinking", Outcome: "success",
		InputTokens: &input, OutputTokens: &output,
	}
	if err := e.Import(ctx, "glm54.jsonl", "v1", []Fact{fact}); err != nil {
		t.Fatal(err)
	}
	checkCost := func(want any) {
		t.Helper()
		result, err := e.Query(ctx, QueryFilter{Range: "all"})
		if err != nil {
			t.Fatal(err)
		}
		if got := result.Total["estimated_cost_usd"]; got != want {
			t.Fatalf("estimated cost = %v, want %v", got, want)
		}
	}
	// No confirmed GLM 5.4 price must mean unknown, not zero or the GLM 5.3 price.
	checkCost(nil)
	if err := e.SavePrice(ctx, Price{Model: model, Input: 2, Output: 5, Source: "manual", Verified: true}); err != nil {
		t.Fatal(err)
	}
	checkCost(float64(7))
}

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
		{Model: "gemini-4-argon", Source: "fact-discovered"},
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
	if p := byModel["gemini-4-argon"]; p.Input != 2 || p.Output != 10 || p.CacheRead != .1 || !p.Verified {
		t.Fatal("Argon discovery placeholder was not hydrated", p)
	}
	if p := byModel["glm-5.3"]; p.Input != 9 || p.Verified {
		t.Fatal("manual price changed", p)
	}
	if p := byModel["codex-auto-review"]; p.Verified {
		t.Fatal("unknown price marked verified")
	}
	if len(byModel) != len(modelCatalog) {
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
	for input, want := range map[string]string{"glm-5.3-flash-thinking": "glm-5.3-flash", "claude-fable-5-1-high": "claude-fable-5-1", "claude-sonnet-5-5": "claude-sonnet-5-5", "claude-sonnet-5-5-thinking": "claude-sonnet-5-5", "gpt-5.50": "gpt-5.50", "gpt-6.1-sol-thinking": "gpt-6.1-sol", "gemini-3.1-pro-search": "gemini-3.1-pro"} {
		if got := canonicalPriceModel(input); got != want {
			t.Errorf("%s: got %s want %s", input, got, want)
		}
	}
}

func TestDeepSeekFlash1017CatalogAndOperatorPrices(t *testing.T) {
	const model = "deepseek-v4.1-flash-1017"
	prices := withCatalogPrices(nil)
	found := 0
	for _, p := range prices {
		if p.Model != model {
			continue
		}
		found++
		if p.Input != .3 || p.Output != 1.2 || p.CacheRead != .006 || p.CacheWrite != .3 || p.CacheWrite1h != .3 || !p.Verified {
			t.Fatalf("wrong Flash 1017 reference: %+v", p)
		}
	}
	if found != 1 || !subModelAllowed(model) {
		t.Fatal("missing or duplicate Flash 1017", found)
	}
	for _, name := range []string{model, model + "-thinking"} {
		if canonicalPriceModel(name) != model {
			t.Fatal("new model collapsed into previous Flash", name)
		}
	}
	for _, p := range withCatalogPrices([]Price{{Model: model, Input: 7, Source: "manual", Verified: false}, {Model: "deepseek-v4.1-flash", Input: 9, Source: "manual", Verified: true}}) {
		if p.Model == model && (p.Input != 7 || p.Verified || p.Source != "manual") {
			t.Fatal("manual Flash 1017 price changed", p)
		}
		if p.Model == "deepseek-v4.1-flash" && p.Input != 9 {
			t.Fatal("previous Flash price changed", p)
		}
	}
	for _, p := range withCatalogPrices([]Price{{Model: model, Source: "fact-discovered"}}) {
		if p.Model == model && (p.Input != .3 || !p.Verified) {
			t.Fatal("unconfirmed discovery not hydrated", p)
		}
	}
}

func TestJevInputOnlyPriceAndNativeProtocol(t *testing.T) {
	for _, model := range []string{"jev-latest", "jev-preview", "jev-1.13.0"} {
		var found bool
		for _, price := range withCatalogPrices(nil) {
			if price.Model != model {
				continue
			}
			found = true
			if price.Input != .042 || price.Output != 0 || price.chargesCacheWrite() || price.salePercent() != 100 || !price.Verified {
				t.Fatalf("wrong Jev price: %+v", price)
			}
		}
		if !found {
			t.Fatalf("missing %s", model)
		}
		for _, detector := range subModels {
			if detector == model {
				t.Fatal("Jev must not use the sub2api chat detector")
			}
		}
	}
	e, err := OpenEngine(filepath.Join(t.TempDir(), "jev.duckdb"), Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	input, output := int64(1_000_000), int64(999_999)
	ctx := context.Background()
	if err = e.Import(ctx, "jev.jsonl", "etag", []Fact{{Schema: 1, EventID: "jev-cost", Kind: "request", AtMS: time.Now().Add(-time.Second).UnixMilli(), Provider: "typesafe-jev", Model: "jev-latest", UpstreamModel: "jev-1.13.0", Outcome: "success", InputTokens: &input, OutputTokens: &output}}); err != nil {
		t.Fatal(err)
	}
	result, err := e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	cost, ok := result.Total["estimated_cost_usd"].(float64)
	if !ok || math.Abs(cost-.042) > 1e-9 {
		t.Fatalf("output tokens charged or price wrong: %v", result.Total)
	}
}

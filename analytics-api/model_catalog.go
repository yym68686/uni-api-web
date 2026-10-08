package main

import (
	_ "embed"
	"encoding/json"
	"strings"
	"time"
)

// One catalog supplies both the detector's model list and its default prices.
// Persisted operator prices remain authoritative; this is only a fallback for
// missing prices and the old, unconfirmed fact-discovery placeholders.
//
//go:embed model_catalog.json
var modelCatalogJSON []byte

var modelCatalog = func() []Price {
	var prices []Price
	if err := json.Unmarshal(modelCatalogJSON, &prices); err != nil {
		panic(err)
	}
	for i := range prices {
		prices[i].EffectiveAt = time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	}
	return prices
}()

var subModels = func() []string {
	models := make([]string, 0, len(modelCatalog))
	for _, price := range modelCatalog {
		// System One models use their native endpoint, not the sub2api chat probes.
		if !strings.HasPrefix(price.Model, "jev-") {
			models = append(models, price.Model)
		}
	}
	return models
}()

func canonicalPriceModel(model string) string {
	best := ""
	for _, price := range modelCatalog {
		base := price.Model
		if len(base) > len(best) && (model == base || strings.HasPrefix(model, base+"-")) {
			best = base
		}
	}
	if best != "" {
		return best
	}
	return model
}

// Price immutable request facts by the model actually sent upstream. Public
// aliases (and today's route definitions) must not reprice historical usage as
// a different model. Only legacy facts without an upstream name use the alias.
func usagePriceModel(model, upstream string) string {
	if upstream = strings.TrimSpace(upstream); upstream != "" {
		model = upstream
	}
	return canonicalPriceModel(model)
}

// A missing setting is a legacy/default value, not an explicit opt-out.
func (p Price) chargesCacheWrite() bool {
	if p.ChargeCacheWrite != nil {
		return *p.ChargeCacheWrite
	}
	model := strings.ToLower(p.Model)
	return !strings.HasPrefix(model, "gpt-") && !strings.HasPrefix(model, "jev-")
}

func (p Price) chargesLongContextPremium() bool {
	return p.LongContextPremium != nil && *p.LongContextPremium
}

// Percent of the reference API price, independently configurable per model.
func (p Price) salePercent() float64 {
	if p.SalePercent != nil {
		return *p.SalePercent
	}
	model := strings.ToLower(p.Model)
	if strings.HasPrefix(model, "jev-") {
		return 100
	}
	if strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "gemini-") {
		return 15
	}
	return 2.5
}

// Catalog tiers take precedence over the optional legacy 272k premium, so they
// cannot be disabled or compounded by an older client's checkbox setting.
func catalogPromptPriceTier(model string) *PromptPriceTier {
	model = canonicalPriceModel(model)
	for _, reference := range modelCatalog {
		if reference.Model == model && reference.PromptPriceTier != nil {
			return reference.PromptPriceTier
		}
	}
	return nil
}

func (p Price) promptPriceTier() *PromptPriceTier {
	if tier := catalogPromptPriceTier(p.Model); tier != nil {
		return tier
	}
	if p.chargesLongContextPremium() {
		return &PromptPriceTier{ThresholdTokens: 272000, InputMultiplier: 2, OutputMultiplier: 1.5, CacheMultiplier: 2}
	}
	return nil
}

func withCatalogPrices(prices []Price) []Price {
	indices := make(map[string]int, len(prices))
	for i, p := range prices {
		indices[p.Model] = i
	}
	for _, p := range modelCatalog {
		if i, found := indices[p.Model]; found {
			old := prices[i]
			// Hydrate policy from the current catalog, not from stored metadata.
			prices[i].PromptPriceTier = p.PromptPriceTier
			if old.Source == "fact-discovered" && !old.Verified && old.Input == 0 && old.Output == 0 && old.CacheRead == 0 && old.CacheWrite == 0 && old.CacheWrite1h == 0 {
				p.ChargeCacheWrite = old.ChargeCacheWrite
				p.LongContextPremium = old.LongContextPremium
				p.SalePercent = old.SalePercent
				prices[i] = p
			}
		} else {
			prices = append(prices, p)
		}
	}
	for i := range prices {
		enabled := prices[i].chargesCacheWrite()
		prices[i].ChargeCacheWrite = &enabled
		longContext := prices[i].chargesLongContextPremium()
		prices[i].LongContextPremium = &longContext
		percent := prices[i].salePercent()
		prices[i].SalePercent = &percent
	}
	return prices
}

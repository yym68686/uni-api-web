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
	models := make([]string, len(modelCatalog))
	for i, price := range modelCatalog {
		models[i] = price.Model
	}
	return models
}()

func canonicalPriceModel(model string) string {
	best := ""
	for _, base := range subModels {
		if len(base) > len(best) && (model == base || strings.HasPrefix(model, base+"-")) {
			best = base
		}
	}
	if best != "" {
		return best
	}
	return model
}

func withCatalogPrices(prices []Price) []Price {
	indices := make(map[string]int, len(prices))
	for i, p := range prices {
		indices[p.Model] = i
	}
	for _, p := range modelCatalog {
		if i, found := indices[p.Model]; found {
			old := prices[i]
			if old.Source == "fact-discovered" && !old.Verified && old.Input == 0 && old.Output == 0 && old.CacheRead == 0 && old.CacheWrite == 0 && old.CacheWrite1h == 0 {
				prices[i] = p
			}
		} else {
			prices = append(prices, p)
		}
	}
	return prices
}

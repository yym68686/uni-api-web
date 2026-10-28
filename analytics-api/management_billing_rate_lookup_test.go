package main

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func sameManagementRate(a, b managementRate) bool {
	if a.Mismatch != b.Mismatch || (a.Rate == nil) != (b.Rate == nil) {
		return false
	}
	return a.Rate == nil || math.Float64bits(*a.Rate) == math.Float64bits(*b.Rate)
}

func TestManagementBillingRateLookupPreservesAliasesFallbackAndBindingScope(t *testing.T) {
	rng := rand.New(rand.NewSource(293157))
	models := []string{"", "alias", "upstream", "model-1", "模型", "mapped-empty", "empty-alias", "missing"}
	for fixture := 0; fixture < 1500; fixture++ {
		row := managementQueryRow{Target: subTarget{ObservedRates: map[string]subObservedRate{}}}
		if fixture%7 != 0 {
			row.Target.Billing = &subBilling{Rate: usageFloat(float64(rng.Intn(9)-2) / 10)}
		}
		for _, name := range models {
			if rng.Intn(3) != 0 {
				row.Target.ObservedRates[name] = subObservedRate{Rate: float64(rng.Intn(9)-2) / 10}
			}
		}
		if fixture%3 != 0 {
			row.Configured = &managementQueryChannel{managementChannel: managementChannel{subInstalledChannel: subInstalledChannel{Models: []string{"alias", "模型", "alias"}, ModelMappings: map[string]string{"alias": "upstream", "empty-alias": ""}}}}
			if fixture%4 == 0 {
				row.Configured.ModelMappings[""] = "mapped-empty"
			}
			if fixture%5 == 0 {
				row.Configured.Models = nil
			}
			if fixture%8 == 0 {
				row.Configured.Members = []managementChannel{{subInstalledChannel: subInstalledChannel{AccountID: "first", GroupID: 1}}, {subInstalledChannel: subInstalledChannel{AccountID: "second", GroupID: 1}}}
			} else if fixture%6 == 0 {
				row.Configured.Members = []managementChannel{{subInstalledChannel: subInstalledChannel{Kind: "configured", BindingStatus: "matched", BoundKeys: []subBoundKey{{AccountID: "first", GroupID: 1}, {AccountID: "first", GroupID: 1}}}}}
			}
		}
		before := mustJSON(row)
		for _, model := range models {
			got, want := managementBillingRate(row, model), previousManagementBillingRate(row, model)
			if !sameManagementRate(got, want) || mustJSON(row) != before {
				t.Fatalf("fixture %d model %q changed rate, mismatch or input", fixture, model)
			}
			if row.Target.Billing != nil && got.Rate != nil && want.Rate == row.Target.Billing.Rate && got.Rate != want.Rate {
				t.Fatal("advertised fallback lost its existing pointer")
			}
		}
	}
}

func TestManagementBillingRateLookupKeepsInvalidZeroAndMismatchTolerance(t *testing.T) {
	for _, advertised := range []*float64{nil, usageFloat(0), usageFloat(1), usageFloat(-1), usageFloat(math.NaN()), usageFloat(math.Inf(1))} {
		for _, value := range []float64{0, math.Copysign(0, -1), -1, math.NaN(), math.Inf(1), math.Inf(-1), 0.5, 1 + 5e-10, 1 + 2e-9} {
			row := managementQueryRow{Configured: &managementQueryChannel{managementChannel: managementChannel{subInstalledChannel: subInstalledChannel{Models: []string{"other"}, ModelMappings: map[string]string{"alias": "upstream"}}}}, Target: subTarget{Billing: &subBilling{Rate: advertised}, ObservedRates: map[string]subObservedRate{"upstream": {Rate: value}, "other": {Rate: 99}}}}
			for _, model := range []string{"alias", "upstream", "missing"} {
				if got, want := managementBillingRate(row, model), previousManagementBillingRate(row, model); !sameManagementRate(got, want) {
					t.Fatalf("changed invalid/zero/tolerance behavior for %q", model)
				}
			}
		}
	}
}

var managementBillingLookupSink managementRate

func BenchmarkManagementBillingRateLookup(b *testing.B) {
	for _, size := range []int{33, 160, 288} {
		row := managementQueryRow{Configured: &managementQueryChannel{}, Target: subTarget{Billing: &subBilling{Rate: usageFloat(1)}, ObservedRates: map[string]subObservedRate{}}}
		for i := 0; i < size; i++ {
			name := fmt.Sprintf("model-%03d", i)
			row.Configured.Models = append(row.Configured.Models, name)
			row.Target.ObservedRates[name] = subObservedRate{Rate: float64(i%17) / 10}
		}
		for _, model := range []string{"", "model-017", "missing"} {
			for _, run := range []struct {
				name string
				fn   func(managementQueryRow, string) managementRate
			}{{"previous_scan", previousManagementBillingRate}, {"exact_upstream", managementBillingRate}} {
				b.Run(fmt.Sprintf("%d/%s/%s", size, model, run.name), func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						managementBillingLookupSink = run.fn(row, model)
					}
				})
			}
		}
	}
}

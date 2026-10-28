package main

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func previousSortManagementRowsByRate(rows []managementQueryRow, model, direction string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := managementBillingRate(rows[i], model).Rate, managementBillingRate(rows[j], model).Rate
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		if direction == "asc" {
			return *a < *b
		}
		return *a > *b
	})
}

func TestManagementRateSortPreservesFullRowsStableTiesAndDuplicateIDs(t *testing.T) {
	rng := rand.New(rand.NewSource(7591))
	for size := 0; size < 120; size++ {
		rows := []managementQueryRow{}
		for i := 0; i < size; i++ {
			row := managementQueryRow{ID: fmt.Sprint(i % 3), Account: subAccount{Name: fmt.Sprint(i)}, Target: subTarget{ObservedRates: map[string]subObservedRate{}}}
			if i%7 != 0 {
				row.Target.Billing = &subBilling{Rate: usageFloat(float64(rng.Intn(5)))}
			}
			for _, model := range []string{"gpt-6-astra", "upstream", "alias", "", "模型", "off-page"} {
				if rng.Intn(4) != 0 {
					row.Target.ObservedRates[model] = subObservedRate{Rate: float64(rng.Intn(7) - 1)}
				}
			}
			if i%3 == 0 {
				row.Configured = &managementQueryChannel{managementChannel: managementChannel{subInstalledChannel: subInstalledChannel{Models: []string{"alias", "模型", "alias"}, ModelMappings: map[string]string{"alias": "upstream"}}}}
				if i%9 == 0 {
					row.Configured.Members = []managementChannel{{subInstalledChannel: subInstalledChannel{AccountID: "first", GroupID: 1}}, {subInstalledChannel: subInstalledChannel{AccountID: "second", GroupID: 1}}}
				}
			}
			rows = append(rows, row)
		}
		if size == 1 {
			rows = nil
		}
		before := mustJSON(rows)
		for _, model := range []string{"", "alias", "gpt-6-astra", "模型", "missing"} {
			for _, direction := range []string{"asc", "desc", "legacy-other"} {
				old, got := append([]managementQueryRow(nil), rows...), append([]managementQueryRow(nil), rows...)
				previousSortManagementRowsByRate(old, model, direction)
				sortManagementRowsByRate(got, model, direction)
				if !reflect.DeepEqual(got, old) || mustJSON(got) != mustJSON(old) || mustJSON(rows) != before {
					t.Fatalf("size=%d model=%q direction=%s changed full rows, ties or input evidence", size, model, direction)
				}
				for i := range got {
					if got[i].Target.Billing != old[i].Target.Billing || got[i].Configured != old[i].Configured {
						t.Fatal("sort cloned or rebound request-local evidence")
					}
				}
			}
		}
	}
}

func TestManagementRateSortKeepsUnknownInvalidAndZeroRates(t *testing.T) {
	rows := []managementQueryRow{}
	for i, value := range []float64{0, math.Copysign(0, -1), -1, math.NaN(), math.Inf(1), math.Inf(-1), 2, 2} {
		rows = append(rows, managementQueryRow{ID: fmt.Sprint(i), Target: subTarget{Billing: &subBilling{Rate: usageFloat(value)}}})
	}
	rows = append(rows, managementQueryRow{ID: "no-rate"}, managementQueryRow{ID: "other-no-rate"})
	for _, direction := range []string{"asc", "desc"} {
		old, got := append([]managementQueryRow{}, rows...), append([]managementQueryRow{}, rows...)
		previousSortManagementRowsByRate(old, "", direction)
		sortManagementRowsByRate(got, "", direction)
		for i := range got {
			if got[i].ID != old[i].ID || got[i].Target.Billing != old[i].Target.Billing {
				t.Fatal("invalid-rate or stable-zero order changed")
			}
		}
	}
}

func BenchmarkManagementRateSort(b *testing.B) {
	rows := make([]managementQueryRow, 749)
	for i := range rows {
		row := &rows[i]
		row.ID = fmt.Sprint(i % 3)
		row.Target.Billing = &subBilling{Rate: usageFloat(float64(i%23) / 10)}
		row.Target.ObservedRates = map[string]subObservedRate{}
		for j := 0; j < 288; j++ {
			model := fmt.Sprintf("model-%03d", j)
			row.Target.ObservedRates[model] = subObservedRate{Rate: float64((i+j)%31) / 10}
		}
		if i%3 == 0 {
			row.Configured = &managementQueryChannel{}
			for j := 0; j < 288; j++ {
				row.Configured.Models = append(row.Configured.Models, fmt.Sprintf("model-%03d", j))
			}
		}
	}
	for _, model := range []string{"", "model-123"} {
		for _, run := range []struct {
			name string
			sort func([]managementQueryRow, string, string)
		}{{"previous_repeated", previousSortManagementRowsByRate}, {"once_per_row", sortManagementRowsByRate}} {
			b.Run(model+"/"+run.name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					page := append([]managementQueryRow{}, rows...)
					run.sort(page, model, "asc")
				}
			})
		}
	}
}

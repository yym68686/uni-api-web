package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func TestManagementRateReusePreservesAllFacetsRowsAndStableOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(281977))
	for fixture := 0; fixture < 120; fixture++ {
		rows := []managementQueryRow{}
		evidence := managementFilterEvidence{FingerprintReady: fixture%3 != 0, Fingerprints: map[string][]string{}, Joined: map[string]string{}}
		for i := 0; i < fixture%47; i++ {
			row := managementQueryRow{ID: fmt.Sprint(i % 3), Account: subAccount{Name: fmt.Sprint(i)}, Target: subTarget{Active: true, Platform: []string{"", "openai", "模型", "😀", "\ue000"}[i%5], ObservedRates: map[string]subObservedRate{}}}
			if i%7 != 0 {
				row.Target.Billing = &subBilling{Rate: usageFloat(float64(rng.Intn(7)) / 10)}
			}
			for _, name := range []string{"alias", "upstream", "model-1", "模型", "", "off-page"} {
				if rng.Intn(4) != 0 {
					row.Target.ObservedRates[name] = subObservedRate{Rate: float64(rng.Intn(9)-1) / 10}
				}
			}
			if i%3 == 0 {
				row.Configured = &managementQueryChannel{managementChannel: managementChannel{subInstalledChannel: subInstalledChannel{Models: []string{"alias", "模型", "alias"}, ModelMappings: map[string]string{"alias": "upstream"}}}}
				if i%9 == 0 {
					row.Configured.Members = []managementChannel{{subInstalledChannel: subInstalledChannel{AccountID: "first", GroupID: 1}}, {subInstalledChannel: subInstalledChannel{AccountID: "second", GroupID: 1}}}
				}
			}
			rows = append(rows, row)
			evidence.Joined[row.ID] = []string{"", "joined", "unjoined"}[i%3]
			if i%2 == 0 {
				evidence.Fingerprints[row.ID] = []string{"match", "mismatch"}
			}
		}
		if fixture == 1 {
			rows = nil
		}
		before, beforeEvidence := mustJSON(rows), mustJSON(evidence)
		for sample := 0; sample < 16; sample++ {
			f := managementQueryFilters{
				GroupStatus: "all",
				Model:       []string{"", "alias", "model-1", "模型", "missing"}[rng.Intn(5)],
				MaxRate:     []string{"", "mismatch", "0", "0.5", "-1", "invalid", "NaN", "+Inf", "-Inf", "1e-9999"}[rng.Intn(10)],
				Sort:        []string{"", "asc", "desc", "legacy-other"}[rng.Intn(4)],
				Platform:    []string{"", "openai", "模型", "😀", "\ue000", "missing"}[rng.Intn(6)],
				Joined:      []string{"", "joined", "unjoined"}[rng.Intn(3)],
				Fingerprint: []string{"", "not_applicable", "match", "missing"}[rng.Intn(4)],
			}
			want := previousFilterManagementQueryRows(rows, f, evidence)
			got := filterManagementQueryRows(rows, f, evidence)
			if !reflect.DeepEqual(got, want) || mustJSON(got) != mustJSON(want) || mustJSON(rows) != before || mustJSON(evidence) != beforeEvidence {
				t.Fatalf("fixture %d sample %d changed rows, facets, stable order or inputs; filters=%s", fixture, sample, mustJSON(f))
			}
			for i := range got.Rows {
				if got.Rows[i].Target.Billing != want.Rows[i].Target.Billing || got.Rows[i].Configured != want.Rows[i].Configured {
					t.Fatal("rate reuse rebound request-local evidence")
				}
			}
		}
	}
}

var managementFilterRateSink managementFilteredRows

func BenchmarkManagementFilterRateReuse(b *testing.B) {
	rows := make([]managementQueryRow, 749)
	for i := range rows {
		row := &rows[i]
		row.ID = fmt.Sprint(i % 3)
		row.Target.Active, row.Target.Platform = true, "openai"
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
	for _, tc := range []struct {
		name, model, rate, sort string
	}{{"all_unsorted", "", "", ""}, {"all_sorted", "", "", "asc"}, {"scoped_sorted", "model-123", "", "asc"}, {"scoped_limited", "model-123", "0.9", "desc"}} {
		for _, run := range []struct {
			name string
			fn   func([]managementQueryRow, managementQueryFilters, managementFilterEvidence) managementFilteredRows
		}{{"previous_repeated", previousFilterManagementQueryRows}, {"once_per_candidate", filterManagementQueryRows}} {
			b.Run(tc.name+"/"+run.name, func(b *testing.B) {
				f := managementQueryFilters{GroupStatus: "all", Model: tc.model, MaxRate: tc.rate, Sort: tc.sort}
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					managementFilterRateSink = run.fn(rows, f, managementFilterEvidence{})
				}
			})
		}
	}
}

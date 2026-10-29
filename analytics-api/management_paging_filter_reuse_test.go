package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestManagementPagingFilterReuseMatchesRecomputedDirectory(t *testing.T) {
	var fixture struct {
		Input managementQueryInput `json:"input"`
		Cases []struct {
			Filters  managementQueryFilters   `json:"filters"`
			Evidence managementFilterEvidence `json:"evidence"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(managementContractFixture(t, "testdata/management-query-filters.json.gz"), &fixture); err != nil {
		t.Fatal(err)
	}
	for i, tc := range fixture.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			rows, err := buildManagementQueryRows(fixture.Input, tc.Filters.Model, tc.Filters.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			before := mustJSON(fixture.Input) + mustJSON(rows) + mustJSON(tc.Evidence)
			filtered := filterManagementQueryRows(rows, tc.Filters, tc.Evidence)
			now := time.Unix(1000, 0)
			page, err := buildManagementQueryPageFromRowsMode(fixture.Input, rows, tc.Filters, tc.Evidence, "owner", []byte("key"), "", now, false)
			if err != nil {
				t.Fatal(err)
			}
			got, err := buildManagementQueryPageFromFilteredRowsMode(fixture.Input, filtered, tc.Filters, tc.Evidence, "owner", []byte("key"), "", now, false)
			if err != nil || !reflect.DeepEqual(got, page) || mustJSON(got) != mustJSON(page) {
				t.Fatal("page, revision, cursors or facets changed", err)
			}
			snapshot := managementQuerySnapshot{
				View: managementDatabaseView{Input: fixture.Input, ProjectedModels: true},
				Page: page, Rows: rows, Evidence: tc.Evidence,
				Proof: &managementFreshProof{Stamp: "proof", HeadersRevision: 7},
			}
			previous, current := &Service{}, &Service{}
			previous.rememberManagementPagingPlan("owner", tc.Filters, snapshot)
			snapshot.Filtered = &filtered
			current.rememberManagementPagingPlan("owner", tc.Filters, snapshot)
			if mustJSON(previous.managementComputed.pages) != mustJSON(current.managementComputed.pages) || previous.managementComputed.bytes != current.managementComputed.bytes {
				t.Fatal("directory bytes, order, members, proof or admission changed")
			}
			if before != mustJSON(fixture.Input)+mustJSON(rows)+mustJSON(tc.Evidence) {
				t.Fatal("request input mutated")
			}
		})
	}
}

func TestManagementPagingFilterReuseRetainsPresentEmptyResult(t *testing.T) {
	f := managementQueryFilters{GroupStatus: "all"}
	snapshot := managementQuerySnapshot{
		View:     managementDatabaseView{ProjectedModels: true},
		Page:     managementQueryPage{Total: 0},
		Rows:     []managementQueryRow{{ID: "raw-row", Target: subTarget{Active: true}}},
		Proof:    &managementFreshProof{Stamp: "proof"},
		Filtered: &managementFilteredRows{},
	}
	s := &Service{}
	s.rememberManagementPagingPlan("owner", f, snapshot)
	entry, ok := s.managementComputed.pages[managementPagingKey("owner", f)]
	var plan managementPagingPlan
	if !ok || json.Unmarshal(entry.Plan, &plan) != nil || len(plan.Rows) != 0 {
		t.Fatal("empty result fell back to unfiltered rows")
	}
	snapshot.Page.Total = 1
	s.rememberManagementPagingPlan("owner", f, snapshot)
	if string(s.managementComputed.pages[managementPagingKey("owner", f)].Plan) != string(entry.Plan) {
		t.Fatal("inconsistent count replaced the current directory")
	}
}

func BenchmarkManagementPagingFilterReuse(b *testing.B) {
	rows := make([]managementQueryRow, 749)
	for i := range rows {
		row := &rows[i]
		row.ID, row.Account.ID, row.Target.GroupID = fmt.Sprint(i), fmt.Sprint(i/16), int64(i)
		row.Target.Active, row.Target.Platform = true, "openai"
		row.Target.Billing = &subBilling{Rate: usageFloat(float64(i%23) / 10)}
		row.Target.ObservedRates = map[string]subObservedRate{}
		for j := 0; j < 288; j++ {
			row.Target.ObservedRates[fmt.Sprintf("model-%03d", j)] = subObservedRate{Rate: float64((i+j)%31) / 10}
		}
	}
	for _, model := range []string{"", "model-123"} {
		for _, reuse := range []bool{false, true} {
			b.Run(fmt.Sprintf("model=%s/reuse=%t", model, reuse), func(b *testing.B) {
				f := managementQueryFilters{GroupStatus: "all", Model: model, Sort: "asc"}
				b.ReportAllocs()
				for range b.N {
					filtered := filterManagementQueryRows(rows, f, managementFilterEvidence{})
					snapshot := managementQuerySnapshot{View: managementDatabaseView{ProjectedModels: true}, Page: managementQueryPage{Total: len(filtered.Rows)}, Rows: rows, Proof: &managementFreshProof{Stamp: "proof"}}
					if reuse {
						snapshot.Filtered = &filtered
					}
					s := &Service{}
					s.rememberManagementPagingPlan("owner", f, snapshot)
					if len(s.managementComputed.pages) != 1 {
						b.Fatal("directory was not retained")
					}
				}
			})
		}
	}
}

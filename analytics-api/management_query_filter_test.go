package main

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

func TestManagementFiltersMatchBrowserContract(t *testing.T) {
	raw := managementContractFixture(t, "testdata/management-query-filters.json.gz")
	var fixture struct {
		Input managementQueryInput `json:"input"`
		Cases []struct {
			Filters  managementQueryFilters   `json:"filters"`
			Evidence managementFilterEvidence `json:"evidence"`
			Expected struct {
				IDs       []string  `json:"ids"`
				Rates     []float64 `json:"rates"`
				Platforms []string  `json:"platforms"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for i, c := range fixture.Cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			rows, err := buildManagementQueryRows(fixture.Input, c.Filters.Model, c.Filters.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			before := mustJSON(rows)
			out := filterManagementQueryRows(rows, c.Filters, c.Evidence)
			if mustJSON(rows) != before {
				t.Fatal("filter mutated request view")
			}
			ids := []string{}
			for _, row := range out.Rows {
				ids = append(ids, row.ID)
			}
			if !reflect.DeepEqual(ids, c.Expected.IDs) || !reflect.DeepEqual(out.Rates, c.Expected.Rates) || !reflect.DeepEqual(out.Platforms, c.Expected.Platforms) {
				t.Fatalf("filters=%s\nids=%v want=%v\nrates=%v want=%v\nplatforms=%v want=%v", mustJSON(c.Filters), ids, c.Expected.IDs, out.Rates, c.Expected.Rates, out.Platforms, c.Expected.Platforms)
			}
		})
	}
}

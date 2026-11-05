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

func TestManagementNameExclusion(t *testing.T) {
	rows := []managementQueryRow{}
	for _, name := range []string{"Claude KiRo proxy", "Safe channel", "KIRO90", "literal.*", "literal12"} {
		rows = append(rows, managementQueryRow{ID: name, Account: subAccount{Name: "Kiro site"}, Target: subTarget{Name: name, Channel: "kiro", Platform: "kiro", Active: true}})
	}
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"kiro", []string{"Safe channel", "literal.*", "literal12"}},
		{" KIRO ", []string{"Safe channel", "literal.*", "literal12"}},
		{"literal.*", []string{"Claude KiRo proxy", "Safe channel", "KIRO90", "literal12"}},
		{"   ", []string{"Claude KiRo proxy", "Safe channel", "KIRO90", "literal.*", "literal12"}},
	} {
		out := filterManagementQueryRows(rows, managementQueryFilters{ExcludeName: c.query}, managementFilterEvidence{})
		ids := []string{}
		for _, row := range out.Rows {
			ids = append(ids, row.ID)
		}
		if !reflect.DeepEqual(ids, c.want) {
			t.Fatalf("exclude=%q ids=%v want=%v", c.query, ids, c.want)
		}
	}
}

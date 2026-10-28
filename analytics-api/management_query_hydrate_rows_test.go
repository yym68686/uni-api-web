package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func managementHydrationGroups(rows []managementQueryRow) map[string]bool {
	groups := map[string]bool{}
	for _, row := range rows {
		if row.Account.ID != "" {
			groups[qualityGroupID(row.Account.ID, row.Target.GroupID)] = true
		}
		if row.Configured != nil {
			for _, member := range row.Configured.Members {
				groups[qualityGroupID(member.AccountID, member.GroupID)] = true
			}
		}
	}
	return groups
}

func TestManagementHydratedRowsPreserveEveryPageContractAndInput(t *testing.T) {
	var cases []struct {
		Name    string               `json:"name"`
		Input   managementQueryInput `json:"input"`
		Model   string               `json:"model"`
		Account string               `json:"account"`
	}
	if err := json.Unmarshal(managementContractFixture(t, "testdata/management-query-rows.json.gz"), &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			before := mustJSON(c.Input)
			want, err := buildManagementQueryRows(c.Input, c.Model, c.Account)
			if err != nil {
				t.Fatal(err)
			}
			// Match hydration's existing last-row-wins replacement map, even
			// when one provider has multiple engines with the same public ID.
			wantByID := map[string]managementQueryRow{}
			for _, row := range want {
				wantByID[row.ID] = row
			}
			// Every singleton, and the entire set, exercise groups outside a
			// page as well as shared/cross-account alias members.
			pages := [][]managementQueryRow{want, {}}
			for _, row := range want {
				pages = append(pages, []managementQueryRow{row})
			}
			for _, page := range pages {
				got, err := buildHydratedManagementRows(c.Input, managementHydrationGroups(page), c.Model, c.Account)
				if err != nil {
					t.Fatal(err)
				}
				byID := map[string]managementQueryRow{}
				for _, row := range got {
					byID[row.ID] = row
				}
				for _, row := range page {
					if !reflect.DeepEqual(byID[row.ID], wantByID[row.ID]) {
						t.Fatalf("page replacement row %q changed", row.ID)
					}
				}
				if mustJSON(c.Input) != before {
					t.Fatal("page-only assembly mutated the complete input")
				}
			}
		})
	}
}

func TestManagementHydratedRowsRetainOffPageDecodeErrors(t *testing.T) {
	for _, kind := range []string{"model", "availability", "compaction", "tool-use"} {
		for _, raw := range []json.RawMessage{[]byte(`{"availability":false}`), []byte(`[]`), []byte(`{"checked_at":"bad"}`)} {
			in := managementQueryInput{
				Accounts: []subAccount{{ID: "account", Targets: []subTarget{{GroupID: 1, Models: []subModelResult{{Model: "extra"}}}}}},
				Channels: []managementChannel{{subInstalledChannel: subInstalledChannel{SourceID: "source", Provider: "off-page", Models: []string{"extra"}}, Engine: "codex"}},
				Checks:   []configuredCheck{{Source: "source", Provider: "off-page", Kind: kind, Model: "extra", Result: raw}},
			}
			_, want := buildManagementQueryRows(in, "", "")
			_, got := buildHydratedManagementRows(in, map[string]bool{}, "", "")
			if (want == nil) != (got == nil) || want != nil && want.Error() != got.Error() {
				t.Fatalf("off-page %s decode changed: got %v want %v", kind, got, want)
			}
		}
	}
}

var managementHydrationRowsSink []managementQueryRow

func BenchmarkManagementHydratedRows(b *testing.B) {
	// Synthetic catalog of production-like size; this is final assembly only,
	// not a database, HTTP or end-to-end latency measurement.
	in := managementQueryInput{}
	groups := map[string]bool{}
	for i := 0; i < 49; i++ {
		a := subAccount{ID: fmt.Sprint(i)}
		for j := 0; j < 15; j++ {
			t := subTarget{GroupID: int64(j), Models: []subModelResult{}}
			for k := 0; k < 258; k++ {
				t.Models = append(t.Models, subModelResult{Model: fmt.Sprint(k), State: "done"})
			}
			if len(groups) < managementPageSize {
				groups[qualityGroupID(a.ID, t.GroupID)] = true
			}
			a.Targets = append(a.Targets, t)
		}
		in.Accounts = append(in.Accounts, a)
	}
	for _, optimized := range []bool{false, true} {
		b.Run(fmt.Sprint(optimized), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var err error
				if optimized {
					managementHydrationRowsSink, err = buildHydratedManagementRows(in, groups, "", "")
				} else {
					managementHydrationRowsSink, err = buildManagementQueryRows(in, "", "")
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

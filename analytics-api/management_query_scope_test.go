package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

type managementEmptyFallbackComparison struct {
	controlQueryDB
	legacy bool
	args   int
}

func (d *managementEmptyFallbackComparison) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.HasPrefix(query, managementModelStreamSQL) {
		d.args = len(args)
		if d.legacy && len(args) == 2 {
			query = managementModelStreamSQL + ` AND (p.model=ANY($2::text[]) OR EXISTS(SELECT 1 FROM jsonb_to_recordset($3::jsonb) AS g(account_id text,group_id bigint) WHERE g.account_id=p.account_id AND g.group_id=p.group_id))`
			args = append(args, "[]")
		}
	}
	return d.controlQueryDB.QueryContext(ctx, query, args...)
}

func TestManagementEmptyFallbackMatchesLegacyScopedRows(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	compare := func(model string, names []string, expectedArgs int) {
		t.Helper()
		for _, owner := range []string{"owner", "foreign"} {
			tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				legacy := &managementEmptyFallbackComparison{controlQueryDB: tx, legacy: true}
				current := &managementEmptyFallbackComparison{controlQueryDB: tx}
				scope := managementStreamScope{Model: model, Names: names, CompactRevision: true}
				want, wantIDs, err := readManagementStreamModels(ctx, legacy, owner, true, scope)
				if err != nil {
					t.Fatal(err)
				}
				got, gotIDs, err := readManagementStreamModels(ctx, current, owner, true, scope)
				if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotIDs, wantIDs) {
					t.Fatal("empty fallback optimization changed complete model or receipt inputs", err)
				}
				expected := expectedArgs
				if owner == "foreign" {
					expected = 2 // No owned groups can need the missing-model fallback.
				}
				if current.args != expected {
					t.Fatalf("empty fallback still retained a JSON subplan: got=%d want=%d", current.args, expected)
				}
			}()
		}
	}
	compare("gpt-5.5", []string{"gpt-5.5", "extra-model", "gpt-5.5"}, 2)
	compare("gpt-5.5", []string{}, 2)
	compare("gpt-5.5", nil, 2)
	compare("extra-model", []string{"extra-model", "gpt-6-astra"}, 2)
	if _, err := s.control.db.Exec(`DELETE FROM console_sub_models WHERE group_id=61 AND model='extra-model'`); err != nil {
		t.Fatal(err)
	}
	compare("extra-model", []string{"extra-model", "gpt-6-astra"}, 3)
}

func TestManagementScopedPredicatesPreserveBrowserFilterContract(t *testing.T) {
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
	if err := json.Unmarshal(managementContractFixture(t, "testdata/management-query-filters.json.gz"), &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		projected := projectManagementFixture(fixture.Input)
		if c.Filters.Model != "" {
			bindings := append([]subInstalledChannel{}, projected.Imports...)
			for _, channel := range projected.Channels {
				bindings = append(bindings, channel.subInstalledChannel)
			}
			names := managementScopeModelNames(c.Filters.Model, nil, bindings)
			catalog := []managementModelSignals{}
			for _, account := range projected.Accounts {
				for _, target := range account.Targets {
					for _, model := range target.Models {
						catalog = append(catalog, managementModelSignals{Account: account.ID, Group: target.GroupID, Model: model})
					}
				}
			}
			fallback := map[string]bool{}
			for _, g := range managementScopeFallbackGroups(c.Filters.Model, catalog) {
				fallback[qualityGroupID(g.AccountID, g.GroupID)] = true
			}
			for ai := range projected.Accounts {
				for ti := range projected.Accounts[ai].Targets {
					target := &projected.Accounts[ai].Targets[ti]
					for mi := range target.Models {
						m := &target.Models[mi]
						if !fallback[qualityGroupID(projected.Accounts[ai].ID, target.GroupID)] && !slices.Contains(names, m.Model) {
							m.Result = nil
						}
					}
				}
			}
		}
		rows, err := buildManagementQueryRows(projected, c.Filters.Model, c.Filters.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		filtered := filterManagementQueryRows(rows, c.Filters, c.Evidence)
		ids := []string{}
		for _, row := range filtered.Rows {
			ids = append(ids, row.ID)
		}
		if !reflect.DeepEqual(ids, c.Expected.IDs) || !reflect.DeepEqual(filtered.Rates, c.Expected.Rates) || !reflect.DeepEqual(filtered.Platforms, c.Expected.Platforms) {
			t.Fatalf("predicate projection changed filters %s: ids=%v want=%v", mustJSON(c.Filters), ids, c.Expected.IDs)
		}
		fullRows, err := buildManagementQueryRows(fixture.Input, c.Filters.Model, c.Filters.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		fullPage, err := buildManagementQueryPageFromRows(fixture.Input, fullRows, c.Filters, c.Evidence, "owner", []byte("key"), "", time.Unix(1, 0))
		if err != nil {
			t.Fatal(err)
		}
		page, err := buildManagementQueryPageFromRows(projected, rows, c.Filters, c.Evidence, "owner", []byte("key"), "", time.Unix(1, 0))
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != fullPage.Total || page.EligibleCount != fullPage.EligibleCount || !reflect.DeepEqual(page.Models, fullPage.Models) || !reflect.DeepEqual(page.DetectionModels, fullPage.DetectionModels) {
			t.Fatal("compact predicates truncated facet or full-selection scope")
		}
	}
}

func TestManagementScopeIncludesAliasesAndFallsBackForMissingModel(t *testing.T) {
	facts := []managementGatewayFacts{{Catalog: []managementModel{{Model: "alias", Upstream: "upstream-a"}}, Routes: map[string][]managementModel{"caller": {{Model: "alias", Upstream: "upstream-b"}}}}}
	names := managementScopeModelNames("alias", facts, []subInstalledChannel{{ModelMappings: map[string]string{"alias": "upstream-c"}}})
	if !reflect.DeepEqual(names, []string{"alias", "gpt-6-astra", "upstream-a", "upstream-b", "upstream-c"}) {
		t.Fatal(names)
	}
	catalog := []managementModelSignals{{Account: "a", Group: 1, Model: subModelResult{Model: "alias"}}, {Account: "a", Group: 2, Model: subModelResult{Model: "different"}}}
	if got := managementScopeFallbackGroups("alias", catalog); !reflect.DeepEqual(got, []subBoundKey{{AccountID: "a", GroupID: 2}}) {
		t.Fatal("missing-model fallback changed", got)
	}
}

func TestManagementScopedReadKeepsGlobalEvidenceFenceAndTypeErrors(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	db := s.control.db
	read := func() ([]managementModelSignals, string, error) {
		return readManagementScopedSignals(ctx, db, "owner", "gpt-5.5", []string{"gpt-5.5", "gpt-6-astra"})
	}
	rows, revision, err := read()
	if err != nil {
		t.Fatal(err)
	}
	fullRevision, err := readManagementModelRevisionFrom(ctx, db, "owner")
	if err != nil || revision != fullRevision {
		t.Fatal("global fence was narrowed", err)
	}
	present := 0
	for _, item := range rows {
		if item.Model.Result != nil {
			present++
		}
		if item.Model.Model == "extra-model" && item.Model.Result != nil {
			t.Fatal("unneeded predicate read")
		}
	}
	if len(rows) != 183 || present != 122 {
		t.Fatal("model directory or scope incomplete", len(rows), present)
	}
	if _, err := db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','888') WHERE model='extra-model'`); err != nil {
		t.Fatal(err)
	}
	_, changed, err := read()
	if err != nil || changed == revision {
		t.Fatal("off-scope change lost fence", err)
	}
	if _, err := db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','"invalid"') WHERE model='extra-model'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := read(); !errors.Is(err, errManagementPredicateFallback) {
		t.Fatal("off-scope malformed evidence was hidden", err)
	}
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	if _, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false); err == nil {
		t.Fatal("partial page published after malformed full evidence")
	}
}

func TestManagementScopePreservesAbsentSelectedModelFilterBehavior(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`DELETE FROM console_sub_models WHERE group_id=61 AND model='extra-model';UPDATE console_sub_models SET result=jsonb_set(result,'{availability,model_match}','"mismatch"') WHERE group_id=61 AND model='gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	filters := managementQueryFilters{GroupStatus: "all", Model: "extra-model", ModelMatch: "mismatch"}
	page, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if page.Page.Total != 1 || page.Page.Rows[0].Target.GroupID != 61 || page.Page.Revision != full.Page.Revision || !reflect.DeepEqual(page.Page.Rows, full.Page.Rows) {
		t.Fatal("missing selected model no longer uses existing all-check filter semantics")
	}
}

func TestManagementScopeReadsEveryCurrentAliasUpstream(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	facts := []managementGatewayFacts{{Catalog: []managementModel{{Model: "gpt-5.5", Upstream: "extra-model"}}}}
	names := managementScopeModelNames("gpt-5.5", facts, nil)
	rows, _, err := readManagementScopedSignals(ctx, s.control.db, "owner", "gpt-5.5", names)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Model.Model == "extra-model" && (row.Model.Result == nil || row.Model.Result.Availability.Status != "success") {
			t.Fatal("current upstream alias evidence was omitted")
		}
	}
}

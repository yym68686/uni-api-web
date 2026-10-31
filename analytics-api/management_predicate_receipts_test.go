package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"reflect"
	"strings"
	"testing"
)

func TestManagementDeferredPredicateReceiptsAvoidDatabaseAccess(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before, err := managementReceiptAccessSnapshot(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	reuse := &managementReceiptReuse{deferModelUsage: true}
	got, revision, err := readManagementScopedStreams(ctx, tx, "owner", true, managementStreamScope{ReceiptReuse: reuse})
	if err != nil || len(got) != 183 || len(reuse.receipts) != 0 || reuse.current(tx, "owner") == nil {
		t.Fatal("complete page predicate proof did not defer model receipts", len(got), err)
	}
	after, err := managementReceiptAccessSnapshot(ctx, tx)
	if err != nil || after != before {
		t.Fatal("deferred model predicates still accessed receipt storage", before, after, err)
	}
	// Predicate results deliberately contain no private probe IDs. Keeping the
	// ordinary account/capability attachment cannot repeat this deferred read.
	models := []subModelResult{}
	for _, item := range got {
		models = append(models, item.Model)
	}
	if probes := subUsageProbes([]subAccount{{Targets: []subTarget{{Models: models}}}}); len(probes) != 0 {
		t.Fatal("predicate models reintroduced private receipt lookups")
	}
	full, fullRevision, err := readManagementSignalStreams(ctx, tx, "owner", true)
	if err != nil || revision != fullRevision {
		t.Fatal("deferral changed the complete revision", err)
	}
	accessed, err := managementReceiptAccessSnapshot(ctx, tx)
	if err != nil || accessed.heapFetch <= after.heapFetch {
		t.Fatal("baseline did not read the fixture's receipt rows", err)
	}
	for i := range full {
		if full[i].Model.Result == nil || full[i].Model.Result.Availability.Usage == nil {
			t.Fatal("baseline lost current receipt evidence")
		}
		full[i].Model.Result.Availability.Usage = nil
	}
	if !reflect.DeepEqual(got, full) {
		t.Fatal("deferral changed a non-usage model predicate")
	}
	t.Logf("predicate receipt heap accesses: deferred=%d baseline=%d", after.heapFetch-before.heapFetch, accessed.heapFetch-after.heapFetch)
}

func TestManagementDeferredReceiptsKeepCompletePageAndPriceFilters(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	for _, f := range []managementQueryFilters{
		{GroupStatus: "all"},
		{GroupStatus: "all", Model: "gpt-5.5"},
		{GroupStatus: "all", Model: "extra-model", Sort: "asc"},
		{GroupStatus: "all", Model: "gpt-5.5", Availability: "success", ModelMatch: "match"},
		{GroupStatus: "all", Model: "gpt-5.5", Platform: "openai", AccountID: "a", Search: "G1"},
		{GroupStatus: "all", Model: "gpt-5.5", PriceStatus: "normal"},
		{GroupStatus: "all", Model: "gpt-5.5", PriceStatus: "abnormal"},
		{GroupStatus: "all", Model: "gpt-5.5", PriceStatus: "unconfirmed"},
	} {
		output.Reset()
		page, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
		if err != nil || !page.View.ProjectedModels {
			t.Fatal("page failed", f, err)
		}
		deferred := strings.Contains(output.String(), "management_signal_receipts deferred=true ")
		catalogOnly := f.Model == "" && f.Availability == "" && f.PriceStatus == "" && f.ModelMatch == "" && f.ToolUse == "" && f.Quality == ""
		if deferred != (f.PriceStatus == "" && !catalogOnly) {
			t.Fatal("model receipt deferral used the wrong filter policy", f, output.String())
		}
		full, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", true)
		if err != nil || page.Page.Revision != full.Page.Revision || page.Page.Total != full.Page.Total || page.Page.EligibleCount != full.Page.EligibleCount ||
			!reflect.DeepEqual(page.Page.Rows, full.Page.Rows) || !reflect.DeepEqual(page.Page.Rates, full.Page.Rates) || !reflect.DeepEqual(page.Page.Platforms, full.Page.Platforms) ||
			!reflect.DeepEqual(page.Evidence, full.Evidence) || !reflect.DeepEqual(managementContextForPage(page.View, page.Page, f.Model), managementContextForPage(full.View, full.Page, f.Model)) {
			t.Fatal("deferral changed complete page, price filtering, facets or selection revision", f, err)
		}
	}
}

func TestManagementDeferredReceiptsRequireCompleteCompactProof(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`UPDATE console_management_revision_state SET complete=false WHERE relation_name='console_sub_usage'`); err != nil {
		t.Fatal(err)
	}
	reuse := &managementReceiptReuse{deferModelUsage: true}
	rows, _, err := readManagementScopedStreams(context.Background(), s.control.db, "owner", true, managementStreamScope{ReceiptReuse: reuse})
	if err != nil || len(rows) != 183 || len(reuse.receipts) != 366 {
		t.Fatal("incomplete compact proof deferred the original receipt read", len(rows), len(reuse.receipts), err)
	}
	for _, row := range rows {
		if row.Model.Result == nil || row.Model.Result.Availability.Usage == nil || *row.Model.Result.Availability.Usage.InputPrice != 2 {
			t.Fatal("legacy predicate reader lost current receipt evidence")
		}
	}
}

func TestManagementNonPriceFiltersIgnoreModelReceiptValues(t *testing.T) {
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
	checked := 0
	inputJSON := mustJSON(fixture.Input)
	for _, tc := range fixture.Cases {
		if tc.Filters.PriceStatus != "" {
			continue
		}
		var input managementQueryInput
		if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
			t.Fatal(err)
		}
		rows, err := buildManagementQueryRows(input, tc.Filters.Model, tc.Filters.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		before := filterManagementQueryRows(rows, tc.Filters, tc.Evidence)
		ids := func(rows []managementQueryRow) []string {
			out := []string{}
			for _, row := range rows {
				out = append(out, row.ID)
			}
			return out
		}
		wantIDs := ids(before.Rows)
		// Builders return aliases to request-local model results; change only
		// receipt values, including embedded ones, leaving every other signal.
		for i := range rows {
			for j := range rows[i].Checks {
				if result := rows[i].Checks[j].Result; result != nil {
					result.Availability.Usage = &subUsage{Status: "pending", InputPrice: usageFloat(999), InputTokens: usageInt(999999)}
				}
			}
		}
		after := filterManagementQueryRows(rows, tc.Filters, tc.Evidence)
		if !reflect.DeepEqual(wantIDs, ids(after.Rows)) || !reflect.DeepEqual(before.Rates, after.Rates) || !reflect.DeepEqual(before.Platforms, after.Platforms) {
			t.Fatal("a non-price filter consumed model receipt values", tc.Filters)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no browser filter cases covered")
	}
	t.Logf("non-price browser filter cases checked: %d", checked)
}

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestManagementReceiptRawScopeIsDiagnosticAndSnapshotLocal(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	tx := &sql.Tx{}
	reuse := &managementReceiptReuse{}
	reuse.capture(tx, "private-owner", nil)
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "raw-scope-123")
	for _, test := range []struct {
		ctx   context.Context
		db    controlQueryDB
		owner string
	}{
		{context.Background(), tx, "private-owner"},
		{ctx, &sql.Tx{}, "private-owner"},
		{ctx, (*sql.Tx)(nil), "private-owner"},
		{ctx, &sql.DB{}, "private-owner"},
		{ctx, tx, "different-private-owner"},
	} {
		if got := reuse.traceRawIDs(test.ctx, test.db, test.owner); got != test.ctx {
			t.Fatal("diagnostic crossed an untraced read, connection, transaction or owner")
		}
	}
	if got := (*managementReceiptReuse)(nil).traceRawIDs(ctx, tx, "private-owner"); got != ctx {
		t.Fatal("missing snapshot installed a callback")
	}
	accountCtx := reuse.traceRawIDs(context.WithValue(ctx, managementAccountReceiptsKey{}, true), tx, "private-owner")
	accountObserver, ok := accountCtx.Value(managementRawReceiptObserverKey{}).(func([]string))
	if !ok {
		t.Fatal("traced account snapshot has no observer")
	}
	ids := []string{"private-present", "private-absent"}
	accountObserver(ids)
	ids[0] = "caller-edited"
	pageCtx := reuse.traceRawIDs(ctx, tx, "private-owner")
	pageCtx.Value(managementRawReceiptObserverKey{}).(func([]string))([]string{"private-absent", "private-new"})
	if _, found := reuse.accountRawIDs["private-present"]; !found || len(reuse.accountRawIDs) != 2 || reuse.current(tx, "private-owner")("private-absent") != nil {
		t.Fatal("diagnostic aliases caller IDs or became missing-row lookup evidence")
	}
	for _, want := range []string{
		"management_receipt_raw_scope phase=account requested=2 prior_account=0 repeated=0 request_id=raw-scope-123",
		"management_receipt_raw_scope phase=page requested=2 prior_account=2 repeated=1 request_id=raw-scope-123",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatal("incorrect fixed overlap counts", want)
		}
	}
	encoded, _ := json.Marshal(reuse)
	if string(encoded) != "{}" || strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "caller-edited") {
		t.Fatal("diagnostic identities entered public JSON or logs")
	}
	reuse.capture(tx, "private-owner", nil)
	if reuse.accountRawIDs != nil {
		t.Fatal("recaptured evidence retained old diagnostics")
	}
}

func TestManagementReceiptRawScopeKeepsBothQueriesValuesAndErrors(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "raw-scope-db-123")
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reuse := &managementReceiptReuse{}
	reuse.capture(tx, "owner", nil)
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	accountIDs := []string{"a-1-gpt-5.5", "private-absent"}
	pageIDs := []string{"private-absent", "a-1-gpt-5.5", "q-1-extra-model"}
	for _, account := range []bool{true, false} {
		phase, ids, traceCtx := "page", pageIDs, ctx
		if account {
			phase, ids = "account", accountIDs
			traceCtx = context.WithValue(ctx, managementAccountReceiptsKey{}, true)
		}
		want, got := map[string]*subUsage{}, map[string]*subUsage{}
		if err := s.subReadUsageByIDsFrom(context.Background(), tx, "owner", ids, want); err != nil {
			t.Fatal(err)
		}
		recorder := &managementReceiptQueryRecorder{controlQueryDB: tx}
		traceCtx = reuse.traceRawIDs(traceCtx, tx, "owner")
		if err := s.subReadUsageByIDsFrom(traceCtx, recorder, "owner", ids, got); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("diagnostic changed raw receipt values", err)
		}
		if len(recorder.ids) != 1 || !slices.Equal(recorder.ids[0], ids) {
			t.Fatal("diagnostic skipped or changed requested IDs")
		}
		if !strings.Contains(output.String(), "management_rows_read stage="+phase+"_receipts ") {
			t.Fatal("account/page SQL still has an ambiguous stage", phase)
		}
	}
	if !strings.Contains(output.String(), "phase=page requested=3 prior_account=2 repeated=2 ") {
		t.Fatal("present and absent IDs were not both counted, without skipping either")
	}
	if strings.Contains(output.String(), "private-absent") || strings.Contains(output.String(), "gpt-5.5") || strings.Contains(output.String(), "extra-model") {
		t.Fatal("private IDs reached diagnostic output")
	}
	// A callback is not a query result: empty and failed reads cannot populate it.
	for _, cancel := range []bool{false, true} {
		calls := 0
		caseCtx := context.WithValue(ctx, managementRawReceiptObserverKey{}, func([]string) { calls++ })
		ids := []string{}
		if cancel {
			ids = pageIDs
			canceled, cancelRead := context.WithCancel(caseCtx)
			cancelRead()
			caseCtx = canceled
		}
		err := s.subReadUsageByIDsFrom(caseCtx, tx, "owner", ids, map[string]*subUsage{})
		if calls != 0 || cancel && !errors.Is(err, context.Canceled) || !cancel && err != nil {
			t.Fatal("empty/failed read reported a completed raw scope", err)
		}
	}
}

func TestManagementReceiptRawScopeDoesNotObserveInvalidReceipt(t *testing.T) {
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"input_tokens":"private-invalid"}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ids := []string{"a-1-gpt-5.5"}
	wantErr := s.subReadUsageByIDsFrom(ctx, s.control.db, "owner", ids, map[string]*subUsage{})
	calls := 0
	ctx = context.WithValue(ctx, managementRawReceiptObserverKey{}, func([]string) { calls++ })
	gotErr := s.subReadUsageByIDsFrom(ctx, s.control.db, "owner", ids, map[string]*subUsage{})
	if wantErr == nil || gotErr == nil || reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) || gotErr.Error() != wantErr.Error() || calls != 0 {
		t.Fatal("invalid receipt changed its typed error or reported a successful scope")
	}
}

func TestManagementReceiptRawScopeInstalledAcrossAccountAndPageReaders(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET result='{"model":"extra-model","availability":{"id":"a-1-extra-model","status":"success"},"quality":{"id":"private-absent","status":"success"}}' WHERE group_id=1`); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "raw-scope-full-123")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	snapshot, err := s.readManagementQuerySnapshot(ctx, "owner", managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}, "", false)
	if err != nil || len(snapshot.Page.Rows) != 25 || snapshot.View.receiptReuse != nil {
		t.Fatal("full snapshot failed or retained private diagnostic holder", err)
	}
	// Normalization removes the legacy target's private IDs before hydration.
	// The page reintroduces a-1-extra-model from its model row, not the absent
	// quality ID from legacy metadata. With no price predicate, all 150 model
	// receipts are first attached during hydration; account metadata retains its
	// original raw reader and request correlation.
	if result := snapshot.View.Input.Accounts[0].Targets[0].Result; result == nil || result.Availability.ID != "" || result.Quality.ID != "" {
		t.Fatal("fixture did not normalize legacy private IDs before hydration")
	}
	for _, want := range []string{"stage=account_receipts ", "phase=account requested=2 prior_account=0 repeated=0 ", "management_page_receipt_signals requested=150 resolved=150 request_id=raw-scope-full-123"} {
		if !strings.Contains(output.String(), want) {
			for _, line := range strings.Split(output.String(), "\n") {
				if strings.Contains(line, "management_receipt_raw_scope ") {
					t.Log(line)
				}
			}
			t.Fatal("actual readers did not install distinct correlated diagnostics", want)
		}
	}
	if strings.Contains(output.String(), "stage=page_receipts ") || strings.Contains(output.String(), "phase=page requested=") {
		t.Fatal("fully projected page still read raw receipts")
	}
	if strings.Contains(output.String(), "private-absent") || strings.Contains(mustJSON(snapshot), "accountRawIDs") {
		t.Fatal("diagnostic leaked private IDs or was retained")
	}
	// Catalog-only has no predicate-reuse holder, but its account SQL must
	// still receive the correct stage instead of being mislabeled page SQL.
	output.Reset()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := s.readManagementDatabaseModeWithReceiptsFrom(ctx, tx, "owner", nil, true, "gpt-5.5", true, &managementReceiptReuse{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "stage=account_receipts ") || strings.Contains(output.String(), "stage=page_receipts ") || strings.Contains(output.String(), "management_receipt_raw_scope ") {
		t.Fatal("catalog-only scope invented reuse evidence or kept ambiguous account stage")
	}
}

func BenchmarkManagementReceiptRawScope(b *testing.B) {
	previous := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(previous)
	accountIDs, pageIDs := make([]string, 384), make([]string, 7138)
	for i := range pageIDs {
		pageIDs[i] = fmt.Sprintf("fixture-%d", i)
		if i < len(accountIDs) {
			accountIDs[i] = pageIDs[i]
		}
	}
	tx := &sql.Tx{}
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "raw-scope-bench-123")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reuse := &managementReceiptReuse{}
		reuse.capture(tx, "owner", nil)
		accountCtx := reuse.traceRawIDs(context.WithValue(ctx, managementAccountReceiptsKey{}, true), tx, "owner")
		accountCtx.Value(managementRawReceiptObserverKey{}).(func([]string))(accountIDs)
		pageCtx := reuse.traceRawIDs(ctx, tx, "owner")
		pageCtx.Value(managementRawReceiptObserverKey{}).(func([]string))(pageIDs)
	}
}

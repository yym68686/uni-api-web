package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func receiptSetScopeForTest() (*managementReceiptSetScope, context.Context) {
	scope := &managementReceiptSetScope{store: &managementReceiptSets{}, tx: &sql.Tx{}, version: managementReceiptSetVersion{database: &sql.DB{}, owner: "private-owner", stamp: "proof"}}
	return scope, context.WithValue(context.Background(), managementReceiptSetScopeKey{}, scope)
}

func TestManagementReceiptSetCacheRequiresCompleteReceiptProof(t *testing.T) {
	proof := managementFreshProof{ExportStamp: "page", ReceiptStamp: "receipts", Epochs: map[string]string{"console_sub_accounts": "0", "console_sub_targets": "1", "console_sub_usage": "2", "console_sub_observed_rates": "3"}}
	store, database, tx := &managementReceiptSets{}, &sql.DB{}, &sql.Tx{}
	if managementReceiptSetScopeFromProof(store, database, tx, "owner", proof) == nil {
		t.Fatal("complete receipt proof rejected")
	}
	for relation, value := range proof.Epochs {
		delete(proof.Epochs, relation)
		if managementReceiptSetScopeFromProof(store, database, tx, "owner", proof) != nil {
			t.Fatal("incomplete source proof admitted", relation)
		}
		proof.Epochs[relation] = value
	}
	proof.ReceiptStamp = ""
	if managementReceiptSetScopeFromProof(store, database, tx, "owner", proof) != nil {
		t.Fatal("receipt proof missing")
	}
	proof.ReceiptStamp, proof.ExportStamp = "receipts", ""
	if managementReceiptSetScopeFromProof(store, database, tx, "owner", proof) != nil {
		t.Fatal("unprepared complete proof admitted")
	}
}

func TestManagementReceiptSetCacheSetScopeAndTypedIsolation(t *testing.T) {
	for _, name := range []string{"hit", "permutation", "strict", "ids", "transaction", "pool", "database", "owner", "proof", "empty_proof", "unverified", "no_scope", "empty", "large"} {
		t.Run(name, func(t *testing.T) {
			scope, ctx := receiptSetScopeForTest()
			enableManagementReceiptSetScope(ctx, scope.tx, true)
			price, tokens := math.SmallestNonzeroFloat64, int64(math.MaxInt64)
			want := map[string]managementStreamReceipt{"a": {valid: true, evidence: []byte{}, usage: managementStreamUsage{Status: "matched", InputPrice: &price, OutputTokens: &tokens}}, "b": {valid: false, evidence: nil}}
			calls := 0
			read := func() (map[string]managementStreamReceipt, error) { calls++; return want, nil }
			ids := []string{"a", "b", "absent"}
			if _, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, ids, false, read); err != nil {
				t.Fatal(err)
			}
			var db controlQueryDB = scope.tx
			owner, strict := scope.version.owner, false
			switch name {
			case "permutation":
				ids = []string{"absent", "b", "b", "a"}
			case "strict":
				strict = true
			case "ids":
				ids = []string{"a", "b", "another"}
			case "transaction":
				db = &sql.Tx{}
			case "pool":
				db = scope.version.database
			case "database":
				scope.version.database = &sql.DB{}
			case "owner":
				owner = "foreign"
			case "proof":
				scope.version.stamp = "next"
			case "empty_proof":
				scope.version.stamp = ""
			case "unverified":
				scope.complete.Store(false)
			case "no_scope":
				ctx = context.Background()
			case "empty":
				ids = nil
			case "large":
				ids = make([]string, 4097)
			}
			got, err := readManagementReceiptSet(ctx, db, owner, ids, strict, read)
			hit := name == "hit" || name == "permutation"
			if err != nil || !reflect.DeepEqual(got, want) || calls != map[bool]int{true: 1, false: 2}[hit] {
				t.Fatal("receipt set, strictness or snapshot scope changed", calls, err)
			}
			if hit {
				*got["a"].usage.InputPrice = 9
				*got["a"].usage.OutputTokens = 2
				delete(got, "b")
				price, tokens = 10, 3
				next, err := readManagementReceiptSet(ctx, db, owner, ids, strict, read)
				if err != nil || *next["a"].usage.InputPrice != math.SmallestNonzeroFloat64 || *next["a"].usage.OutputTokens != math.MaxInt64 || next["a"].evidence == nil || next["b"].evidence != nil || len(next) != 2 {
					t.Fatal("typed pointers, evidence or source alias escaped", err)
				}
			}
		})
	}
}

func TestManagementReceiptSetCacheRejectsErrorsAndKeepsNilEmpty(t *testing.T) {
	for _, name := range []string{"error", "fallback", "cancel", "oversize", "non_finite", "nil", "empty"} {
		t.Run(name, func(t *testing.T) {
			scope, ctx := receiptSetScopeForTest()
			enableManagementReceiptSetScope(ctx, scope.tx, true)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			failure := errors.New("fixture error")
			var want map[string]managementStreamReceipt
			read := func() (map[string]managementStreamReceipt, error) {
				want = map[string]managementStreamReceipt{}
				switch name {
				case "error":
					return nil, failure
				case "fallback":
					return nil, errManagementPredicateFallback
				case "cancel":
					cancel()
				case "oversize":
					want[strings.Repeat("x", managementReceiptSetBudget)] = managementStreamReceipt{}
				case "non_finite":
					p := math.NaN()
					want["a"] = managementStreamReceipt{usage: managementStreamUsage{InputPrice: &p}}
				case "nil":
					want = nil
				}
				return want, nil
			}
			got, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, []string{"a"}, true, read)
			if name == "error" && !errors.Is(err, failure) || name == "fallback" && !errors.Is(err, errManagementPredicateFallback) || name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("read error changed", err)
			}
			if name != "nil" && name != "empty" && len(scope.store.entries) != 0 {
				t.Fatal("unsuccessful or unbounded read retained")
			}
			if name == "nil" || name == "empty" {
				next, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, []string{"a"}, true, read)
				if err != nil || !reflect.DeepEqual(next, got) || !reflect.DeepEqual(next, want) {
					t.Fatal("nil/empty map changed", err)
				}
				cancel()
				if _, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, []string{"a"}, true, read); !errors.Is(err, context.Canceled) {
					t.Fatal("hit bypassed cancellation", err)
				}
			}
		})
	}
}

func TestManagementReceiptSetCacheConcurrentBudget(t *testing.T) {
	store := &managementReceiptSets{}
	database := &sql.DB{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				scope, _ := receiptSetScopeForTest()
				scope.store, scope.version.database, scope.version.owner, scope.version.stamp = store, database, fmt.Sprint(i), fmt.Sprint(n)
				ctx := context.WithValue(context.Background(), managementReceiptSetScopeKey{}, scope)
				enableManagementReceiptSetScope(ctx, scope.tx, true)
				want := map[string]managementStreamReceipt{strings.Repeat("m", 300000): {valid: true}}
				got, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, []string{"a"}, true, func() (map[string]managementStreamReceipt, error) { return want, nil })
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Error("concurrent output changed", err)
				}
			}
		}(i)
	}
	wg.Wait()
	if len(store.entries) > 8 || store.bytes > managementReceiptSetBudget {
		t.Fatal("cache exceeded memory or entry limit")
	}
	total := 0
	for _, entry := range store.entries {
		total += entry.bytes
		if cap(entry.body) != len(entry.body) {
			t.Fatal("retained byte capacity exceeds accounting")
		}
		if cap(entry.absent) != len(entry.absent) {
			t.Fatal("retained absence capacity exceeds accounting")
		}
	}
	if store.bytes != total {
		t.Fatal("eviction byte accounting changed")
	}
}

func TestManagementReceiptSetCacheCurrentProofAndOldSnapshot(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	prepareManagementRevisionsForTest(t, db)
	ctx := managementTraceContext(context.Background())
	ids := []string{"a-1-gpt-5.5", "q-1-gpt-5.5", "absent"}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	read := func(tx *sql.Tx) map[string]managementStreamReceipt {
		t.Helper()
		proof, _, err := s.managementReadProof(ctx, tx, "owner")
		if err != nil {
			t.Fatal(err)
		}
		scope := managementReceiptSetScopeFromProof(&s.receiptSets, db, tx, "owner", proof)
		var complete bool
		if err := tx.QueryRowContext(ctx, `SELECT complete FROM console_management_projection_state WHERE singleton`).Scan(&complete); err != nil {
			t.Fatal(err)
		}
		enableManagementReceiptSetScope(context.WithValue(ctx, managementReceiptSetScopeKey{}, scope), tx, complete)
		got, err := readManagementStreamReceipts(context.WithValue(ctx, managementReceiptSetScopeKey{}, scope), tx, "owner", ids, false)
		want, wantErr := readManagementStreamReceiptsUncached(context.Background(), tx, "owner", ids, false)
		if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("cached receipt projection differs from current SQL", err, wantErr)
		}
		return got
	}
	fresh := func() map[string]managementStreamReceipt {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		return read(tx)
	}
	first := fresh()
	if !strings.Contains(output.String(), "hit=false stored=true") {
		t.Fatal("complete selected-ID read not retained", output.String())
	}
	output.Reset()
	if got := fresh(); !reflect.DeepEqual(got, first) || !strings.Contains(output.String(), "management_receipt_set_cache hit=true") || strings.Contains(output.String(), "stage=receipt_signal_rows ") {
		t.Fatal("unchanged generation did not bypass receipt SQL", output.String())
	}
	old, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback()
	before := read(old)
	for _, query := range []string{
		`UPDATE console_management_projection_state SET phase='verify_models',complete=false WHERE singleton`,
		`UPDATE console_management_projection_state SET phase='complete',complete=true WHERE singleton`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{input_price}','9') WHERE id='a-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{output_tokens}','9223372036854775807') WHERE id='a-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET status='pending' WHERE id='a-1-gpt-5.5'`,
		`UPDATE console_management_receipt_signal SET evidence_digest=decode('ab','hex') WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_accounts SET owner='foreign' WHERE id='a'`,
		`UPDATE console_sub_accounts SET owner='owner' WHERE id='a'`,
		`UPDATE console_sub_usage SET result='{"input_tokens":1.0}' WHERE id='a-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET result='{"Status":"matched"}' WHERE id='a-1-gpt-5.5'`,
		`DELETE FROM console_sub_usage WHERE id='a-1-gpt-5.5'`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		output.Reset()
		fresh()
		if query != `UPDATE console_management_projection_state SET phase='complete',complete=true WHERE singleton` && strings.Contains(output.String(), "management_receipt_set_cache hit=true") {
			t.Fatal("changed or unsupported receipt reused prior evidence", query)
		}
	}
	if after := read(old); !reflect.DeepEqual(after, before) {
		t.Fatal("new writes contaminated pinned old snapshot")
	}
}

func TestManagementReceiptSetCachePreservesPageAndRawFallback(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	f := managementQueryFilters{GroupStatus: "available", Model: "gpt-5.5"}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for n := 0; n < 2; n++ {
		output.Reset()
		got, err := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", false)
		want, wantErr := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", true)
		got.Page.ReadAtMS = want.Page.ReadAtMS
		if err != nil || wantErr != nil || !reflect.DeepEqual(got.Page, want.Page) || !reflect.DeepEqual(managementContextForPage(got.View, got.Page, f.Model), managementContextForPage(want.View, want.Page, f.Model)) {
			t.Fatal("complete page or add context changed", err, wantErr)
		}
		if n == 1 && (!strings.Contains(output.String(), "management_receipt_set_cache hit=true") || !strings.Contains(output.String(), "stage=account_receipts ")) {
			t.Fatal("hit did not retain raw fallback for missing IDs", output.String())
		}
	}
	if len(s.receiptSets.entries) == 0 {
		t.Fatal("normal management snapshot did not install receipt scope")
	}
}

func TestManagementReceiptSetCacheIgnoresUnrelatedPageInputs(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	prepareManagementRevisionsForTest(t, db)
	ctx := managementTraceContext(context.Background())
	ids := []string{"a-1-gpt-5.5", "q-1-gpt-5.5", "absent"}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	read := func() (managementFreshProof, map[string]managementStreamReceipt) {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		proof, _, err := s.managementReadProof(ctx, tx, "owner")
		if err != nil || proof.ExportStamp == "" {
			t.Fatal("fixture has no current proof", err)
		}
		scope := managementReceiptSetScopeFromProof(&s.receiptSets, db, tx, "owner", proof)
		readCtx := context.WithValue(ctx, managementReceiptSetScopeKey{}, scope)
		var complete bool
		if err := tx.QueryRowContext(ctx, `SELECT complete FROM console_management_projection_state WHERE singleton`).Scan(&complete); err != nil {
			t.Fatal(err)
		}
		enableManagementReceiptSetScope(readCtx, tx, complete)
		got, err := readManagementStreamReceipts(readCtx, tx, "owner", ids, false)
		want, wantErr := readManagementStreamReceiptsUncached(context.Background(), tx, "owner", ids, false)
		if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("selected receipts differ from current transaction SQL", err, wantErr)
		}
		return proof, got
	}
	before, receipts := read()
	for _, query := range []string{
		`UPDATE console_sub_models SET message='changed page diagnostic' WHERE group_id=1 AND model='gpt-5.5'`,
		`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','456') WHERE group_id=1 AND model='gpt-5.5'`,
		`UPDATE console_sub_targets SET name='changed target name' WHERE group_id=1`,
		`INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES('a',1,'model','gpt-5.5')`,
		`INSERT INTO ` + s.engine.historyPath + `.prices(model,input,output) VALUES('gpt-5.5',9,10) ON CONFLICT(model) DO UPDATE SET input=EXCLUDED.input,output=EXCLUDED.output`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		output.Reset()
		after, got := read()
		if before.ExportStamp == after.ExportStamp {
			t.Fatal("unrelated page change must still invalidate the complete page proof", query)
		}
		if !reflect.DeepEqual(got, receipts) {
			t.Fatal("unrelated page input changed the selected receipt SQL", query)
		}
		if !strings.Contains(output.String(), "management_receipt_set_cache hit=true") || strings.Contains(output.String(), "stage=receipt_signal_rows ") {
			t.Fatal("unrelated page change repeated the selected receipt SQL", query, output.String())
		}
		before = after
	}
}

func TestManagementPagingReusesVerifiedReceiptSet(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	filters := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	first, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
	if err != nil || first.Page.NextCursor == "" {
		t.Fatal("paging fixture did not produce a next page", err)
	}
	s.rememberManagementPagingPlan("owner", filters, first)

	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)

	page, ok, err := s.currentManagementPagingPage(ctx, "owner", filters, first.Page.NextCursor, false)
	if err != nil || !ok {
		t.Fatal("first verified page read failed", err)
	}
	if !strings.Contains(output.String(), "management_receipt_set_cache hit=") {
		t.Fatal("verified paging did not install the receipt cache scope", output.String())
	}
	output.Reset()

	repeated, ok, err := s.currentManagementPagingPage(ctx, "owner", filters, first.Page.NextCursor, false)
	if err != nil || !ok {
		t.Fatal("repeated verified page read failed", err)
	}
	repeated.ReadAtMS = page.ReadAtMS
	if mustJSON(page) != mustJSON(repeated) {
		t.Fatal("receipt reuse changed page output")
	}
	if !strings.Contains(output.String(), "management_receipt_set_cache hit=true") || strings.Contains(output.String(), "stage=receipt_signal_rows ") {
		t.Fatal("repeated verified page did not bypass the selected-ID SQL", output.String())
	}
	if _, err := s.control.db.Exec(`UPDATE console_management_projection_state SET phase='verify_models',complete=false WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if _, _, err := s.currentManagementPagingPage(ctx, "owner", filters, first.Page.NextCursor, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "management_receipt_set_cache hit=true") {
		t.Fatal("incomplete projection reused cached receipt evidence", output.String())
	}
}

func TestManagementPagingReusesWideReceiptBatches(t *testing.T) {
	s := managementHydrationFixture(t)
	// Each 25-group page needs more than one selected-ID batch. A missing
	// receipt reaches the raw decoder before its certified absence is reused.
	for _, query := range []string{
		`INSERT INTO console_sub_models(account_id,group_id,model,state,result)
 SELECT 'a',g,m,'done',jsonb_build_object('model',m,'checked_at',g,'verdict','pass',
 'availability',jsonb_build_object('id','dense-a-'||g||'-'||m,'status','success','model_match','match'),
 'quality',jsonb_build_object('id','dense-q-'||g||'-'||m,'status','success'))
 FROM generate_series(1,61) g CROSS JOIN (SELECT 'wide-'||lpad(n::text,3,'0') m FROM generate_series(1,100) n) models`,
		`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result)
 SELECT prefix||g||'-'||m,'a',g,g,m,0,'[]','matched','{"input_price":2,"output_price":3}'
 FROM generate_series(1,61) g CROSS JOIN (SELECT 'wide-'||lpad(n::text,3,'0') m FROM generate_series(1,100) n) models
 CROSS JOIN unnest(ARRAY['dense-a-','dense-q-']) prefix
 WHERE NOT (g=26 AND m='wide-001' AND prefix='dense-a-')`,
	} {
		if _, err := s.control.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	first, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil || first.Page.NextCursor == "" {
		t.Fatal("wide fixture did not produce a next page", err)
	}
	s.rememberManagementPagingPlan("owner", f, first)
	want, err := s.readManagementQuerySnapshot(ctx, "owner", f, first.Page.NextCursor, true)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for n := 0; n < 3; n++ {
		output.Reset()
		start := time.Now()
		got, ok, err := s.currentManagementPagingPage(ctx, "owner", f, first.Page.NextCursor, false)
		elapsed := time.Since(start)
		got.ReadAtMS = want.Page.ReadAtMS
		if err != nil || !ok || mustJSON(got.Rows) != mustJSON(want.Page.Rows) || mustJSON(got.Context) != mustJSON(managementContextForPage(want.View, want.Page, f.Model)) || got.Revision != want.Page.Revision {
			t.Fatal("wide paging changed complete rows, context or revision", err)
		}
		logs := output.String()
		t.Logf("local_wide_paging pass=%d elapsed_ms=%d hits=%d sql_reads=%d", n, elapsed.Milliseconds(), strings.Count(logs, "management_receipt_set_cache hit=true"), strings.Count(logs, "stage=receipt_signal_rows "))
		if !strings.Contains(logs, "management_page_receipt_signals requested=5150 resolved=5149") {
			t.Fatal("wide fixture lost its second batch or fabricated a missing receipt")
		}
		if n == 0 && !strings.Contains(logs, "stage=page_receipts ") {
			t.Fatal("unknown raw receipt bypassed the original decoder")
		}
		if n > 0 && (strings.Contains(logs, "stage=page_receipts ") || !strings.Contains(logs, "management_raw_receipt_absence reused=1 raw_ids=0 stored=false phase=page ")) {
			t.Fatal("verified raw absence did not replace only the proven empty SQL")
		}
		if n > 0 && (strings.Count(logs, "management_receipt_set_cache hit=true") != 2 || strings.Contains(logs, "stage=receipt_signal_rows ")) {
			t.Fatalf("unchanged wide page repeated receipt SQL: pass=%d hits=%d sql_reads=%d", n, strings.Count(logs, "management_receipt_set_cache hit=true"), strings.Count(logs, "stage=receipt_signal_rows "))
		}
	}
}

func TestManagementReceiptSetCacheLocalMeasurement(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	proof, _, err := s.managementReadProof(ctx, tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	cached := context.WithValue(ctx, managementReceiptSetScopeKey{}, managementReceiptSetScopeFromProof(&s.receiptSets, s.control.db, tx, "owner", proof))
	enableManagementReceiptSetScope(cached, tx, true)
	var ids []string
	for group := 1; group <= 61; group++ {
		for _, model := range []string{"gpt-5.5", "gpt-6-astra", "extra-model"} {
			ids = append(ids, fmt.Sprintf("a-%d-%s", group, model), fmt.Sprintf("q-%d-%s", group, model))
		}
	}
	if _, err := readManagementStreamReceipts(cached, tx, "owner", ids, true); err != nil {
		t.Fatal(err)
	}
	for _, reuse := range []bool{false, true} {
		readCtx := ctx
		if reuse {
			readCtx = cached
		}
		start := time.Now()
		for n := 0; n < 10; n++ {
			if _, err := readManagementStreamReceipts(readCtx, tx, "owner", ids, true); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("local_receipt_set_read cache=%t mean_ms=%.3f ids=%d", reuse, float64(time.Since(start).Microseconds())/10000, len(ids))
	}
}

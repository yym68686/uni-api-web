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

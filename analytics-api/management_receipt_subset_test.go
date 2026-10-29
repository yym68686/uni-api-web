package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestManagementReceiptSubsetReadsOnlyUnverifiedIDs(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := managementTraceContext(context.Background())
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	proof, _, err := s.managementReadProof(ctx, tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	scope := managementReceiptSetScopeFromProof(&s.receiptSets, s.control.db, tx, "owner", proof)
	ctx = context.WithValue(ctx, managementReceiptSetScopeKey{}, scope)
	enableManagementReceiptSetScope(ctx, tx, true)
	if _, err := readManagementStreamReceipts(ctx, tx, "owner", []string{"a-1-gpt-5.5", "q-1-gpt-5.5", "absent"}, false); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for _, ids := range [][]string{
		{"q-1-gpt-5.5", "a-2-gpt-5.5", "a-1-gpt-5.5", "absent", "a-1-gpt-5.5"},
		{"a-2-gpt-5.5", "q-1-gpt-5.5"},
	} {
		output.Reset()
		got, err := readManagementStreamReceipts(ctx, tx, "owner", ids, true)
		want, wantErr := readManagementStreamReceiptsUncached(context.Background(), tx, "owner", ids, true)
		if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("partial reuse changed exact current SQL results", err, wantErr)
		}
		if !strings.Contains(output.String(), "management_receipt_subset_cache reused=2 ") {
			t.Fatal("different selected-ID set reread already verified receipts", output.String())
		}
		if len(ids) == 2 && strings.Contains(output.String(), "stage=receipt_signal_rows ") {
			t.Fatal("fully verified subset still queried SQL", output.String())
		}
		if len(ids) > 2 && !strings.Contains(output.String(), "raw_ids=2 ") {
			t.Fatal("missing receipt must remain an unknown SQL lookup", output.String())
		}
	}
}

func TestManagementReceiptSubsetKeepsSourceAndTypedIsolation(t *testing.T) {
	for _, name := range []string{"hit", "database", "owner", "stamp", "empty_stamp", "transaction", "pool", "nil_tx", "unverified", "absent_scope", "large", "cancel", "corrupt"} {
		t.Run(name, func(t *testing.T) {
			scope, ctx := receiptSetScopeForTest()
			enableManagementReceiptSetScope(ctx, scope.tx, true)
			p, n := math.SmallestNonzeroFloat64, int64(math.MaxInt64)
			warm := map[string]managementStreamReceipt{"a": {valid: true, evidence: []byte{}, usage: managementStreamUsage{InputPrice: &p, OutputTokens: &n}}, "invalid": {valid: false}}
			if _, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, []string{"a", "invalid", "absent"}, false, func() (map[string]managementStreamReceipt, error) { return warm, nil }); err != nil {
				t.Fatal(err)
			}
			var db controlQueryDB = scope.tx
			owner := scope.version.owner
			ids := []string{"a", "a", "invalid", "absent", "unknown"}
			switch name {
			case "database":
				scope.version.database = &sql.DB{}
			case "owner":
				owner = "foreign"
			case "stamp":
				scope.version.stamp = "changed"
			case "empty_stamp":
				scope.version.stamp = ""
			case "transaction":
				db = &sql.Tx{}
			case "pool":
				db = scope.version.database
			case "nil_tx":
				db = (*sql.Tx)(nil)
			case "unverified":
				scope.complete.Store(false)
			case "absent_scope":
				ctx = context.Background()
			case "large":
				ids = make([]string, 4097)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "corrupt":
				scope.store.entries[0].body = []byte("invalid json")
			}
			got, missing, err := managementReceiptSubset(ctx, db, owner, ids)
			if name == "cancel" {
				if !errors.Is(err, context.Canceled) || got != nil || missing != nil {
					t.Fatal("cancellation published partial values", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name != "hit" {
				if len(got) != 0 || !reflect.DeepEqual(missing, ids) {
					t.Fatal("subset crossed current proof scope or changed original IDs")
				}
				return
			}
			if len(got) != 1 || !reflect.DeepEqual(got["a"], warm["a"]) || !reflect.DeepEqual(missing, []string{"invalid", "absent", "unknown"}) {
				t.Fatal("invalid/absent evidence suppressed SQL or typed values changed")
			}
			*got["a"].usage.InputPrice, *got["a"].usage.OutputTokens = 3, 4
			again, _, err := managementReceiptSubset(ctx, db, owner, []string{"a"})
			if err != nil || *again["a"].usage.InputPrice != math.SmallestNonzeroFloat64 || *again["a"].usage.OutputTokens != math.MaxInt64 || again["a"].evidence == nil {
				t.Fatal("subset values alias retained data", err)
			}
		})
	}
}

func TestManagementReceiptSubsetUnionsOnlyCurrentValidRows(t *testing.T) {
	scope, ctx := receiptSetScopeForTest()
	enableManagementReceiptSetScope(ctx, scope.tx, true)
	seed := func(ids []string, rows map[string]managementStreamReceipt, strict bool) {
		t.Helper()
		if _, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, ids, strict, func() (map[string]managementStreamReceipt, error) { return rows, nil }); err != nil {
			t.Fatal(err)
		}
	}
	seed([]string{"a", "invalid", "absent"}, map[string]managementStreamReceipt{"a": {valid: true}, "invalid": {valid: false}}, false)
	seed([]string{"b"}, map[string]managementStreamReceipt{"b": {valid: true}}, true)
	foreign := scope.version
	foreign.owner, foreign.ids = "another-owner", "foreign"
	body, _ := json.Marshal([]managementCachedReceipt{{ID: "foreign", Valid: true}})
	scope.store.put(&managementReceiptSet{version: foreign, body: body, rows: 1, bytes: len(body) + 256})
	got, missing, err := managementReceiptSubset(ctx, scope.tx, scope.version.owner, []string{"a", "b", "invalid", "absent", "foreign"})
	if err != nil || len(got) != 2 || !got["a"].valid || !got["b"].valid || !reflect.DeepEqual(missing, []string{"invalid", "absent", "foreign"}) {
		t.Fatal("union crossed scope, strict invalidity, or missing-row evidence", err)
	}
	// Returned values remain usable after eviction, while the next lookup must
	// observe the current store rather than retaining another hidden cache.
	scope.store.mu.Lock()
	scope.store.entries, scope.store.bytes = nil, 0
	scope.store.mu.Unlock()
	again, missing, err := managementReceiptSubset(ctx, scope.tx, scope.version.owner, []string{"a", "b"})
	if err != nil || len(again) != 0 || len(got) != 2 || !reflect.DeepEqual(missing, []string{"a", "b"}) {
		t.Fatal("eviction changed independent results or left hidden retention", err)
	}
}

func TestManagementReceiptSubsetKeepsCurrentSQLAcrossMutations(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	prepareManagementRevisionsForTest(t, db)
	ctx := managementTraceContext(context.Background())
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	read := func(tx *sql.Tx, ids []string, strict bool) map[string]managementStreamReceipt {
		t.Helper()
		proof, _, err := s.managementReadProof(ctx, tx, "owner")
		if err != nil {
			t.Fatal(err)
		}
		scope := managementReceiptSetScopeFromProof(&s.receiptSets, db, tx, "owner", proof)
		call := context.WithValue(ctx, managementReceiptSetScopeKey{}, scope)
		enableManagementReceiptSetScope(call, tx, true)
		got, err := readManagementStreamReceipts(call, tx, "owner", ids, strict)
		want, wantErr := readManagementStreamReceiptsUncached(context.Background(), tx, "owner", ids, strict)
		if !errors.Is(err, wantErr) || !reflect.DeepEqual(got, want) {
			t.Fatal("subset diverged from same-transaction SQL, fallback or ownership", err, wantErr)
		}
		return got
	}
	fresh := func(ids []string, strict bool) map[string]managementStreamReceipt {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		return read(tx, ids, strict)
	}
	warm := []string{"a-1-gpt-5.5", "q-1-gpt-5.5", "new-receipt"}
	fresh(warm, false)
	old, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback()
	before := read(old, warm, false)
	for i, query := range []string{
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{input_price}','9') WHERE id='a-1-gpt-5.5'`,
		`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES('new-receipt','a',1,1,'gpt-5.5',0,'[]','matched','{"input_price":12}')`,
		`UPDATE console_sub_usage SET result='{"input_tokens":1.0}' WHERE id='q-1-gpt-5.5'`,
		`DELETE FROM console_sub_usage WHERE id='a-1-gpt-5.5'`,
		`UPDATE console_sub_accounts SET owner='foreign' WHERE id='a'`,
		`UPDATE console_sub_accounts SET owner='owner' WHERE id='a'`,
		`UPDATE console_management_receipt_signal SET evidence_digest=decode('abcd','hex') WHERE id='new-receipt'`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		ids := append(append([]string{}, warm...), fmt.Sprintf("not-present-%d", i))
		output.Reset()
		fresh(ids, false)
		if strings.Contains(output.String(), "management_receipt_subset_cache reused=") {
			t.Fatal("source/ownership change reused previous-version rows", query)
		}
		// A non-strict cache may contain invalid rows. Strict reads must still
		// query them and return the same fallback with no partial result.
		fresh(append(ids, "strict-missing"), true)
		if after := read(old, warm, false); !reflect.DeepEqual(after, before) {
			t.Fatal("new-generation data contaminated old RR snapshot")
		}
	}
}

func BenchmarkManagementReceiptSubsetBoundedDecode(b *testing.B) {
	scope, ctx := receiptSetScopeForTest()
	enableManagementReceiptSetScope(ctx, scope.tx, true)
	for batch := 0; batch < 4; batch++ {
		rows := make([]managementCachedReceipt, 4096)
		for i := range rows {
			rows[i] = managementCachedReceipt{ID: fmt.Sprintf("receipt-%05d", batch*4096+i), Valid: true, Evidence: make([]byte, 32)}
		}
		body, _ := json.Marshal(rows)
		version := scope.version
		version.ids = fmt.Sprint(batch)
		scope.store.put(&managementReceiptSet{version: version, body: body, rows: len(rows), bytes: len(body) + 256})
	}
	ids := make([]string, 4096)
	for i := range ids {
		ids[i] = fmt.Sprintf("receipt-%05d", i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, missing, err := managementReceiptSubset(ctx, scope.tx, scope.version.owner, ids)
		if err != nil || len(got) != len(ids) || len(missing) != 0 {
			b.Fatal("bounded subset decode changed output", err)
		}
	}
}

package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func TestManagementReceiptAbsenceNeedsSuccessfulBoundedRead(t *testing.T) {
	for _, name := range []string{"nil", "empty", "error", "fallback", "cancel", "oversize"} {
		t.Run(name, func(t *testing.T) {
			scope, ctx := receiptSetScopeForTest()
			enableManagementReceiptSetScope(ctx, scope.tx, true)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			ids := []string{"absent-b", "absent-a", "absent-b"}
			if name == "oversize" {
				ids = []string{strings.Repeat("x", managementReceiptSetBudget)}
			}
			_, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, ids, false, func() (map[string]managementStreamReceipt, error) {
				switch name {
				case "nil":
					return nil, nil
				case "error":
					return nil, errors.New("read interrupted")
				case "fallback":
					return nil, errManagementPredicateFallback
				case "cancel":
					cancel()
				}
				return map[string]managementStreamReceipt{}, nil
			})
			success := name == "nil" || name == "empty"
			if success && err != nil {
				t.Fatal(err)
			}
			// Do not let the caller mutate the retained absence certificate.
			ids[0] = "changed"
			got, missing, subsetErr := managementReceiptSubset(context.WithValue(context.Background(), managementReceiptSetScopeKey{}, scope), scope.tx, scope.version.owner, []string{"absent-a", "unknown", "absent-b", "absent-a"})
			wantMissing := []string{"absent-a", "unknown", "absent-b", "absent-a"}
			if success {
				wantMissing = []string{"unknown"}
				entry := scope.store.entries[0]
				wantBytes := int(unsafe.Sizeof(managementReceiptSet{})) + len(entry.version.owner) + len(entry.version.stamp) + len(entry.version.ids) + len(entry.body) + len(entry.absent)
				if entry.bytes != wantBytes || scope.store.bytes != wantBytes || cap(entry.absent) != len(entry.absent) {
					t.Fatal("absence certificate escaped the existing memory budget")
				}
			} else if len(scope.store.entries) != 0 {
				t.Fatal("failed, canceled or oversized read admitted absence")
			}
			if subsetErr != nil || len(got) != 0 || !reflect.DeepEqual(missing, wantMissing) {
				t.Fatal("absence certificate changed output or suppressed unknown IDs", subsetErr, missing)
			}
		})
	}
}

func TestManagementReceiptAbsenceSkipsOnlyKnownProjectionLookups(t *testing.T) {
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
	if _, err := readManagementStreamReceipts(ctx, tx, "owner", []string{"absent-a", "absent-b"}, false); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	// A strict read with a different set may reuse an absence, but never turn
	// it into a fabricated row or conflate it with an invalid projected row.
	got, err := readManagementStreamReceipts(ctx, tx, "owner", []string{"absent-b", "absent-b"}, true)
	if err != nil || len(got) != 0 || strings.Contains(output.String(), "stage=receipt_signal_rows ") || !strings.Contains(output.String(), "reused=0 raw_ids=0 absent=1 ") {
		t.Fatal("known empty subset still queried SQL or fabricated a row", err, output.String())
	}
	// No raw lookup is suppressed: absent projections remain unresolved.
	output.Reset()
	probe := &subProbe{ID: "absent-b"}
	if err := s.subAttachUsageProbesWithCurrentFrom(ctx, tx, "owner", map[string][]*subProbe{"absent-b": {probe}}, nil); err != nil || probe.Usage != nil || !strings.Contains(output.String(), "stage=page_receipts ") {
		t.Fatal("certified projection absence bypassed the raw decoder", err, output.String())
	}
	output.Reset()
	got, err = readManagementStreamReceipts(ctx, tx, "owner", []string{"absent-a", "a-1-gpt-5.5"}, true)
	want, wantErr := readManagementStreamReceiptsUncached(context.Background(), tx, "owner", []string{"absent-a", "a-1-gpt-5.5"}, true)
	if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) || !strings.Contains(output.String(), "raw_ids=1 absent=1 ") || !strings.Contains(output.String(), "stage=receipt_signal_rows ") {
		t.Fatal("unknown receipt escaped current SQL", err, wantErr, output.String())
	}
}

func TestManagementReceiptAbsenceKeepsFilteredPageAndAddContext(t *testing.T) {
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,id}',to_jsonb('absent-'||group_id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET result=jsonb_build_object('model','gpt-5.5','availability',jsonb_build_object('id','absent-'||group_id))`); err != nil {
		t.Fatal(err)
	}
	prepareManagementRevisionsForTest(t, s.control.db)
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for _, model := range []string{"gpt-5.5", "gpt-6-astra"} {
		output.Reset()
		f := managementQueryFilters{GroupStatus: "available", Model: model}
		got, err := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", false)
		optimizedLogs := output.String()
		want, wantErr := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", true)
		got.Page.ReadAtMS = want.Page.ReadAtMS
		if err != nil || wantErr != nil || !reflect.DeepEqual(got.Page, want.Page) || !reflect.DeepEqual(managementContextForPage(got.View, got.Page, model), managementContextForPage(want.View, want.Page, model)) {
			t.Fatal("absence reuse changed complete filtered page or add context", model, err, wantErr)
		}
		if !strings.Contains(optimizedLogs, "absent=25 ") || !strings.Contains(optimizedLogs, "stage=account_receipts ") || !strings.Contains(optimizedLogs, "stage=page_receipts ") {
			t.Fatal("filtered page did not reuse absences while keeping both raw fallbacks", model, optimizedLogs)
		}
	}
}

func TestManagementReceiptAbsenceInvalidatesWithVisibilityAndTruncate(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	if _, err := db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('foreign','other','fixture','https://foreign.test','fixture','');
INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result)
VALUES('moving-receipt','foreign',1,1,'gpt-5.5',0,'[]','matched','{"input_price":7}')`); err != nil {
		t.Fatal(err)
	}
	prepareManagementRevisionsForTest(t, db)
	ctx := managementTraceContext(context.Background())
	read := func(tx *sql.Tx, ids []string) map[string]managementStreamReceipt {
		t.Helper()
		proof, _, err := s.managementReadProof(ctx, tx, "owner")
		if err != nil {
			t.Fatal(err)
		}
		scope := managementReceiptSetScopeFromProof(&s.receiptSets, db, tx, "owner", proof)
		if scope == nil {
			t.Fatal("mutation fixture lost its complete current receipt proof")
		}
		call := context.WithValue(ctx, managementReceiptSetScopeKey{}, scope)
		enableManagementReceiptSetScope(call, tx, true)
		got, err := readManagementStreamReceipts(call, tx, "owner", ids, true)
		want, wantErr := readManagementStreamReceiptsUncached(context.Background(), tx, "owner", ids, true)
		if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("absence diverged from same-snapshot owner-scoped SQL", err, wantErr)
		}
		return got
	}
	old, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback()
	if got := read(old, []string{"moving-receipt", "future-receipt"}); len(got) != 0 {
		t.Fatal("foreign receipt visible before reassignment")
	}
	// TRUNCATE needs an exclusive lock, so retain the old snapshot only for
	// the row-level visibility changes and verify it before closing it.
	for _, query := range []string{
		`UPDATE console_sub_usage SET account_id='a' WHERE id='moving-receipt'`,
		`UPDATE console_sub_usage SET account_id='foreign' WHERE id='moving-receipt'`,
		`UPDATE console_sub_accounts SET owner='owner' WHERE id='foreign'`,
		`UPDATE console_sub_accounts SET owner='other' WHERE id='foreign'`,
		`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES('future-receipt','a',1,1,'gpt-5.5',0,'[]','matched','{"input_price":9}')`,
		`DELETE FROM console_sub_usage WHERE id='future-receipt'`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		// Not the warmed exact set: this must exercise subset certificates.
		read(tx, []string{"moving-receipt", "future-receipt", "unknown"})
		tx.Rollback()
		if got := read(old, []string{"moving-receipt", "future-receipt", "old-unknown"}); len(got) != 0 {
			t.Fatal("current visibility contaminated an older RR snapshot")
		}
	}
	old.Rollback()
	if _, err := db.Exec(`TRUNCATE console_sub_usage CASCADE`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if got := read(tx, []string{"a-1-gpt-5.5", "moving-receipt", "future-receipt"}); len(got) != 0 {
		t.Fatal("truncate retained previously present receipts")
	}
}

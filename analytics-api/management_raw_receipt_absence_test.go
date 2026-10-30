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

func TestManagementRawReceiptAbsenceRequiresCurrentCompleteScope(t *testing.T) {
	for _, name := range []string{"hit", "overlap", "owner", "database", "stamp", "transaction", "pool", "incomplete", "missing_proof", "no_scope", "large"} {
		t.Run(name, func(t *testing.T) {
			scope, ctx := receiptSetScopeForTest()
			enableManagementReceiptSetScope(ctx, scope.tx, true)
			owner := scope.version.owner
			var db controlQueryDB = scope.tx
			calls := 0
			var selected []string
			current := map[string]*subUsage{}
			read := func(ids []string) error {
				calls++
				selected = append([]string(nil), ids...)
				for _, id := range ids {
					if id == "present" {
						current[id] = &subUsage{Status: "matched", Message: "full raw detail"}
					}
				}
				return nil
			}
			if err := readManagementRawReceiptAbsences(ctx, db, owner, []string{"absent-b", "present", "absent-a", "absent-a"}, current, read); err != nil {
				t.Fatal(err)
			}
			ids := []string{"absent-a", "absent-b"}
			switch name {
			case "overlap":
				ids = []string{"absent-b", "unknown", "present", "absent-b"}
			case "owner":
				owner = "foreign"
			case "database":
				scope.version.database = &sql.DB{}
			case "stamp":
				scope.version.stamp = "changed"
			case "transaction":
				db = &sql.Tx{}
			case "pool":
				db = scope.version.database
			case "incomplete":
				scope.complete.Store(false)
			case "missing_proof":
				scope.version.stamp = ""
			case "no_scope":
				ctx = context.Background()
			case "large":
				ids = make([]string, 4097)
				for i := range ids {
					ids[i] = "absent-a"
				}
			}
			current = map[string]*subUsage{}
			if err := readManagementRawReceiptAbsences(ctx, db, owner, ids, current, read); err != nil {
				t.Fatal(err)
			}
			if name == "hit" {
				if calls != 1 || len(current) != 0 {
					t.Fatal("certified missing raw rows still queried or fabricated")
				}
			} else if name == "overlap" {
				if calls != 2 || !reflect.DeepEqual(selected, []string{"unknown", "present"}) || current["present"].Message != "full raw detail" {
					t.Fatal("unknown/present receipt bypassed original raw decoder", selected)
				}
			} else if calls != 2 || !reflect.DeepEqual(selected, ids) {
				t.Fatal("scope mismatch suppressed SQL", name, calls)
			}
		})
	}
}

func TestManagementRawReceiptAbsenceKeepsProjectionCertificatesSeparate(t *testing.T) {
	scope, ctx := receiptSetScopeForTest()
	enableManagementReceiptSetScope(ctx, scope.tx, true)
	owner := scope.version.owner
	ids := []string{"missing"}
	if _, err := readManagementReceiptSet(ctx, scope.tx, owner, ids, false, func() (map[string]managementStreamReceipt, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	calls := 0
	read := func([]string) error { calls++; return nil }
	if err := readManagementRawReceiptAbsences(ctx, scope.tx, owner, ids, map[string]*subUsage{}, read); err != nil || calls != 1 {
		t.Fatal("projection absence was treated as raw absence", err)
	}
	// Conversely, a raw certificate is not an input to projection reuse. Test
	// that independently of the deliberately retained projection certificate.
	for i, entry := range scope.store.entries {
		if !entry.version.rawAbsence {
			scope.store.bytes -= entry.bytes
			scope.store.entries = append(scope.store.entries[:i], scope.store.entries[i+1:]...)
			break
		}
	}
	got, missing, err := managementReceiptSubset(ctx, scope.tx, owner, ids)
	if err != nil || len(got) != 0 || !reflect.DeepEqual(missing, ids) {
		t.Fatal("raw absence entered projection reuse", err, missing)
	}
	entry := scope.store.entries[0]
	wantBytes := int(unsafe.Sizeof(managementReceiptSet{})) + len(entry.version.owner) + len(entry.version.stamp) + len(entry.version.ids) + len(entry.absent)
	if entry.bytes != wantBytes || scope.store.bytes != wantBytes || cap(entry.absent) != len(entry.absent) || len(entry.body) != 0 {
		t.Fatal("raw absence escaped existing budget or retained raw payloads")
	}
}

func TestManagementRawReceiptAbsenceRejectsErrorsCancellationAndOversize(t *testing.T) {
	for _, name := range []string{"error", "cancel", "already_canceled", "oversize", "present"} {
		t.Run(name, func(t *testing.T) {
			scope, base := receiptSetScopeForTest()
			enableManagementReceiptSetScope(base, scope.tx, true)
			ctx, cancel := context.WithCancel(base)
			defer cancel()
			if name == "already_canceled" {
				cancel()
			}
			ids := []string{"missing"}
			if name == "oversize" {
				ids[0] = strings.Repeat("x", managementReceiptSetBudget)
			}
			current := map[string]*subUsage{}
			failure := errors.New("typed raw decode failed")
			err := readManagementRawReceiptAbsences(ctx, scope.tx, scope.version.owner, ids, current, func([]string) error {
				if name == "already_canceled" {
					t.Fatal("canceled read reached SQL")
				}
				if name == "error" {
					return failure
				}
				if name == "cancel" {
					cancel()
				}
				if name == "present" {
					current[ids[0]] = &subUsage{}
				}
				return nil
			})
			if name == "error" && !errors.Is(err, failure) || (name == "cancel" || name == "already_canceled") && !errors.Is(err, context.Canceled) {
				t.Fatal("raw error or cancellation changed", err)
			}
			if len(scope.store.entries) != 0 || scope.store.bytes != 0 {
				t.Fatal("unsafe absence admitted")
			}
		})
	}
}

func TestManagementRawReceiptAbsenceInvalidatesWithRawRowsAndOwnership(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	if _, err := db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('foreign','other','fixture','https://foreign.test','fixture','');
INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result)
VALUES('moving','foreign',1,1,'gpt-5.5',0,'[]','matched','{"input_price":7,"message":"raw detail"}')`); err != nil {
		t.Fatal(err)
	}
	prepareManagementRevisionsForTest(t, db)
	ctx := context.Background()
	read := func(tx *sql.Tx) map[string]*subUsage {
		t.Helper()
		proof, _, err := s.managementReadProof(ctx, tx, "owner")
		if err != nil {
			t.Fatal(err)
		}
		scope := managementReceiptSetScopeFromProof(&s.receiptSets, db, tx, "owner", proof)
		if scope == nil {
			t.Fatal("complete proof unavailable")
		}
		call := context.WithValue(ctx, managementReceiptSetScopeKey{}, scope)
		enableManagementReceiptSetScope(call, tx, true)
		ids := []string{"moving", "future", "a-1-gpt-5.5"}
		got, want := map[string]*subUsage{}, map[string]*subUsage{}
		gotErr := s.subReadUsageByIDsFrom(call, tx, "owner", ids, got)
		wantErr := s.subReadUsageByIDsUncachedFrom(ctx, tx, "owner", ids, want)
		if gotErr != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("raw absence changed same-snapshot authorized SQL", gotErr, wantErr)
		}
		return got
	}
	old, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback()
	if len(read(old)) != 1 {
		t.Fatal("foreign or missing receipt visible")
	}
	for _, query := range []string{
		`UPDATE console_sub_usage SET account_id='a' WHERE id='moving'`,
		`UPDATE console_sub_usage SET account_id='foreign' WHERE id='moving'`,
		`UPDATE console_sub_accounts SET owner='owner' WHERE id='foreign'`,
		`UPDATE console_sub_accounts SET owner='other' WHERE id='foreign'`,
		`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES('future','a',1,1,'gpt-5.5',0,'[]','matched','{"input_price":9}')`,
		`DELETE FROM console_sub_usage WHERE id='future'`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		read(tx)
		tx.Rollback()
		if len(read(old)) != 1 {
			t.Fatal("new state contaminated old RR transaction")
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
	if len(read(tx)) != 0 {
		t.Fatal("truncate reused present receipts")
	}
}

func TestManagementRawReceiptAbsencePreservesPagesAndEmbeddedUsage(t *testing.T) {
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,id}',to_jsonb('absent-'||group_id));
UPDATE console_sub_targets SET result=jsonb_build_object('model','gpt-5.5','availability',jsonb_build_object('id','absent-'||group_id,'usage',jsonb_build_object('status','embedded','input_price',11)))`); err != nil {
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
		logs := output.String()
		want, wantErr := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", true)
		got.Page.ReadAtMS = want.Page.ReadAtMS
		if err != nil || wantErr != nil || !reflect.DeepEqual(got.Page, want.Page) || !reflect.DeepEqual(managementContextForPage(got.View, got.Page, model), managementContextForPage(want.View, want.Page, model)) {
			t.Fatal("raw absence changed page or add context", model, err, wantErr)
		}
		if !strings.Contains(logs, "management_raw_receipt_absence reused=25 raw_ids=0 stored=false phase=page ") || strings.Contains(logs, "stage=page_receipts ") {
			t.Fatal("same-snapshot missing raw receipts still queried twice", logs)
		}
		s.rememberManagementPagingPlan("owner", f, got)
		output.Reset()
		next, ok, nextErr := s.currentManagementPagingPage(context.Background(), "owner", f, got.Page.NextCursor, false)
		pagingLogs := output.String()
		full, fullErr := s.readManagementQuerySnapshot(context.Background(), "owner", f, got.Page.NextCursor, true)
		next.ReadAtMS = full.Page.ReadAtMS
		expectedContext := managementContextForPage(full.View, full.Page, model)
		// Paging restores prices from JSON; pgx can use time.Local for UTC
		// timestamps while JSON uses time.UTC. Compare the complete public
		// context, including prices and effective_at, rather than that private
		// location pointer. Rows retain the original typed equality check.
		contextEqual := mustJSON(next.Context) == mustJSON(expectedContext)
		if nextErr != nil || fullErr != nil || !ok || !reflect.DeepEqual(next.Rows, full.Page.Rows) || !contextEqual {
			t.Logf("model=%s paging_hit=%t rows_equal=%t context_json_equal=%t", model, ok, reflect.DeepEqual(next.Rows, full.Page.Rows), contextEqual)
			if !contextEqual {
				t.Logf("context difference\nnext=%s\nfull=%s", mustJSON(next.Context), mustJSON(expectedContext))
			}
			for i := range min(len(next.Rows), len(full.Page.Rows)) {
				if !reflect.DeepEqual(next.Rows[i], full.Page.Rows[i]) {
					t.Logf("first row difference index=%d\nnext=%s\nfull=%s", i, mustJSON(next.Rows[i]), mustJSON(full.Page.Rows[i]))
					break
				}
			}
			t.Logf("paging trace:\n%s", pagingLogs)
			t.Fatal("raw absence changed fresh-transaction paging or add context", nextErr, fullErr)
		}
		if !strings.Contains(pagingLogs, "management_raw_receipt_absence reused=25 raw_ids=0 stored=false phase=page ") || strings.Contains(pagingLogs, "stage=page_receipts ") {
			t.Fatal("current paging repeated certified empty raw SQL", pagingLogs)
		}
	}
}

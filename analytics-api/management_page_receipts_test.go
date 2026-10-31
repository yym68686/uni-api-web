package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestManagementSummaryReceiptsMatchCompleteDecoder(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	for _, raw := range []string{
		`null`, `{}`, `{"status":null}`, `{"status":"","input_tokens":0,"input_price":0}`,
		`{"input_tokens":9223372036854775807,"output_tokens":-9223372036854775808}`,
		`{"input_price":1.7976931348623157e308,"output_price":5e-324}`,
		`{"status":"matched","message":"detail","log_id":3,"request_id":"private","checked_at":123,"created_at":"date","actual_cost":1,"total_cost":2,"rate_multiplier":0.5,"cache_read_tokens":5,"cache_creation_tokens":6,"cache_read_price":7,"cache_write_price":8,"paid_input_price":9,"paid_output_price":10,"duration_ms":11,"first_token_ms":12,"input_price":0,"output_price":3,"input_tokens":0,"output_tokens":2}`,
		`{"Status":"matched","Input_Price":3}`, `{"input_tokens":1.0}`, `{"input_price":"bad"}`,
		`{"message":false}`, `{"actual_cost":1e1000}`, `[]`, `true`,
	} {
		if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET status='pending',result=$1 WHERE id='a-1-extra-model'`, raw); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"page", "account"} {
			func() {
				tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				want, got := managementReceiptAccounts(), managementReceiptAccounts()
				// Cover the legacy target metadata compactor as well as model and
				// capability usage; they do not share the same normalization code.
				want[0].Targets[0].Result.Availability.ID = "a-1-extra-model"
				got[0].Targets[0].Result.Availability.ID = "a-1-extra-model"
				wantErr := s.subAttachUsageFrom(ctx, tx, "owner", want)
				readCtx := context.WithValue(ctx, managementAccountReceiptsKey{}, phase == "account")
				gotErr := s.attachManagementSummaryUsageFrom(readCtx, tx, "owner", got, nil)
				if (wantErr == nil) != (gotErr == nil) {
					t.Fatal("decoder error changed", phase, raw, wantErr, gotErr)
				}
				if wantErr != nil {
					if wantErr.Error() != gotErr.Error() || !reflect.DeepEqual(want, got) {
						t.Fatal("partial result or typed error changed", raw)
					}
					return
				}
				wantErr = normalizeManagementAccounts(want)
				gotErr = normalizeManagementAccounts(got)
				if (wantErr == nil) != (gotErr == nil) || wantErr != nil && (reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) || wantErr.Error() != gotErr.Error()) || wantErr == nil && !reflect.DeepEqual(want, got) {
					t.Fatal("summary or legacy numeric error changed", phase, raw, wantErr, gotErr)
				}
			}()
		}
	}
}

func TestManagementPageSummaryReceiptsKeepMissingForeignAndSnapshot(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('foreign','other','F','https://foreign.test','f','')`,
		`UPDATE console_sub_usage SET account_id='foreign' WHERE id='q-1-extra-model'`,
		`DELETE FROM console_management_receipt_signal WHERE id='a-1-extra-model'`,
		`UPDATE console_sub_usage SET result='{"Status":"matched","Input_Price":7}' WHERE id='a-1-gpt-6-astra'`,
	} {
		if _, err := s.control.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	want, got := managementReceiptAccounts(), managementReceiptAccounts()
	if err := s.subAttachUsageFrom(ctx, tx, "owner", want); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"input_price":99}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if err := s.attachManagementPageSummaryUsageFrom(ctx, tx, "owner", got, nil); err != nil {
		t.Fatal(err)
	}
	if *got[0].Targets[0].ToolUse.Attempts[0].Usage.InputPrice != 7 {
		t.Fatal("unsupported legacy receipt skipped raw decoding")
	}
	for _, accounts := range [][]subAccount{want, got} {
		if err := normalizeManagementAccounts(accounts); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("missing/foreign/fallback/snapshot output changed")
	}
}

func TestManagementPageSummaryReceiptsFallbackAndCancel(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	for _, complete := range []bool{false, true} {
		if _, err := s.control.db.Exec(`UPDATE console_management_projection_state SET complete=$1,phase=CASE WHEN $1 THEN 'complete' ELSE 'models' END WHERE singleton`, complete); err != nil {
			t.Fatal(err)
		}
		for _, pinned := range []bool{false, true} {
			func() {
				var db controlQueryDB = s.control.db
				if pinned {
					tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					db = tx
				}
				want, got := managementReceiptAccounts(), managementReceiptAccounts()
				if err := s.subAttachUsageFrom(ctx, db, "owner", want); err != nil {
					t.Fatal(err)
				}
				if err := s.attachManagementPageSummaryUsageFrom(ctx, db, "owner", got, nil); err != nil {
					t.Fatal(err)
				}
				if !pinned || !complete {
					if !reflect.DeepEqual(want, got) {
						t.Fatal("non-summary fallback changed")
					}
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				before := mustJSON(got)
				if err := s.attachManagementPageSummaryUsageFrom(canceled, db, "owner", got, nil); !errors.Is(err, context.Canceled) || mustJSON(got) != before {
					t.Fatal("cancellation published partial data", err)
				}
			}()
		}
	}
}

func TestManagementPageSummaryReceiptsLocalMeasurement(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result)
 SELECT 'page-measure-'||g,'a',1,1,'measurement',0,'[]','matched',jsonb_build_object('status','matched','input_price',2,'output_price',3,
 'input_tokens',11,'output_tokens',12,'actual_cost',0.2,'total_cost',0.1,'rate_multiplier',2,'message',repeat('diagnostic ',12),
 'request_id','private-'||g,'checked_at',1234,'duration_ms',100,'first_token_ms',20)
 FROM generate_series(1,7000) g`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	makeInput := func() []subAccount {
		target := subTarget{GroupID: 1}
		for i := 1; i <= 7000; i++ {
			target.Models = append(target.Models, subModelResult{Model: fmt.Sprint(i), Result: &subResult{Availability: subProbe{ID: fmt.Sprintf("page-measure-%d", i)}}})
		}
		return []subAccount{{ID: "a", Targets: []subTarget{target}}}
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	traceCtx := managementTraceContext(ctx)
	for round := 0; round < 3; round++ {
		want, got := makeInput(), makeInput()
		start := time.Now()
		if err := s.subAttachUsageFrom(ctx, tx, "owner", want); err != nil {
			t.Fatal(err)
		}
		oldTime := time.Since(start)
		start = time.Now()
		if err := s.attachManagementPageSummaryUsageFrom(traceCtx, tx, "owner", got, nil); err != nil {
			t.Fatal(err)
		}
		newTime := time.Since(start)
		for _, a := range [][]subAccount{want, got} {
			if err := normalizeManagementAccounts(a); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatal("large page output changed")
		}
		t.Logf("local_page_receipts round=%d rows=7000 raw_ms=%.3f projected_ms=%.3f", round, float64(oldTime.Microseconds())/1000, float64(newTime.Microseconds())/1000)
	}
	if !strings.Contains(output.String(), "management_page_receipt_signals requested=7000 resolved=7000 ") || strings.Contains(output.String(), "stage=page_receipts ") {
		t.Fatal("large page did not eliminate raw payload reads")
	}
}

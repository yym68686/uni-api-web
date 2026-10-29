package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"testing"
)

func TestManagementAccountReceiptReusePreservesWholeViewAndFallback(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET result=jsonb_build_object('model','gpt-5.5','checked_at',group_id,'verdict','pass',
 'availability',jsonb_build_object('id','a-'||group_id||'-gpt-5.5','status','success','usage',jsonb_build_object('status','embedded','actual_cost',99)),
 'quality',jsonb_build_object('id','q-'||group_id||'-gpt-5.5','status','success'));
 UPDATE console_sub_usage SET result='{"status":null,"input_price":0,"output_price":null,"input_tokens":0,"output_tokens":9007199254740991,"actual_cost":1,"message":"detail","request_id":"private"}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"", "gpt-5.5", "extra-model", "missing-extra"} {
		for _, catalogOnly := range []bool{false, true} {
			tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				old, oldErr := s.readManagementDatabaseModeFrom(ctx, tx, "owner", nil, true, model, catalogOnly)
				var trace bytes.Buffer
				previous := log.Writer()
				log.SetOutput(&trace)
				defer log.SetOutput(previous)
				got, gotErr := s.readManagementDatabaseModeWithReceiptsFrom(ctx, tx, "owner", nil, true, model, catalogOnly, &managementReceiptReuse{})
				if oldErr != nil || gotErr != nil || mustJSON(old) != mustJSON(got) {
					t.Fatal("normalized accounts, rates, quality, revision or view changed", model, catalogOnly, oldErr, gotErr)
				}
				// 61 legacy target results each refer to both predicate receipts.
				// The complete stream and gpt selection can reuse all 122 IDs;
				// catalog-only reads must retain the original raw reader.
				if !catalogOnly && (model == "" || model == "gpt-5.5") {
					if !strings.Contains(trace.String(), "management_receipt_reuse reused=122 raw_ids=0 phase=account ") || strings.Contains(trace.String(), "management_rows_read stage=page_receipts ") {
						t.Fatal("verified account receipts were still queried")
					}
				} else if catalogOnly && strings.Contains(trace.String(), "management_receipt_reuse ") {
					t.Fatal("catalog-only read invented verified receipts")
				}
				for _, private := range []string{"private", "embedded", "gpt-5.5", "owner"} {
					if strings.Contains(trace.String(), private) {
						t.Fatal("reuse trace leaked private evidence")
					}
				}
			}()
		}
	}
}

func TestManagementAccountReceiptReuseRetainsOrdinaryFullUsageAndReaderErrors(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET result='{"model":"gpt-5.5","availability":{"id":"a-1-gpt-5.5","usage":{"status":"embedded"}}}' WHERE group_id=1;
 UPDATE console_sub_usage SET result='{"input_price":2,"actual_cost":9,"message":"full detail","request_id":"private"}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	readAccounts := func() []subAccount {
		t.Helper()
		accounts, err := readSubAccountHeaders(ctx, tx, "owner", "a")
		if err != nil {
			t.Fatal(err)
		}
		return accounts
	}
	full := readAccounts()
	if err := s.readSubAccountDetailsWithModelsFrom(ctx, tx, "owner", "a", 1, "", true, true, full, nil, sharedQuality{}, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	usage := full[0].Targets[0].Result.Availability.Usage
	if usage.Message != "full detail" || usage.RequestID != "private" || usage.ActualCost == nil || *usage.ActualCost != 9 {
		t.Fatal("ordinary account reader lost full receipt detail")
	}
	for _, cause := range []error{errors.New("fixture receipt decode failed"), context.Canceled} {
		accounts := readAccounts()
		calls := 0
		got := s.readSubAccountDetailsWithReadersFrom(ctx, tx, "owner", "a", 1, "", true, false, accounts, nil, sharedQuality{}, nil, func() (map[string]map[string]subObservedRate, error) {
			t.Fatal("rates read continued after receipt error")
			return nil, nil
		}, true, func(accounts []subAccount) error {
			calls++
			return cause
		})
		if calls != 1 || !errors.Is(got, cause) || got.Error() != "模型账单读取失败，请重新读取" {
			t.Fatal("management receipt reader changed error boundary", got)
		}
	}
}

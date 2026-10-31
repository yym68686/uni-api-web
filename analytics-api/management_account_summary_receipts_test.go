package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestManagementAccountSummaryReceiptsKeepPageRevisionAndSelection(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET result=jsonb_build_object('model','extra-model','checked_at',group_id,'verdict','pass',
 'availability',jsonb_build_object('id','a-'||group_id||'-extra-model','status','success'),
 'quality',jsonb_build_object('id','q-'||group_id||'-extra-model','status','success'))`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, price := range []string{"", "normal", "abnormal", "unconfirmed"} {
		filters := managementQueryFilters{GroupStatus: "available", Model: "gpt-5.5", PriceStatus: price}
		page, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
		full, fullErr := s.readManagementQuerySnapshot(ctx, "owner", filters, "", true)
		if err != nil || fullErr != nil || !page.View.ProjectedModels || full.View.ProjectedModels || len(full.Rows) != 61 || price == "" && (len(page.Page.Rows) != 25 || page.Page.Total != 61) {
			t.Fatal("fixture failed to exercise projected account metadata", price, err, fullErr)
		}
		page.Page.ReadAtMS = full.Page.ReadAtMS
		if !reflect.DeepEqual(page.Page, full.Page) || !reflect.DeepEqual(managementContextForPage(page.View, page.Page, filters.Model), managementContextForPage(full.View, full.Page, filters.Model)) {
			t.Fatal("full page, cursor revision, prices or import context changed", price)
		}
		if page.Page.NextCursor == "" {
			continue
		}
		next, err := s.readManagementQuerySnapshot(ctx, "owner", filters, page.Page.NextCursor, false)
		selection, fullErr := s.readManagementQuerySnapshot(ctx, "owner", filters, page.Page.NextCursor, true)
		if err != nil || fullErr != nil || len(next.Page.Rows) != 25 || len(selection.Rows) != 61 || next.Page.Revision != page.Page.Revision || selection.Page.Revision != page.Page.Revision ||
			!reflect.DeepEqual(next.Page.Rows, selection.Page.Rows) || !reflect.DeepEqual(managementContextForPage(next.View, next.Page, filters.Model), managementContextForPage(selection.View, selection.Page, filters.Model)) {
			t.Fatal("pagination or complete action selection changed", price, err, fullErr)
		}
	}
}

func TestManagementAccountSummaryReceiptsLocalMeasurement(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result)
 SELECT 'account-summary-'||g,'a',1,1,'measurement',0,'[]','matched',jsonb_build_object('status','matched','input_price',2,'output_price',3,
 'input_tokens',11,'output_tokens',12,'actual_cost',0.2,'total_cost',0.1,'rate_multiplier',2,'message',repeat('diagnostic ',12),
 'request_id','private-'||g,'checked_at',1234,'duration_ms',100,'first_token_ms',20)
 FROM generate_series(1,605) g`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	makeInput := func() []subAccount {
		a := subAccount{ID: "a"}
		for i := 1; i <= 605; i++ {
			a.Targets = append(a.Targets, subTarget{GroupID: int64(i), Result: &subResult{Model: "measurement", Availability: subProbe{ID: fmt.Sprintf("account-summary-%d", i)}}})
		}
		return []subAccount{a}
	}
	previous := log.Writer()
	defer log.SetOutput(previous)
	traceCtx := context.WithValue(managementTraceContext(ctx), managementAccountReceiptsKey{}, true)
	request := &managementReceiptPlanRequest{hydrationOnly: true}
	traceCtx = context.WithValue(traceCtx, managementReceiptPlanKey{}, request)
	for round := 0; round < 3; round++ {
		want, got := makeInput(), makeInput()
		var rawTrace, projectedTrace bytes.Buffer
		var oldTime, newTime time.Duration
		oldRead := func() {
			log.SetOutput(&rawTrace)
			start := time.Now()
			if err := s.subAttachUsageFrom(traceCtx, tx, "owner", want); err != nil {
				t.Fatal(err)
			}
			oldTime = time.Since(start)
		}
		newRead := func() {
			log.SetOutput(&projectedTrace)
			start := time.Now()
			if err := s.attachManagementSummaryUsageFrom(traceCtx, tx, "owner", got, nil); err != nil {
				t.Fatal(err)
			}
			newTime = time.Since(start)
		}
		if round%2 == 0 {
			oldRead()
			newRead()
		} else {
			newRead()
			oldRead()
		}
		for _, accounts := range [][]subAccount{want, got} {
			if err := normalizeManagementAccounts(accounts); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatal("legacy account metadata, including zero/null fields, changed")
		}
		if !strings.Contains(rawTrace.String(), "stage=account_receipts ") || !strings.Contains(rawTrace.String(), "rows=605 ") ||
			!strings.Contains(projectedTrace.String(), "management_account_receipt_signals requested=605 resolved=605 ") ||
			strings.Contains(projectedTrace.String(), "stage=account_receipts ") || strings.Contains(projectedTrace.String(), "hydrate_receipt_signals") || request.used.Load() {
			t.Fatal("account summaries read raw payloads or consumed hydration-only observation", rawTrace.String(), projectedTrace.String())
		}
		t.Logf("local_account_receipts round=%d rows=605 raw_ms=%.3f projected_ms=%.3f", round, float64(oldTime.Microseconds())/1000, float64(newTime.Microseconds())/1000)
		for _, output := range []string{rawTrace.String(), projectedTrace.String()} {
			for _, line := range strings.Split(output, "\n") {
				if strings.Contains(line, "management_rows_read ") {
					t.Log(line)
				}
			}
		}
	}
}

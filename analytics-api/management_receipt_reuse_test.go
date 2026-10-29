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

type managementReceiptQueryRecorder struct {
	controlQueryDB
	ids [][]string
}

func (r *managementReceiptQueryRecorder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if query == `SELECT u.id,u.status,u.result FROM console_sub_usage u JOIN console_sub_accounts a ON a.id=u.account_id WHERE a.owner=$1 AND u.id=ANY($2)` {
		r.ids = append(r.ids, append([]string(nil), args[1].([]string)...))
	}
	return r.controlQueryDB.QueryContext(ctx, query, args...)
}

func managementReceiptAccounts() []subAccount {
	result := func(id string) *subResult {
		return &subResult{Availability: subProbe{ID: id, Usage: &subUsage{Status: "embedded"}}, Quality: subProbe{ID: "q-1-gpt-5.5"}}
	}
	return []subAccount{{ID: "a", Targets: []subTarget{{GroupID: 1, Result: result("a-1-gpt-5.5"),
		Models:     []subModelResult{{Model: "first", Result: result("a-1-gpt-5.5")}, {Model: "extra", Result: result("a-1-extra-model")}, {Model: "absent", Result: result("absent")}},
		ToolUse:    &subCapabilityResult{Attempts: []subProbe{{ID: "a-1-gpt-6-astra"}}},
		Compaction: &subCapabilityResult{Attempts: []subProbe{{ID: "q-1-extra-model"}}},
	}}}}
}

func TestManagementReceiptReuseReadsOnlyUnknownIDsAndPreservesFullSummary(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"status":null,"input_price":0,"output_price":null,"input_tokens":0,"output_tokens":9007199254740991,"actual_cost":1,"request_id":"private","message":"detail"}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, model := range []string{"", "gpt-5.5"} {
		reuse := &managementReceiptReuse{}
		_, _, err := readManagementScopedStreams(ctx, tx, "owner", true, managementStreamScope{Model: model, Names: []string{"gpt-5.5", "gpt-6-astra"}, ReceiptReuse: reuse})
		if err != nil || reuse.tx != tx || len(reuse.receipts) == 0 {
			t.Fatal("missing completed, pinned predicate evidence", err)
		}
		want, got := managementReceiptAccounts(), managementReceiptAccounts()
		if err := s.subAttachUsageFrom(ctx, tx, "owner", want); err != nil {
			t.Fatal(err)
		}
		recorder := &managementReceiptQueryRecorder{controlQueryDB: tx}
		if err := s.subAttachUsageWithCurrentFrom(ctx, recorder, "owner", got, reuse.current(tx, "owner")); err != nil {
			t.Fatal(err)
		}
		// Detail-only fields stay on the original reader. Compare the complete
		// final management representation, including legacy and attempt usage.
		if err := normalizeManagementAccounts(want); err != nil {
			t.Fatal(err)
		}
		if err := normalizeManagementAccounts(got); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("current receipt summary changed", model, err)
		}
		if len(recorder.ids) != 1 {
			t.Fatal("remaining IDs must use one atomic raw read")
		}
		for _, id := range recorder.ids[0] {
			if receipt, exists := reuse.receipts[id]; exists && receipt.valid {
				t.Fatal("a verified receipt was read again")
			}
		}
		wantIDs := 1 // absent is NOT treated as a verified missing source row.
		if model != "" {
			wantIDs += 2 // both extra-model roles were outside the scoped stream.
		}
		if len(recorder.ids[0]) != wantIDs {
			t.Fatal("unknown receipt scope changed", len(recorder.ids[0]), wantIDs)
		}
	}
}

func TestManagementReceiptReuseRequiresExactOwnerTransactionAndValidReceipt(t *testing.T) {
	ctx := context.Background()
	s := managementHydrationFixture(t)
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reuse := &managementReceiptReuse{}
	if _, _, err := readManagementScopedStreams(ctx, tx, "owner", true, managementStreamScope{ReceiptReuse: reuse}); err != nil {
		t.Fatal(err)
	}
	for _, db := range []controlQueryDB{s.control.db, (*sql.Tx)(nil), &sql.Tx{}} {
		if reuse.current(db, "owner") != nil {
			t.Fatal("reuse crossed a connection or transaction")
		}
	}
	if reuse.current(tx, "other") != nil || (*managementReceiptReuse)(nil).current(tx, "owner") != nil {
		t.Fatal("reuse crossed owner or missing evidence")
	}
	unpinned := &managementReceiptReuse{}
	unpinned.capture(s.control.db, "owner", reuse.receipts)
	if unpinned.current(tx, "owner") != nil {
		t.Fatal("pool evidence attributed to a later transaction")
	}
	r := reuse.receipts["a-1-gpt-5.5"]
	r.valid = false
	reuse.receipts["a-1-gpt-5.5"] = r
	if reuse.current(tx, "owner")("a-1-gpt-5.5") != nil {
		t.Fatal("unsupported receipt bypassed the raw decoder")
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"input_price":9}' WHERE id='a-1-gpt-6-astra'`); err != nil {
		t.Fatal(err)
	}
	// Later transactions cannot reuse the old snapshot, even for the same owner.
	next, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Rollback()
	want, got := managementReceiptAccounts(), managementReceiptAccounts()
	if err := s.subAttachUsageFrom(ctx, next, "owner", want); err != nil {
		t.Fatal(err)
	}
	if err := s.attachManagementPageUsageFrom(ctx, next, "owner", got, reuse); err != nil || !reflect.DeepEqual(got, want) || *got[0].Targets[0].ToolUse.Attempts[0].Usage.InputPrice != 9 {
		t.Fatal("later snapshot reused an older receipt", err)
	}
}

func TestManagementReceiptReuseMatchesRawDecoderBoundaries(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	for _, raw := range []string{
		`null`, `{}`, `{"status":null}`, `{"status":"","input_tokens":0,"input_price":0}`,
		`{"input_tokens":9223372036854775807,"output_tokens":-9223372036854775808}`,
		`{"input_price":1.7976931348623157e308,"output_price":5e-324}`,
		`{"Status":"matched","Input_Price":3}`, `{"input_tokens":1.0}`, `{"input_price":"bad"}`,
		`{"message":false}`, `{"actual_cost":1e1000}`, `[]`, `true`,
	} {
		if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET status='pending',result=$1 WHERE id='a-1-gpt-5.5'`, raw); err != nil {
			t.Fatal(err)
		}
		func() {
			tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			receipts, err := readManagementStreamReceipts(ctx, tx, "owner", []string{"a-1-gpt-5.5"}, false)
			if err != nil {
				t.Fatal(err)
			}
			reuse := &managementReceiptReuse{}
			reuse.capture(tx, "owner", receipts)
			makeInput := func() []subAccount {
				return []subAccount{{ID: "a", Targets: []subTarget{{GroupID: 1, Models: []subModelResult{{Model: "m", Result: &subResult{Availability: subProbe{ID: "a-1-gpt-5.5"}}}}}}}}
			}
			old, got := makeInput(), makeInput()
			wantErr := s.subAttachUsageFrom(ctx, tx, "owner", old)
			gotErr := s.attachManagementPageUsageFrom(ctx, tx, "owner", got, reuse)
			if (wantErr == nil) != (gotErr == nil) {
				t.Fatal("raw decoder failure changed", raw, wantErr, gotErr)
			}
			if wantErr != nil {
				if wantErr.Error() != gotErr.Error() || !reflect.DeepEqual(old, got) {
					t.Fatal("raw decoder error or partial publication changed", raw)
				}
				return
			}
			// Model normalization preserves all and only management usage fields.
			if err := normalizeManagementAccounts(old); err != nil {
				t.Fatal(err)
			}
			if err := normalizeManagementAccounts(got); err != nil || !reflect.DeepEqual(got, old) {
				t.Fatal("numeric/null summary changed", raw, err)
			}
		}()
	}
}

func TestManagementReceiptSignalReadTraceSeparatesDatabaseWaitAndDecode(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	_, err = readManagementStreamReceipts(managementTraceContext(ctx), tx, "owner", []string{"a-1-gpt-5.5"}, false)
	if err != nil {
		t.Fatal(err)
	}
	line := output.String()
	for _, field := range []string{"stage=receipt_signal_rows ", "query_ready_ms=", "decode_ms=", "elapsed_ms=", "rows=1 ", "bytes=", "advance_ms="} {
		if !strings.Contains(line, field) {
			t.Fatal("receipt read timing split missing", field, line)
		}
	}
}

func TestManagementReceiptReuseDoesNotPublishOnRawDecodeFailureOrCancellation(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"input_price":"invalid"}' WHERE id='a-1-extra-model'`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reuse := &managementReceiptReuse{}
	if _, _, err := readManagementScopedStreams(ctx, tx, "owner", false, managementStreamScope{Model: "gpt-5.5", Names: []string{"gpt-5.5", "gpt-6-astra"}, ReceiptReuse: reuse}); err != nil {
		t.Fatal(err)
	}
	got := managementReceiptAccounts()
	before := mustJSON(got)
	if err := s.attachManagementPageUsageFrom(ctx, tx, "owner", got, reuse); err == nil || mustJSON(got) != before {
		t.Fatal("raw failure published some reused receipts")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.attachManagementPageUsageFrom(canceled, tx, "owner", got, reuse); !errors.Is(err, context.Canceled) || mustJSON(got) != before {
		t.Fatal("canceled read published some reused receipts", err)
	}
	allKnown := []subAccount{{Targets: []subTarget{{Models: []subModelResult{{Result: &subResult{Availability: subProbe{ID: "a-1-gpt-5.5", Usage: &subUsage{Status: "embedded"}}}}}}}}}
	knownBefore := mustJSON(allKnown)
	if err := s.attachManagementPageUsageFrom(canceled, tx, "owner", allKnown, reuse); !errors.Is(err, context.Canceled) || mustJSON(allKnown) != knownBefore {
		t.Fatal("query-free reuse bypassed cancellation", err)
	}
	// An unsupported full stream must not publish reusable partial evidence.
	failed := &managementReceiptReuse{}
	if _, _, err := readManagementScopedStreams(ctx, tx, "owner", true, managementStreamScope{ReceiptReuse: failed}); !errors.Is(err, errManagementPredicateFallback) || failed.tx != nil {
		t.Fatal("failed stream retained partial evidence", err)
	}
}

func TestManagementPageReceiptReusePreservesSnapshotRowsContextAndWire(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	for _, model := range []string{"", "gpt-5.5", "extra-model", "missing-extra"} {
		t.Run(model, func(t *testing.T) {
			tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			f := managementQueryFilters{GroupStatus: "all", Model: model}
			read := func(reuse bool) (managementDatabaseView, managementQueryPage) {
				t.Helper()
				var receiptEvidence *managementReceiptReuse
				if reuse {
					receiptEvidence = &managementReceiptReuse{}
				}
				view, err := s.readManagementDatabaseModeWithReceiptsFrom(ctx, tx, "owner", nil, true, model, false, receiptEvidence)
				if err != nil {
					t.Fatal(err)
				}
				if !reuse {
					view.receiptReuse = nil
				}
				if err := assembleManagementGatewayView(&view, nil, "owner"); err != nil {
					t.Fatal(err)
				}
				rows, err := buildManagementQueryRows(view.Input, model, "")
				if err != nil {
					t.Fatal(err)
				}
				evidence := managementQueryEvidence(view.Input, rows, model, view.Prices)
				page, err := buildManagementQueryPageFromRows(view.Input, rows, f, evidence, "owner", s.control.key, "", view.ReadAt)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.hydrateManagementPageFrom(ctx, tx, "owner", &view, &page, f); err != nil || view.receiptReuse != nil {
					t.Fatal("hydration failed or retained private receipt evidence", err)
				}
				return view, page
			}
			before, want := read(false)
			after, got := read(true)
			if !reflect.DeepEqual(want, got) || mustJSON(before) != mustJSON(after) || mustJSON(managementContextForPage(before, want, model)) != mustJSON(managementContextForPage(after, got, model)) {
				t.Fatal("page contract changed")
			}
			oldWire, err := encodeManagementColumnPage(managementPageResponse{managementQueryPage: want})
			if err != nil {
				t.Fatal(err)
			}
			newWire, err := encodeManagementColumnPage(managementPageResponse{managementQueryPage: got})
			if err != nil || mustJSON(oldWire) != mustJSON(newWire) {
				t.Fatal("complete wire changed", err)
			}
		})
	}
}

func TestManagementReceiptReuseLocalReadMeasurement(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result)
 SELECT 'measure-'||g,'a',1,1,'measurement',0,'[]','matched',jsonb_build_object('status','matched','input_price',2,'output_price',3,
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
	ids := make([]string, 7000)
	var accounts []subAccount
	target := subTarget{GroupID: 1}
	for i := range ids {
		ids[i] = fmt.Sprintf("measure-%d", i+1)
		target.Models = append(target.Models, subModelResult{Model: ids[i], Result: &subResult{Availability: subProbe{ID: ids[i]}}})
	}
	accounts = []subAccount{{ID: "a", Targets: []subTarget{target}}}
	receipts, err := readManagementStreamReceipts(ctx, tx, "owner", ids, true)
	if err != nil || len(receipts) != len(ids) {
		t.Fatal("measurement input incomplete", err)
	}
	reuse := &managementReceiptReuse{}
	reuse.capture(tx, "owner", receipts)
	for round := 0; round < 3; round++ {
		for _, use := range []bool{false, true} {
			recorder := &managementReceiptQueryRecorder{controlQueryDB: tx}
			var current func(string) *subUsage
			if use {
				current = reuse.current(tx, "owner")
			}
			start := time.Now()
			for n := 0; n < 15; n++ {
				if err := s.subAttachUsageWithCurrentFrom(ctx, recorder, "owner", accounts, current); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("local_receipt_attachment round=%d reuse=%t mean_ms=%.3f queries=%d ids_per_query=%d", round, use, float64(time.Since(start).Microseconds())/15000, len(recorder.ids), len(ids))
			if use && len(recorder.ids) != 0 || !use && len(recorder.ids) != 15 {
				t.Fatal("redundant query elimination failed")
			}
		}
	}
}

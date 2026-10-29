package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type managementStreamReadHook struct {
	controlQueryDB
	beforeReceipts func()
}

func TestManagementModelReadTraceKeepsResultsAndPrivateValues(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	want, revision, err := readManagementSignalStreams(ctx, s.control.db, "owner", true)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for _, owner := range []string{"owner", "foreign"} {
		output.Reset()
		got, actual, err := readManagementSignalStreams(managementTraceContext(ctx), s.control.db, owner, true)
		if err != nil || (owner == "owner" && (actual != revision || !reflect.DeepEqual(got, want))) || (owner == "foreign" && len(got) != 0) {
			t.Fatal("model row tracing changed the owned snapshot", err)
		}
		logs := output.String()
		count := "rows=183 "
		if owner == "foreign" {
			count = "rows=0 "
		}
		for _, stage := range []string{"model_directory_rows", "model_signal_rows"} {
			var found string
			for _, line := range strings.Split(logs, "\n") {
				if strings.Contains(line, "management_rows_read stage="+stage+" ") {
					if found != "" {
						t.Fatal("model row span finished twice", stage)
					}
					found = line
				}
			}
			for _, field := range []string{count, "query_ready_ms=", "decode_ms=", "elapsed_ms=", "bytes=", "advance_ms=", "canceled=false"} {
				if !strings.Contains(found, field) {
					t.Fatal("missing model row metric", stage, field)
				}
			}
		}
		for _, private := range []string{"owner", "foreign", "gpt-5.5", "model message", "fixture", "https://a.test"} {
			if strings.Contains(logs, private) {
				t.Fatal("model row trace leaked private values")
			}
		}
	}
	output.Reset()
	if _, _, err := readManagementSignalStreams(ctx, s.control.db, "owner", true); err != nil || output.Len() != 0 {
		t.Fatal("untraced model reads logged diagnostics", err)
	}
}

// Measure only the new per-row counter work, not database/network latency.
func BenchmarkManagementModelDirectoryRowCounters(b *testing.B) {
	const rows = 190653
	for _, mode := range []string{"per_row_timer", "counter_only"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				trace := &managementRowsTrace{}
				for i := 0; i < rows; i++ {
					if mode == "per_row_timer" {
						started := time.Now()
						trace.decoded(started, 96)
					} else {
						trace.counted(96)
					}
				}
				if trace.rows != rows || trace.bytes != rows*96 {
					b.Fatal("row counters lost observations")
				}
			}
		})
	}
}

func TestManagementDerivedRowsTraceSeparatesAdvanceAndProcessing(t *testing.T) {
	if newManagementRowsTraceDerivedDecode(context.Background(), "model_directory_rows") != nil {
		t.Fatal("disabled trace allocated row metrics")
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	ctx, cancel := context.WithCancel(managementTraceContext(context.Background()))
	cancel()
	trace := newManagementRowsTraceDerivedDecode(ctx, "model_directory_rows")
	trace.start = time.Now().Add(-100 * time.Millisecond)
	trace.ready, trace.advance = 20*time.Millisecond, 30*time.Millisecond
	trace.counted(42)
	trace.finish()
	metrics := map[string]int64{}
	for _, part := range strings.Fields(output.String()) {
		key, value, ok := strings.Cut(part, "=")
		if ok {
			metrics[key], _ = strconv.ParseInt(value, 10, 64)
		}
	}
	processing := metrics["elapsed_ms"] - metrics["query_ready_ms"] - metrics["advance_ms"]
	if metrics["query_ready_ms"] != 20 || metrics["advance_ms"] != 30 || metrics["rows"] != 1 || metrics["bytes"] != 42 || metrics["decode_ms"] != processing || !strings.Contains(output.String(), "canceled=true") {
		t.Fatal("derived processing timing lost row or cancellation evidence")
	}
}

func (d managementStreamReadHook) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if query == managementReceiptStreamSQL || query == managementReceiptSelectedStreamSQL {
		d.beforeReceipts()
	}
	return d.controlQueryDB.QueryContext(ctx, query, args...)
}

func TestManagementStreamsMatchJoinsAndSnapshot(t *testing.T) {
	s := managementHydrationFixture(t)
	db, ctx := s.control.db, context.Background()
	exec := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	compare := func(owner string) string {
		t.Helper()
		want, revision, err := readManagementJoinedSignalsAndRevision(ctx, db, owner)
		if err != nil {
			t.Fatal(err)
		}
		got, actual, err := readManagementSignalStreams(ctx, db, owner, true)
		if err != nil || actual != revision || !reflect.DeepEqual(got, want) {
			t.Fatal("streamed predicates/revision differ", err)
		}
		return actual
	}
	exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES('unrelated','a',1,0,'ignored',0,'[]','pending','{"input_price":"invalid"}')`)
	compare("owner")
	compare("foreign")
	for _, owner := range []string{"owner", "foreign"} {
		for _, catalog := range []bool{false, true} {
			wantRevision, want, err := readManagementJoinedRevisionCatalogFrom(ctx, db, owner, catalog)
			gotRevision, got, actualErr := readManagementRevisionCatalogFrom(ctx, db, owner, catalog)
			if err != nil || actualErr != nil || wantRevision != gotRevision || !reflect.DeepEqual(want, got) {
				t.Fatal("catalog revision compatibility", err, actualErr)
			}
		}
	}
	for _, q := range []string{
		`UPDATE console_sub_models SET model='Unicode雪<&' WHERE model='extra-model'`,
		`UPDATE console_sub_models SET message='changed diagnostic',state='running' WHERE group_id=61`,
		`UPDATE console_sub_usage SET result='{"status":null,"input_price":0,"output_tokens":0}' WHERE id='q-61-gpt-5.5'`,
		`UPDATE console_sub_usage SET result='null' WHERE id='q-60-gpt-5.5'`,
		`DELETE FROM console_sub_usage WHERE id='q-59-gpt-5.5'`,
		`UPDATE console_sub_models SET result=jsonb_set(result,'{quality,id}','"q-61-gpt-5.5"') WHERE group_id=1`,
		`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('cross','owner','X','https://cross.test','x',''),('foreign','foreign','F','https://foreign.test','f','')`,
		`UPDATE console_sub_usage SET account_id='cross' WHERE id='q-61-gpt-5.5'`,
		`UPDATE console_sub_usage SET account_id='foreign' WHERE id='q-60-gpt-5.5'`,
	} {
		exec(q)
		compare("owner")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	want, revision, err := readManagementJoinedSignalsAndRevision(ctx, tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	hook := managementStreamReadHook{controlQueryDB: tx, beforeReceipts: func() {
		exec(`UPDATE console_sub_usage SET result='{"input_price":123}' WHERE id='q-61-gpt-5.5';UPDATE console_sub_models SET message='new snapshot' WHERE group_id=1`)
	}}
	got, actual, err := readManagementSignalStreams(ctx, hook, "owner", true)
	if err != nil || actual != revision || !reflect.DeepEqual(got, want) {
		t.Fatal("concurrent writer mixed snapshots", err)
	}
	if compare("owner") == revision {
		t.Fatal("fresh snapshot missed committed writer")
	}
	for _, tc := range []struct{ bad, restore string }{
		{`UPDATE console_sub_usage SET result='{"input_price":"invalid"}' WHERE id='q-61-gpt-5.5'`, `UPDATE console_sub_usage SET result='{"input_price":0}' WHERE id='q-61-gpt-5.5'`},
		{`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','"invalid"') WHERE group_id=2`, `UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','0') WHERE group_id=2`},
		{`DELETE FROM console_management_model_signal WHERE group_id=3`, `UPDATE console_sub_models SET result=result WHERE group_id=3`},
	} {
		exec(tc.bad)
		if rows, revision, err := readManagementSignalStreams(ctx, db, "owner", true); !errors.Is(err, errManagementPredicateFallback) || rows != nil || revision != "" {
			t.Fatal("unsupported evidence partially published", err)
		}
		exec(tc.restore)
		compare("owner")
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if rows, revision, err := readManagementSignalStreams(canceled, db, "owner", true); err == nil || rows != nil || revision != "" {
		t.Fatal("canceled stream published")
	}
}

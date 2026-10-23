package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Synthetic evidence only, explicitly opt-in on the local integration cluster.
// This measures a candidate predicate read, not HTTP/page/browser acceptance.
func TestManagementModelSignalsProductionCardinality(t *testing.T) {
	if os.Getenv("RUN_MANAGEMENT_SIGNALS_SCALE") != "1" {
		t.Skip("opt-in synthetic scale fixture")
	}
	s := managementBindingTestService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	exec := func(q string) {
		t.Helper()
		if _, err := s.control.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	exec(managementPredicatePrototypeSQL)
	exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) SELECT 'a'||a,'owner','fixture','https://fixture.test',a::text,'' FROM generate_series(0,47) a`)
	exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) SELECT 'a'||(g%48),g,'group','openai' FROM generate_series(1,749) g`)
	exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result)
 SELECT 'a'||(g%48),g,'m'||lpad(m::text,3,'0'),CASE WHEN m%9=0 THEN 'queued' ELSE 'done' END,
 jsonb_build_object('model','m'||lpad(m::text,3,'0'),'checked_at',m,'verdict','pass',
 'availability',jsonb_build_object('id','probe-'||g||'-'||m,'status',CASE WHEN m%3=0 THEN 'error' ELSE 'success' END,
 'terminal_status','complete','model_match','match','protocol','responses','ttft_ms',123,'duration_ms',256,
 'requested_model','m'||m,'response_model','m'||m,'http_status',200,'message',repeat('diagnostic ',12),
 'usage',jsonb_build_object('status','pending','input_price',2,'output_price',10,'input_tokens',100,'output_tokens',20)),
 'quality',jsonb_build_object('status','skipped'))
 FROM generate_series(1,749) g CROSS JOIN generate_series(1,160) m`)
	exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result)
 SELECT 'probe-'||g||'-'||m,'a'||(g%48),g,0,'m'||lpad(m::text,3,'0'),0,'[]','matched',
 jsonb_build_object('status','matched','input_price',3,'output_price',15,'input_tokens',100,'output_tokens',20,'message',repeat('receipt ',40))
 FROM generate_series(1,749) g CROSS JOIN generate_series(1,160) m WHERE m%2=0`)
	exec(`ANALYZE console_sub_accounts`)
	exec(`ANALYZE console_sub_targets`)
	exec(`ANALYZE console_sub_models`)
	exec(`ANALYZE console_sub_model_summaries`)
	exec(`ANALYZE console_sub_usage`)
	var projectionBytes int64
	if err := s.control.db.QueryRowContext(ctx, `SELECT pg_total_relation_size('management_model_signal')+pg_total_relation_size('management_receipt_signal')`).Scan(&projectionBytes); err != nil {
		t.Fatal(err)
	}
	accounts := make([]subAccount, 48)
	for i := range accounts {
		accounts[i] = subAccount{ID: fmt.Sprint("a", i), Targets: []subTarget{}}
	}
	for group := 1; group <= 749; group++ {
		a := &accounts[group%48]
		a.Targets = append(a.Targets, subTarget{GroupID: int64(group)})
	}
	start := time.Now()
	count, bytes, err := s.subReadModelsFrom(ctx, s.control.db, "owner", "", 0, "", true, accounts)
	if err != nil || count != 119840 {
		t.Fatal(count, err)
	}
	if err := s.subAttachUsageFrom(ctx, s.control.db, "owner", accounts); err != nil {
		t.Fatal(err)
	}
	oldElapsed := time.Since(start)
	expected := map[string]subModelResult{}
	for _, a := range accounts {
		for _, target := range a.Targets {
			for _, m := range target.Models {
				expected[mustJSON([]any{a.ID, target.GroupID, m.Model})] = subModelResult{Model: m.Model, State: m.State, Result: managementPredicateResult(m.Result)}
			}
		}
	}
	start = time.Now()
	got, signalBytes, err := readManagementModelSignalsFrom(ctx, s.control.db, "owner")
	newElapsed := time.Since(start)
	if err != nil || len(got) != count {
		t.Fatal(len(got), err)
	}
	start = time.Now()
	typed, err := readManagementModelSignalsTypedFrom(ctx, s.control.db, "owner")
	typedElapsed := time.Since(start)
	if err != nil || !reflect.DeepEqual(typed, got) {
		t.Fatal("typed result differs", err)
	}
	start = time.Now()
	transactional, err := readManagementSignalsColumns(ctx, s.control.db, "owner", managementModelSignalsStoredSQL)
	transactionalElapsed := time.Since(start)
	if err != nil || !reflect.DeepEqual(transactional, got) {
		t.Fatal("transactional typed join changed predicates", err)
	}
	// Isolate read cost from JSON extraction. This table is a disposable local
	// statement snapshot, not a production projection or a valid current cache.
	projectionSQL := strings.ReplaceAll(managementModelSignalsTypedSQL, "$1", "'owner'")
	exec(`CREATE TABLE management_signal_fixture(account_id,group_id,model,state,valid,present,checked_at,verdict,availability,terminal,model_match,usage_status,input_price,output_price,input_tokens,output_tokens,has_usage) AS ` + projectionSQL)
	exec(`ANALYZE management_signal_fixture`)
	start = time.Now()
	storedRows, err := s.control.db.QueryContext(ctx, `SELECT account_id,group_id,model,state,valid,present,checked_at,verdict,availability,terminal,model_match,usage_status,input_price,output_price,input_tokens,output_tokens,has_usage FROM management_signal_fixture ORDER BY account_id,group_id,model`)
	if err != nil {
		t.Fatal(err)
	}
	stored := []managementModelSignals{}
	for storedRows.Next() {
		var item managementModelSignals
		var valid, present, hasUsage bool
		r := &subResult{}
		u := &subUsage{}
		if err := storedRows.Scan(&item.Account, &item.Group, &item.Model.Model, &item.Model.State, &valid, &present, &r.CheckedAt, &r.Verdict, &r.Availability.Status, &r.Availability.TerminalStatus, &r.Availability.ModelMatch, &u.Status, &u.InputPrice, &u.OutputPrice, &u.InputTokens, &u.OutputTokens, &hasUsage); err != nil {
			t.Fatal(err)
		}
		if !valid {
			t.Fatal("invalid synthetic value")
		}
		if present {
			item.Model.Result = r
			if hasUsage {
				r.Availability.Usage = u
			}
		}
		stored = append(stored, item)
	}
	if err := storedRows.Err(); err != nil {
		t.Fatal(err)
	}
	storedRows.Close()
	storedElapsed := time.Since(start)
	if !reflect.DeepEqual(stored, got) {
		t.Fatal("stored columns changed predicates")
	}
	start = time.Now()
	aggregates, err := s.control.db.QueryContext(ctx, `SELECT account_id,group_id,count(*),count(*) FILTER(WHERE availability='success'),count(*) FILTER(WHERE availability='error'),count(*) FILTER(WHERE NOT present),count(*) FILTER(WHERE state IN ('queued','running')),count(*) FILTER(WHERE state='interrupted'),max(checked_at) FROM management_signal_fixture GROUP BY account_id,group_id`)
	if err != nil {
		t.Fatal(err)
	}
	type groupSignal struct{ Total, Success, Failed, Untested, Pending, Interrupted, Latest int64 }
	wantGroups := map[string]groupSignal{}
	for _, item := range got {
		key := qualityGroupID(item.Account, item.Group)
		v := wantGroups[key]
		v.Total++
		if item.Model.Result == nil {
			v.Untested++
		} else {
			r := item.Model.Result
			if r.Availability.Status == "success" {
				v.Success++
			}
			if r.Availability.Status == "error" {
				v.Failed++
			}
			v.Latest = max(v.Latest, r.CheckedAt)
		}
		if managementPending(item.Model.State) {
			v.Pending++
		}
		if item.Model.State == "interrupted" {
			v.Interrupted++
		}
		wantGroups[key] = v
	}
	aggregateCount := 0
	for aggregates.Next() {
		var a string
		var g int64
		var v groupSignal
		if err := aggregates.Scan(&a, &g, &v.Total, &v.Success, &v.Failed, &v.Untested, &v.Pending, &v.Interrupted, &v.Latest); err != nil {
			t.Fatal(err)
		}
		key := qualityGroupID(a, g)
		if v != wantGroups[key] {
			t.Fatal("aggregate differs", key)
		}
		delete(wantGroups, key)
		aggregateCount++
	}
	if err := aggregates.Err(); err != nil {
		t.Fatal(err)
	}
	aggregates.Close()
	aggregateElapsed := time.Since(start)
	if len(wantGroups) != 0 || aggregateCount != 749 {
		t.Fatal("aggregate scope incomplete")
	}
	for _, item := range got {
		key := mustJSON([]any{item.Account, item.Group, item.Model.Model})
		if !reflect.DeepEqual(item.Model, expected[key]) {
			t.Fatal("predicate changed", key)
		}
		delete(expected, key)
	}
	if len(expected) != 0 {
		t.Fatal("missing predicate rows")
	}
	result := map[string]any{"models": count, "shadow_projection_bytes": projectionBytes, "summary_result_bytes": bytes, "predicate_bytes": signalBytes, "old_model_and_receipt_ms": oldElapsed.Milliseconds(), "predicate_join_ms": newElapsed.Milliseconds(), "typed_predicate_join_ms": typedElapsed.Milliseconds(), "transactional_typed_join_ms": transactionalElapsed.Milliseconds(), "stored_typed_read_ms": storedElapsed.Milliseconds(), "stored_aggregate_read_and_compare_ms": aggregateElapsed.Milliseconds(), "aggregate_groups": aggregateCount, "all_predicates_equal": true}
	raw, _ := json.Marshal(result)
	t.Log(string(raw))
}

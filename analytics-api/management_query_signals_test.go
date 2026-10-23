package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func projectManagementFixture(in managementQueryInput) managementQueryInput {
	var out managementQueryInput
	_ = json.Unmarshal([]byte(mustJSON(in)), &out)
	for ai := range out.Accounts {
		for ti := range out.Accounts[ai].Targets {
			for mi := range out.Accounts[ai].Targets[ti].Models {
				m := &out.Accounts[ai].Targets[ti].Models[mi]
				m.Message, m.Result = "", managementPredicateResult(m.Result)
			}
		}
	}
	return out
}

func TestManagementPredicateProjectionPreservesBrowserFilterContract(t *testing.T) {
	var fixture struct {
		Input managementQueryInput `json:"input"`
		Cases []struct {
			Filters  managementQueryFilters   `json:"filters"`
			Evidence managementFilterEvidence `json:"evidence"`
			Expected struct {
				IDs       []string  `json:"ids"`
				Rates     []float64 `json:"rates"`
				Platforms []string  `json:"platforms"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(managementContractFixture(t, "testdata/management-query-filters.json.gz"), &fixture); err != nil {
		t.Fatal(err)
	}
	projected := projectManagementFixture(fixture.Input)
	for _, c := range fixture.Cases {
		rows, err := buildManagementQueryRows(projected, c.Filters.Model, c.Filters.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		filtered := filterManagementQueryRows(rows, c.Filters, c.Evidence)
		ids := []string{}
		for _, row := range filtered.Rows {
			ids = append(ids, row.ID)
		}
		if !reflect.DeepEqual(ids, c.Expected.IDs) || !reflect.DeepEqual(filtered.Rates, c.Expected.Rates) || !reflect.DeepEqual(filtered.Platforms, c.Expected.Platforms) {
			t.Fatalf("predicate projection changed filters %s: ids=%v want=%v", mustJSON(c.Filters), ids, c.Expected.IDs)
		}
		fullRows, err := buildManagementQueryRows(fixture.Input, c.Filters.Model, c.Filters.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		fullPage, err := buildManagementQueryPageFromRows(fixture.Input, fullRows, c.Filters, c.Evidence, "owner", []byte("key"), "", time.Unix(1, 0))
		if err != nil {
			t.Fatal(err)
		}
		page, err := buildManagementQueryPageFromRows(projected, rows, c.Filters, c.Evidence, "owner", []byte("key"), "", time.Unix(1, 0))
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != fullPage.Total || page.EligibleCount != fullPage.EligibleCount || !reflect.DeepEqual(page.Models, fullPage.Models) || !reflect.DeepEqual(page.DetectionModels, fullPage.DetectionModels) {
			t.Fatal("compact predicates truncated facet or full-selection scope")
		}
	}
}

func TestManagementModelSignalsSQLMatchesCurrentReceiptsAndSnapshot(t *testing.T) {
	s := managementBindingTestService(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.control.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('a','owner','A','https://a.test','a',''),('b','owner','B','https://b.test','b',''),('x','other','X','https://x.test','x','')`)
	exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('a',1,'G','openai'),('x',1,'Foreign','openai')`)
	embedded := &subUsage{Status: "pending", InputPrice: usageFloat(1), OutputPrice: usageFloat(2), InputTokens: usageInt(3), OutputTokens: usageInt(4)}
	for _, name := range []string{"current", "absent", "foreign", "same-owner", "zero", "null-receipt", "null-status", "nil", "empty"} {
		var result any = subResult{Model: name, CheckedAt: 42, Verdict: "pass", Availability: subProbe{ID: name, Status: "success", ModelMatch: "match", TerminalStatus: "missing", Text: "private reply", Usage: embedded}}
		if name == "nil" {
			result = nil
		}
		if name == "empty" {
			result = map[string]any{}
		}
		exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES('a',1,$1,'done',$2)`, name, mustJSON(result))
	}
	exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES('x',1,'private','done','{}')`)
	for _, receipt := range []struct{ id, account, status, result string }{
		{"current", "a", "matched", `{"status":"matched","input_price":9,"output_price":10,"input_tokens":11,"output_tokens":12}`},
		{"same-owner", "b", "matched", `{"input_price":5}`},
		{"foreign", "x", "matched", `{"input_price":999}`},
		{"zero", "a", "matched", `{"input_price":0,"output_price":0,"input_tokens":0,"output_tokens":0}`},
		{"null-receipt", "a", "pending", `null`},
		{"null-status", "a", "matched", `{"status":null,"input_price":0}`},
	} {
		exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES($1,$2,1,0,'m',1,'[]',$3,$4)`, receipt.id, receipt.account, receipt.status, receipt.result)
	}
	// Missing summary uses the same projection fallback instead of dropping a row.
	exec(`DELETE FROM console_sub_model_summaries WHERE model='absent'`)
	compare := func(db controlQueryDB) {
		t.Helper()
		accounts := []subAccount{{ID: "a", Targets: []subTarget{{GroupID: 1}}}}
		if _, _, err := s.subReadModelsFrom(ctx, db, "owner", "", 0, "", true, accounts); err != nil {
			t.Fatal(err)
		}
		if err := s.subAttachUsageFrom(ctx, db, "owner", accounts); err != nil {
			t.Fatal(err)
		}
		got, _, err := readManagementModelSignalsFrom(ctx, db, "owner")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(accounts[0].Targets[0].Models) {
			t.Fatal("owner/model scope changed")
		}
		typed, err := readManagementModelSignalsTypedFrom(ctx, db, "owner")
		if err != nil || !reflect.DeepEqual(typed, got) {
			t.Fatal("typed projection diverged", err)
		}
		for i, m := range accounts[0].Targets[0].Models {
			want := managementModelSignals{Account: "a", Group: 1, Model: subModelResult{Model: m.Model, State: m.State, Result: managementPredicateResult(m.Result)}}
			if !reflect.DeepEqual(got[i], want) {
				t.Fatalf("projection mismatch for %s: got=%s want=%s", m.Model, mustJSON(got[i]), mustJSON(want))
			}
		}
	}
	compare(s.control.db)
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before, _, err := readManagementModelSignalsFrom(ctx, tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE console_sub_usage SET result='{"input_price":15}' WHERE id='current'`)
	exec(`UPDATE console_sub_models SET state='running' WHERE model='current'`)
	compare(tx)
	after, _, err := readManagementModelSignalsFrom(ctx, tx, "owner")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("snapshot mixed concurrent changes", err)
	}
	compare(s.control.db)
	fresh, _, _ := readManagementModelSignalsFrom(ctx, s.control.db, "owner")
	if reflect.DeepEqual(before, fresh) {
		t.Fatal("new snapshot missed committed change")
	}
	exec(`UPDATE console_sub_usage SET result='{"input_price":"invalid"}' WHERE id='zero'`)
	if data, _, err := readManagementModelSignalsFrom(ctx, s.control.db, "owner"); err == nil || data != nil {
		t.Fatal("invalid receipt published partial predicates")
	}
	if data, err := readManagementModelSignalsTypedFrom(ctx, s.control.db, "owner"); err == nil || data != nil {
		t.Fatal("typed projection accepted malformed receipt")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if data, _, err := readManagementModelSignalsFrom(canceled, s.control.db, "owner"); err == nil || data != nil {
		t.Fatal("canceled read published predicates")
	}
}

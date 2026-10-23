package main

import (
	"context"
	"database/sql"
	_ "embed"
	"reflect"
	"testing"
)

//go:embed testdata/management-predicate-prototype.sql
var managementPredicatePrototypeSQL string

// This prototype query assumes both shadow tables have been completely
// prepared. That is intentionally NOT a production readiness contract yet.
const managementPrototypeSignalsSQL = `SELECT m.account_id,m.group_id,m.model,m.state,
 COALESCE(p.valid,false) AND COALESCE(r.valid,true),p.present,p.checked_at,p.verdict,p.availability,p.terminal,p.model_match,
 CASE WHEN r.id IS NOT NULL THEN r.status ELSE p.usage_status END,
 CASE WHEN r.id IS NOT NULL THEN r.input_price ELSE p.input_price END,
 CASE WHEN r.id IS NOT NULL THEN r.output_price ELSE p.output_price END,
 CASE WHEN r.id IS NOT NULL THEN r.input_tokens ELSE p.input_tokens END,
 CASE WHEN r.id IS NOT NULL THEN r.output_tokens ELSE p.output_tokens END,
 r.id IS NOT NULL OR p.has_usage
 FROM console_sub_models m JOIN console_sub_accounts a ON a.id=m.account_id
 LEFT JOIN management_model_signal p USING(account_id,group_id,model)
 LEFT JOIN management_receipt_signal r ON r.id=NULLIF(p.probe_id,'')
 AND EXISTS(SELECT 1 FROM console_sub_accounts ra WHERE ra.id=r.account_id AND ra.owner=$1)
 WHERE a.owner=$1 ORDER BY m.account_id,m.group_id,m.model`

func TestManagementShadowPredicatesFollowIndependentWriterTransactions(t *testing.T) {
	s := managementBindingTestService(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.control.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(managementPredicatePrototypeSQL)
	exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('a','owner','A','https://a.test','a',''),('b','foreign','B','https://b.test','b','')`)
	exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('a',1,'G','openai'),('b',1,'X','openai')`)
	result := `{"checked_at":1,"verdict":"pass","availability":{"id":"probe","status":"success","model_match":"match","usage":{"status":"pending","input_price":1}}}`
	exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES('a',1,'m','done',$1)`, result)
	check := func(db controlQueryDB) {
		t.Helper()
		want, _, err := readManagementModelSignalsFrom(ctx, db, "owner")
		if err != nil {
			t.Fatal(err)
		}
		got, err := readManagementSignalsColumns(ctx, db, "owner", managementPrototypeSignalsSQL)
		if err != nil || !reflect.DeepEqual(want, got) {
			t.Fatalf("shadow divergence err=%v want=%s got=%s", err, mustJSON(want), mustJSON(got))
		}
	}
	check(s.control.db)
	exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES('probe','a',1,0,'m',0,'[]','matched','{"input_price":3,"output_price":0,"input_tokens":0}')`)
	check(s.control.db)
	reader, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	before, err := readManagementSignalsColumns(ctx, reader, "owner", managementPrototypeSignalsSQL)
	if err != nil {
		t.Fatal(err)
	}
	// Both statements emulate writers unaware of the new projection tables.
	modelWriter, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer modelWriter.Rollback()
	if _, err = modelWriter.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{checked_at}','2') WHERE account_id='a'`); err != nil {
		t.Fatal(err)
	}
	receiptWriter, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiptWriter.Rollback()
	if _, err = receiptWriter.Exec(`UPDATE console_sub_usage SET result='{"input_price":7}' WHERE id='probe'`); err != nil {
		t.Fatal(err)
	}
	if err = receiptWriter.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = modelWriter.Commit(); err != nil {
		t.Fatal(err)
	}
	check(reader)
	check(s.control.db)
	old, err := readManagementSignalsColumns(ctx, reader, "owner", managementPrototypeSignalsSQL)
	if err != nil || !reflect.DeepEqual(before, old) {
		t.Fatal("repeatable read changed", err)
	}
	if err = reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	rollback, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rollback.Exec(`UPDATE console_sub_usage SET result='{"input_price":99}' WHERE id='probe'`); err != nil {
		t.Fatal(err)
	}
	if err = rollback.Rollback(); err != nil {
		t.Fatal(err)
	}
	check(s.control.db)
	// Owner changes and raw receipt FIFO cannot reveal stale projected values.
	exec(`UPDATE console_sub_usage SET account_id='b' WHERE id='probe'`)
	check(s.control.db)
	exec(`UPDATE console_sub_usage SET account_id='a' WHERE id='probe'`)
	check(s.control.db)
	exec(`DELETE FROM console_sub_usage WHERE id='probe'`)
	check(s.control.db)
	exec(`UPDATE console_sub_models SET state='running' WHERE account_id='a'`)
	check(s.control.db)
	exec(`UPDATE console_sub_models SET result=NULL WHERE account_id='a'`)
	check(s.control.db)
	// A receipt may arrive before its model. There is no link-creation race.
	exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES('probe','a',1,0,'m',0,'[]','matched','{"input_price":5}')`)
	exec(`UPDATE console_sub_models SET result=$1 WHERE account_id='a'`, result)
	check(s.control.db)
	exec(`UPDATE console_sub_usage SET result='{"input_price":"bad"}' WHERE id='probe'`)
	if rows, err := readManagementSignalsColumns(ctx, s.control.db, "owner", managementPrototypeSignalsSQL); err == nil || rows != nil {
		t.Fatal("invalid receipt published predicates")
	}
	exec(`UPDATE console_sub_usage SET result='{"input_price":0}' WHERE id='probe'`)
	check(s.control.db)
	// Invalid legacy JSON remains stored, but cannot become a valid predicate.
	// Do not round token counts or silently accept quoted numbers.
	for _, raw := range []string{`[]`, `{"status":false}`, `{"input_price":"3"}`, `{"input_price":1e400}`, `{"input_tokens":1.5}`, `{"output_tokens":9223372036854775808}`} {
		exec(`UPDATE console_sub_usage SET result=$1 WHERE id='probe'`, raw)
		if rows, err := readManagementSignalsColumns(ctx, s.control.db, "owner", managementPrototypeSignalsSQL); err == nil || rows != nil {
			t.Fatal("invalid receipt was coerced", raw)
		}
	}
	exec(`UPDATE console_sub_usage SET result='null',status='pending' WHERE id='probe'`)
	check(s.control.db)
	for _, raw := range []string{`[]`, `{"checked_at":"3"}`, `{"checked_at":1.5}`, `{"verdict":false}`, `{"availability":true}`, `{"availability":{"status":3}}`, `{"availability":{"id":7}}`, `{"availability":{"usage":{"input_price":"2"}}}`} {
		exec(`UPDATE console_sub_models SET result=$1 WHERE account_id='a'`, raw)
		if rows, err := readManagementSignalsColumns(ctx, s.control.db, "owner", managementPrototypeSignalsSQL); err == nil || rows != nil {
			t.Fatal("invalid model was coerced", raw)
		}
	}
	exec(`UPDATE console_sub_models SET result=$1 WHERE account_id='a'`, result)
	check(s.control.db)
	exec(`DELETE FROM console_sub_models WHERE account_id='a'`)
	check(s.control.db)
	var retained int
	if err = s.control.db.QueryRow(`SELECT count(*) FROM management_model_signal`).Scan(&retained); err != nil || retained != 0 {
		t.Fatal("deleted models retained", err, retained)
	}
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func prepareManagementRevisionsForTest(t *testing.T, db *sql.DB) {
	t.Helper()
	for i := 0; i < 5; i++ {
		name, err := prepareManagementRevisionState(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		if name == "" {
			return
		}
	}
	t.Fatal("revision preparation did not finish")
}

func TestManagementRevisionStateTracksCurrentTransactions(t *testing.T) {
	s := managementHydrationFixture(t)
	db, ctx := s.control.db, context.Background()
	if _, ok, err := readManagementCompactRevision(ctx, db, "owner"); err != nil || ok {
		t.Fatal("unprepared counters activated", err)
	}
	prepareManagementRevisionsForTest(t, db)
	stamp := func(q controlQueryDB) string {
		t.Helper()
		v, ok, err := readManagementCompactRevision(ctx, q, "owner")
		if err != nil || !ok {
			t.Fatal("current revision unavailable", err)
		}
		return v
	}
	exec := func(query string) {
		t.Helper()
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	before := stamp(db)
	var xmin string
	if err := db.QueryRow(`SELECT xmin::text FROM console_management_receipt_signal WHERE id='q-1-gpt-5.5'`).Scan(&xmin); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE console_sub_usage SET result=jsonb_set(result,'{checked_at}','10000'),next_attempt=now() WHERE id='q-1-gpt-5.5'`)
	var afterXmin string
	if err := db.QueryRow(`SELECT xmin::text FROM console_management_receipt_signal WHERE id='q-1-gpt-5.5'`).Scan(&afterXmin); err != nil {
		t.Fatal(err)
	}
	if stamp(db) != before || xmin != afterXmin {
		t.Fatal("poll clock rewrote semantic projection")
	}
	for _, query := range []string{
		`UPDATE console_sub_models SET message='new' WHERE group_id=1`,
		`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','987') WHERE model='extra-model'`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{input_price}','7') WHERE id='q-1-gpt-5.5'`,
		`INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES('a',1,'model','gpt-5.5')`,
		`DELETE FROM console_sub_models WHERE group_id=61`,
		`DELETE FROM console_sub_usage WHERE id='q-1-gpt-5.5'`,
	} {
		exec(query)
		now := stamp(db)
		if now == before {
			t.Fatal("change did not fence revision", query)
		}
		before = now
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	old := stamp(tx)
	exec(`UPDATE console_sub_models SET state='running' WHERE group_id=1`)
	if stamp(tx) != old || stamp(db) == old {
		t.Fatal("version did not follow snapshot")
	}
	write, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	before = stamp(db)
	if _, err = write.Exec(`UPDATE console_sub_models SET state='error' WHERE group_id=1`); err != nil {
		t.Fatal(err)
	}
	write.Rollback()
	if stamp(db) != before {
		t.Fatal("rollback changed version")
	}
	exec(`DELETE FROM console_management_model_signal WHERE group_id=1`)
	if _, ok, err := readManagementCompactRevision(ctx, db, "owner"); err != nil || ok {
		t.Fatal("missing projection accepted", err)
	}
	exec(`UPDATE console_sub_models SET result=result WHERE group_id=1`)
	stamp(db)
	exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','"bad"') WHERE model='extra-model'`)
	if _, ok, err := readManagementCompactRevision(ctx, db, "owner"); err != nil || ok {
		t.Fatal("invalid projection accepted", err)
	}
	if _, _, err := readManagementScopedSignals(ctx, db, "owner", "gpt-5.5", []string{"gpt-5.5"}); !errors.Is(err, errManagementPredicateFallback) {
		t.Fatal("offscope error hidden", err)
	}
	exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','123') WHERE model='extra-model'`)
	stamp(db)
	for _, f := range []managementQueryFilters{{GroupStatus: "all"}, {GroupStatus: "available", Model: "gpt-5.5"}, {GroupStatus: "all", Model: "extra-model", Availability: "success"}, {GroupStatus: "all", Availability: "error"}} {
		page, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
		if err != nil {
			t.Fatal(err)
		}
		full, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", true)
		if err != nil {
			t.Fatal(err)
		}
		page.Page.ReadAtMS = full.Page.ReadAtMS
		if page.Page.Revision != full.Page.Revision || !reflect.DeepEqual(page.Page, full.Page) {
			t.Fatal("compact revision/page differs from complete selection", f)
		}
	}
}

func TestManagementRevisionStateAdoptsLegacyAndCascades(t *testing.T) {
	db := migrationTestDB(t)
	ctx := context.Background()
	all := controlMigrations()
	index := -1
	for i, m := range all {
		if m.Version == "0009_management_revisions" {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("revision migration missing")
	}
	if err := migrateControlSchema(ctx, db, all[:index]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('a','owner','A','https://a.test','a','');INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('a',1,'G','openai');INSERT INTO console_sub_models(account_id,group_id,model,state,result) SELECT 'a',1,'m'||g,'done','{}'::jsonb FROM generate_series(1,100) g;INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) SELECT CASE WHEN g=1 THEN '' WHEN g=2 THEN '雪' ELSE 'p'||g END,'a',1,0,'m',1,'[]','pending','{}' FROM generate_series(1,1300) g`); err != nil {
		t.Fatal(err)
	}
	if err := migrateControlSchema(ctx, db, all); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM console_sub_models WHERE model='m1';UPDATE console_sub_models SET state='running' WHERE model='m2';DELETE FROM console_sub_usage WHERE id='p1'`); err != nil {
		t.Fatal(err)
	}
	prepareManagementRevisionsForTest(t, db)
	for _, relation := range []string{"console_sub_models", "console_management_model_signal", "console_sub_usage", "console_management_receipt_signal"} {
		var actual, count int64
		if err := db.QueryRow(`SELECT count(*) FROM ` + relation).Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT rows_base+rows_delta FROM console_management_revision_state WHERE relation_name=$1`, relation).Scan(&count); err != nil || actual != count {
			t.Fatal("legacy count differs", relation, actual, count, err)
		}
	}
	if _, ok, err := readManagementCompactRevision(ctx, db, "owner"); err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := db.Exec(`TRUNCATE console_sub_models,console_sub_usage CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := readManagementCompactRevision(ctx, db, "owner"); err != nil || !ok {
		t.Fatal("truncate counter lost coherence", err)
	}
}

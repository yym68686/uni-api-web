package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

func TestCapabilityProjectionPreparationAndWriters(t *testing.T) {
	db := migrationTestDB(t)
	ctx := context.Background()
	migrations := controlMigrations()
	index := -1
	for i, m := range migrations {
		if m.Version == "0008_capability_projection" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("capability projection migration missing")
	}
	if err := migrateControlSchema(ctx, db, migrations[:index]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('a','owner','A','https://a.test','a','');INSERT INTO console_sub_targets(account_id,group_id,name,platform,tool_use) SELECT 'a',g,'G','openai',jsonb_build_object('status','success','attempts',jsonb_build_array(jsonb_build_object('text','full evidence'))) FROM generate_series(1,70) g`); err != nil {
		t.Fatal(err)
	}
	if err := migrateControlSchema(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM console_sub_capability_summaries`).Scan(&count); err != nil || count != 0 {
		t.Fatal("startup performed backfill", count, err)
	}
	var schema string
	if err := db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE storage_state(singleton boolean primary key,ingest_limit_bytes bigint);INSERT INTO storage_state VALUES(true,pg_database_size(current_database())+67108864)`); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{32, 32, 6, 0} {
		n, err := prepareCapabilitySummaryBatch(ctx, db, schema)
		if err != nil || n != want {
			t.Fatal("batch progress", n, want, err)
		}
	}
	assertEqual := func(query controlQueryDB) {
		t.Helper()
		var mismatch bool
		if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_sub_targets t LEFT JOIN console_sub_capability_summaries s USING(account_id,group_id) WHERE s.account_id IS NULL OR s.compaction IS DISTINCT FROM console_sub_capability_summary_v1(t.compaction) OR s.tool_use IS DISTINCT FROM console_sub_capability_summary_v1(t.tool_use))`).Scan(&mismatch); err != nil || mismatch {
			t.Fatal("projection mismatch", err)
		}
	}
	assertEqual(db)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	assertEqual(tx)
	for _, q := range []string{`UPDATE console_sub_targets SET tool_use='{"attempts":[{"text":"new"}],"status":"error"}' WHERE group_id=1`, `UPDATE console_sub_targets SET group_id=-2 WHERE group_id=2`, `UPDATE console_sub_targets SET compaction='{"attempts":[{"http_status":"bad"}]}' WHERE group_id=3`, `DELETE FROM console_sub_targets WHERE group_id=4`} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
		assertEqual(db)
		assertEqual(tx)
	}
	write, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = write.Exec(`UPDATE console_sub_targets SET tool_use='{}' WHERE group_id=1`); err != nil {
		t.Fatal(err)
	}
	assertEqual(write)
	if err = write.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertEqual(db)
	if _, err = db.Exec(`DELETE FROM console_sub_capability_summaries WHERE group_id=1;UPDATE storage_state SET ingest_limit_bytes=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareCapabilitySummaryBatch(ctx, db, schema); !errors.Is(err, errManagementProjectionHeadroom) {
		t.Fatal("capacity not guarded", err)
	}
	if _, err = db.Exec(`UPDATE storage_state SET ingest_limit_bytes=pg_database_size(current_database())+67108864`); err != nil {
		t.Fatal(err)
	}
	locker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = locker.Exec(`SELECT pg_advisory_xact_lock(8395015)`); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareCapabilitySummaryBatch(ctx, db, schema); !errors.Is(err, errManagementPreparationBusy) {
		t.Fatal("did not yield", err)
	}
	locker.Rollback()
	locker, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = locker.Exec(`SELECT 1 FROM console_sub_targets WHERE account_id='a' AND group_id=1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareCapabilitySummaryBatch(ctx, db, schema); databaseErrorClass(err) != "database_lock_timeout" {
		t.Fatal("source lock was not bounded", err)
	}
	locker.Rollback()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = prepareCapabilitySummaryBatch(canceled, db, schema); err == nil {
		t.Fatal("canceled prepare succeeded")
	}
	if n, err := prepareCapabilitySummaryBatch(ctx, db, schema); err != nil || n != 1 {
		t.Fatal(fmt.Sprint(n), err)
	}
	assertEqual(db)
}

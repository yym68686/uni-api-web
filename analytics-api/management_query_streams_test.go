package main

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

type managementStreamReadHook struct {
	controlQueryDB
	beforeReceipts func()
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

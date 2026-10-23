package main

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

func TestManagementPreparedRatesKeepLegacyReceiptSemantics(t *testing.T) {
	s := managementBindingTestService(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('a','owner','fixture','https://fixture.test','a',''),('foreign','other','foreign','https://fixture.test','b','')`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id) VALUES('a',1,'group','openai',42),('foreign',1,'group','openai',42)`,
	} {
		if _, err := s.control.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	insert := `INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES($1,$2,1,$3,$4,$5,'[]','matched',$6)`
	for i, raw := range []string{
		`{"status":"matched","actual_cost":0.3,"total_cost":1,"input_price":2,"message":"diagnostic"}`,
		`{"status":"matched","actual_cost":0,"total_cost":1,"output_price":2}`,
		`{"status":"matched","actual_cost":0,"total_cost":0,"input_price":2}`,
		`{"status":"matched","actual_cost":3,"total_cost":1,"input_price":2,"rate_multiplier":0.3,"created_at":"2026-10-13T14:42:54+08:00"}`,
		`{"status":"matched","actual_cost":3,"total_cost":1,"input_price":2,"future_field":true}`,
		`{"Status":"matched","actual_cost":3,"total_cost":1,"input_price":2}`,
		`{"status":"matched","actual_cost":"invalid","total_cost":1,"input_price":2}`,
		`{"actual_cost":3,"total_cost":1,"input_price":2}`,
		`null`,
		`{"status":"matched","actual_cost":3,"total_cost":1,"input_price":2,"duration_ms":1.5}`,
		`{"status":"matched","actual_cost":3,"total_cost":1,"input_price":2,"duration_ms":1.0}`,
	} {
		if _, err := s.control.db.Exec(insert, fmt.Sprint("r", i), "a", 42, fmt.Sprint("m", i), i, raw); err != nil {
			t.Fatal(err)
		}
	}
	good := `{"status":"matched","actual_cost":9,"total_cost":1,"input_price":2}`
	if _, err := s.control.db.Exec(insert, "foreign", "foreign", 42, "m0", 100, good); err != nil {
		t.Fatal(err)
	}
	compare := func(db controlQueryDB, owner string) map[string]map[string]subObservedRate {
		t.Helper()
		old, err := s.control.subObservedRatesForAccountFrom(ctx, db, owner, "")
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.control.subObservedRatesPreparedFrom(ctx, db, owner, "")
		if err != nil || !reflect.DeepEqual(old, got) {
			t.Fatal("prepared rates differ", old, got, err)
		}
		return got
	}
	compare(s.control.db, "owner") // Incomplete preparation must use the old path.
	if _, err := s.control.db.Exec(`UPDATE console_management_projection_state SET phase='complete',complete=true WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	expected := compare(s.control.db, "owner")
	if expected["a:1"]["m0"].Rate != 0.3 || expected["a:1"]["m3"].Rate != 3 || expected["a:1"]["m4"].Rate != 3 {
		t.Fatal("raw fallback or verified rate lost", expected)
	}
	if _, err := s.control.db.Exec(`DELETE FROM console_sub_observed_rates WHERE account_id='a' AND model='m5'`); err != nil {
		t.Fatal(err)
	}
	if got := compare(s.control.db, "owner"); got["a:1"]["m5"].Rate != 3 {
		t.Fatal("valid legacy casing did not use raw decoder", got)
	}
	if _, err := s.control.db.Exec(`DELETE FROM console_sub_observed_rates WHERE account_id='a' AND model IN ('m9','m10')`); err != nil {
		t.Fatal(err)
	}
	got := compare(s.control.db, "owner")
	for _, model := range []string{"m9", "m10"} {
		if _, ok := got["a:1"][model]; ok {
			t.Fatal("malformed integer was accepted through compact rate payload", model)
		}
	}
	compare(s.control.db, "other")
	if got := compare(s.control.db, "nobody"); len(got) != 0 {
		t.Fatal("foreign owner leaked")
	}
	if _, err := s.control.db.Exec(`DELETE FROM console_management_receipt_signal WHERE id='r0'`); err != nil {
		t.Fatal(err)
	}
	compare(s.control.db, "owner") // Missing projection keeps raw current evidence.
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before := compare(tx, "owner")
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result=jsonb_set(result,'{actual_cost}','7') WHERE id='r0'`); err != nil {
		t.Fatal(err)
	}
	if after := compare(tx, "owner"); !reflect.DeepEqual(before, after) {
		t.Fatal("mixed receipt snapshots")
	}
	if got := compare(s.control.db, "owner"); got["a:1"]["m0"].Rate != 7 {
		t.Fatal("new writer not reflected", got)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET remote_key_id=43 WHERE account_id='a'`); err != nil {
		t.Fatal(err)
	}
	if got := compare(s.control.db, "owner"); len(got) != 0 {
		t.Fatal("reused previous key rate")
	}
	if _, err := s.control.db.Exec(insert, "new-key", "a", 43, "m0", 200, good); err != nil {
		t.Fatal(err)
	}
	compare(s.control.db, "owner")
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET remote_key_id=42 WHERE account_id='a'`); err != nil {
		t.Fatal(err)
	}
	if got := compare(s.control.db, "owner"); got["a:1"]["m0"].Rate != 7 {
		t.Fatal("raw current-key fallback lost", got)
	}
	if _, err := s.control.db.Exec(`DELETE FROM console_sub_usage WHERE account_id='a'`); err != nil {
		t.Fatal(err)
	}
	if got := compare(s.control.db, "owner"); got["a:1"]["m0"].Rate != 7 {
		t.Fatal("FIFO lost metadata", got)
	}
}

package main

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

// The preceding compact-payload query is retained only as an isolated baseline.
// Production never chooses it, and tests can compare values and query work.
func previousObservedRatesReadSQL(prepared bool) string {
	payload, rawSource, rawSupport, metadataSupport, support := "u.result", "FROM raw_latest l JOIN console_sub_usage u ON u.id=l.id", "", "", ""
	if prepared {
		payload = "p.rate_result"
		// raw_latest already identifies a visible, owned, current-key receipt in
		// this statement. Reading its raw row again is unnecessary for projection.
		rawSource = "FROM raw_latest l LEFT JOIN console_management_receipt_signal p ON p.id=l.id AND p.account_id=l.account_id"
		rawSupport = ",NOT COALESCE(p.valid,false) AS requires_raw"
		metadataSupport = ",false"
		// Evaluate before DISTINCT so an unsupported raw candidate cannot be
		// hidden by its compact metadata twin or allow a partially decoded map.
		support = ",bool_or(requires_raw) OVER()"
	}
	return `WITH owned_targets AS MATERIALIZED (
 SELECT t.account_id,t.group_id,t.remote_key_id FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id WHERE a.owner=$1 AND ($2='' OR a.id=$2)),
 raw_latest AS (
 SELECT DISTINCT ON (u.account_id,u.group_id,u.model) u.id,u.account_id,u.group_id,u.model,u.started_at
 FROM console_sub_usage u JOIN owned_targets t ON t.account_id=u.account_id AND t.group_id=u.group_id AND t.remote_key_id=u.key_id
 WHERE u.status='matched' ORDER BY u.account_id,u.group_id,u.model,u.started_at DESC,u.id DESC),
 candidates AS (
 SELECT l.id,l.account_id,l.group_id,l.model,l.started_at,` + payload + ` AS result` + rawSupport + `
 ` + rawSource + `
 UNION ALL
 SELECT r.receipt_id,r.account_id,r.group_id,r.model,r.started_at,r.result` + metadataSupport + `
 FROM console_sub_observed_rates r JOIN owned_targets t ON t.account_id=r.account_id AND t.group_id=r.group_id AND t.remote_key_id=r.key_id)
 SELECT DISTINCT ON (account_id,group_id,model) account_id,group_id,model,started_at,result` + support + `
 FROM candidates ORDER BY account_id,group_id,model,started_at DESC,id DESC`
}

type previousRateQueryDB struct{ controlQueryDB }

func (d previousRateQueryDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if query == subObservedRatesReadSQL(true) {
		query = previousObservedRatesReadSQL(true)
	}
	return d.controlQueryDB.QueryContext(ctx, query, args...)
}

type rateQueryTraceDB struct {
	controlQueryDB
	rawReads int
}

func (d *rateQueryTraceDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if q == subObservedRatesReadSQL(false) {
		d.rawReads++
	}
	return d.controlQueryDB.QueryContext(ctx, q, args...)
}

func TestManagementPreparedRateCandidateMembership(t *testing.T) {
	const good = `{"status":"matched","actual_cost":2,"total_cost":1,"input_price":1}`
	const newer = `{"status":"matched","actual_cost":3,"total_cost":1,"input_price":1}`
	const invalid = `{"status":"matched","actual_cost":2,"total_cost":1,"input_price":1,"duration_ms":"bad"}`
	const unusable = `{"status":"matched","actual_cost":0,"total_cost":0,"input_price":1}`
	cases := []struct {
		name     string
		mutate   []string
		fallback bool
		rate     *float64
	}{
		{"equal_duplicate", nil, false, usageFloat(2)},
		{"raw_only", []string{`DELETE FROM console_sub_observed_rates WHERE account_id='a'`}, false, usageFloat(2)},
		{"retained_only", []string{`DELETE FROM console_sub_usage WHERE account_id='a'`}, false, usageFloat(2)},
		{"empty", []string{`DELETE FROM console_sub_usage WHERE account_id='a'`, `DELETE FROM console_sub_observed_rates WHERE account_id='a'`}, false, nil},
		{"raw_newer", []string{`UPDATE console_sub_observed_rates SET started_at=10,result='` + newer + `' WHERE account_id='a'`}, false, usageFloat(2)},
		{"retained_newer", []string{`UPDATE console_sub_observed_rates SET started_at=30,result='` + newer + `' WHERE account_id='a'`}, false, usageFloat(3)},
		{"newer_unusable_hides_old_valid", []string{`UPDATE console_sub_observed_rates SET started_at=30,result='` + unusable + `' WHERE account_id='a'`}, false, nil},
		{"raw_id_wins_tie", []string{`UPDATE console_sub_observed_rates SET receipt_id='a',result='` + newer + `' WHERE account_id='a'`}, false, usageFloat(2)},
		{"retained_id_wins_tie", []string{`UPDATE console_sub_observed_rates SET receipt_id='z',result='` + newer + `' WHERE account_id='a'`}, false, usageFloat(3)},
		{"same_identity_conflict", []string{`UPDATE console_sub_observed_rates SET result='` + newer + `' WHERE account_id='a'`}, true, nil},
		{"missing_projection", []string{`DELETE FROM console_management_receipt_signal WHERE id='m'`}, true, usageFloat(2)},
		{"projection_wrong_account", []string{`UPDATE console_management_receipt_signal SET account_id='b' WHERE id='m'`}, true, usageFloat(2)},
		{"unsupported_raw_hidden_by_newer_retained", []string{`UPDATE console_sub_usage SET result='` + invalid + `' WHERE id='m'`, `UPDATE console_sub_observed_rates SET started_at=30,result='` + newer + `' WHERE account_id='a'`}, true, usageFloat(3)},
		{"different_current_key", []string{`UPDATE console_sub_targets SET remote_key_id=43 WHERE account_id='a'`}, false, nil},
		{"unicode_receipt_collation", []string{`UPDATE console_sub_usage SET id='β' WHERE id='m'`, `UPDATE console_sub_observed_rates SET receipt_id='z',result='` + newer + `' WHERE account_id='a'`}, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := managementBindingTestService(t)
			ctx := context.Background()
			queries := []string{
				`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('a','owner','fixture','https://fixture.test','a',''),('b','owner','fixture','https://fixture.test','b',''),('foreign','other','fixture','https://fixture.test','f','')`,
				`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id) VALUES('a',1,'group','openai',42),('b',1,'group','openai',42),('foreign',1,'group','openai',42)`,
				`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES('m','a',1,42,'model',20,'[]','matched','` + good + `'),('foreign','foreign',1,42,'model',200,'[]','matched','` + newer + `'),('unrelated-invalid','b',1,42,'model',40,'[]','matched','` + invalid + `')`,
				`UPDATE console_management_projection_state SET phase='complete',complete=true WHERE singleton`,
			}
			queries = append(queries, tc.mutate...)
			for _, q := range queries {
				if _, err := s.control.db.ExecContext(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			expected, err := s.control.subObservedRatesForAccountFrom(ctx, s.control.db, "owner", "a")
			if err != nil {
				t.Fatal(err)
			}
			trace := &rateQueryTraceDB{controlQueryDB: s.control.db}
			got, err := s.control.subObservedRatesPreparedFrom(ctx, trace, "owner", "a")
			if err != nil || !reflect.DeepEqual(expected, got) {
				t.Fatal("candidate values changed", expected, got, err)
			}
			if (trace.rawReads != 0) != tc.fallback {
				t.Fatal("unexpected fallback", trace.rawReads)
			}
			if tc.rate != nil {
				v, ok := got["a:1"]["model"]
				if !ok || v.Rate != *tc.rate {
					t.Fatal("wrong winner", got)
				}
			}
			if len(got) > 1 || got["b:1"] != nil || got["foreign:1"] != nil {
				t.Fatal("account scope escaped")
			}
			// A different owner's ID cannot select the receipt by account parameter.
			foreign, err := s.control.subObservedRatesPreparedFrom(ctx, s.control.db, "owner", "foreign")
			if err != nil || len(foreign) != 0 {
				t.Fatal("foreign owner escaped", foreign, err)
			}
		})
	}
}

func TestManagementPreparedRateMergeStaysInCurrentTransaction(t *testing.T) {
	s := managementBindingTestService(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('a','owner','fixture','https://fixture.test','a','')`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id) VALUES('a',1,'group','openai',42)`,
		`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES('r','a',1,42,'m',20,'[]','matched','{"status":"matched","actual_cost":2,"total_cost":1,"input_price":1}')`,
		`UPDATE console_management_projection_state SET phase='complete',complete=true WHERE singleton`,
	} {
		if _, err := s.control.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	read := func(db controlQueryDB) map[string]map[string]subObservedRate {
		t.Helper()
		trace := &rateQueryTraceDB{controlQueryDB: db}
		v, e := s.control.subObservedRatesPreparedFrom(ctx, trace, "owner", "")
		if e != nil || trace.rawReads != 0 {
			t.Fatal("fast read failed", e, trace.rawReads)
		}
		return v
	}
	before := read(tx)
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result=jsonb_set(result,'{actual_cost}','9') WHERE id='r'`); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, read(tx)) {
		t.Fatal("mixed current transactions")
	}
	if read(s.control.db)["a:1"]["m"].Rate != 9 {
		t.Fatal("new commit not read")
	}
	write, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := write.Exec(`UPDATE console_sub_usage SET result=jsonb_set(result,'{actual_cost}','4') WHERE id='r'`); err != nil {
		write.Rollback()
		t.Fatal(err)
	}
	write.Rollback()
	if read(s.control.db)["a:1"]["m"].Rate != 9 {
		t.Fatal("rollback changed current result")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	v, err := s.control.subObservedRatesPreparedFrom(cancelCtx, s.control.db, "owner", "")
	if err == nil || v != nil {
		t.Fatal("canceled read published rates")
	}
}

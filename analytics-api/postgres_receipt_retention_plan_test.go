package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestReceiptRetentionUsesBoundedTimeSeekForOneDeepAccount(t *testing.T) {
	db := migrationTestDB(t)
	ctx := context.Background()
	for _, q := range []string{
		`CREATE TABLE console_sub_spend_logs(account_id text NOT NULL,key_id bigint NOT NULL,log_id bigint NOT NULL,at_ms bigint NOT NULL,PRIMARY KEY(account_id,key_id,log_id))`,
		`CREATE INDEX console_sub_spend_time ON console_sub_spend_logs(account_id,key_id,at_ms)`,
		`CREATE INDEX console_sub_spend_retention ON console_sub_spend_logs(at_ms)`,
		`CREATE INDEX console_sub_spend_account_retention ON console_sub_spend_logs(account_id,at_ms)`,
		// Physical/key order and time order differ within the deep account, as
		// they do after several keys' receipt pages have been imported.
		`INSERT INTO console_sub_spend_logs SELECT 'fixture-account-'||lpad(a::text,5,'0'),n%134+1,a*1000000+n,((n%18000)*997+a*200000)::bigint FROM generate_series(0,30) a CROSS JOIN generate_series(1,36000) n ORDER BY n,a`,
		`ANALYZE console_sub_spend_logs`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	accounts, floor := []string{"fixture-account-00024"}, int64(100000000)
	read := func(query string) []int64 {
		t.Helper()
		rows, err := db.QueryContext(ctx, query, accounts, floor, receiptRetentionBatchSize)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		values := []int64{}
		for rows.Next() {
			var at int64
			if err := rows.Scan(&at); err != nil {
				t.Fatal(err)
			}
			values = append(values, at)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return values
	}
	want := read(`SELECT at_ms FROM console_sub_spend_logs WHERE account_id=ANY($1::text[]) AND at_ms<$2 ORDER BY at_ms LIMIT $3`)
	got := read(`SELECT at_ms FROM (` + receiptExpiredCandidatesSQL + `) candidates`)
	if len(got) != receiptRetentionBatchSize || !reflect.DeepEqual(got, want) {
		t.Fatal("seek differs from exact FIFO timestamps")
	}
	type planNode struct {
		Kind    string     `json:"Node Type"`
		Index   string     `json:"Index Name"`
		Rows    float64    `json:"Actual Rows"`
		Loops   float64    `json:"Actual Loops"`
		Removed float64    `json:"Rows Removed by Filter"`
		Plans   []planNode `json:"Plans"`
	}
	old := `SELECT expired.ctid,expired.at_ms FROM unnest($1::text[]) AS a(account_id)
 CROSS JOIN LATERAL (SELECT ctid,at_ms FROM console_sub_spend_logs l
 WHERE (l.account_id,l.at_ms)>=(a.account_id,'-9223372036854775808'::bigint)
 AND (l.account_id,l.at_ms)<(a.account_id,$2::bigint) ORDER BY l.account_id,l.at_ms LIMIT $3) expired
 ORDER BY expired.at_ms LIMIT $3`
	for _, item := range []struct{ name, query string }{{"old_tuple", old}, {"explicit_equality", receiptExpiredCandidatesSQL}} {
		var raw []byte
		if err := db.QueryRowContext(ctx, `EXPLAIN (ANALYZE,BUFFERS,TIMING FALSE,FORMAT JSON) `+item.query, accounts, floor, receiptRetentionBatchSize).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var plan []struct {
			Plan      planNode
			Execution float64 `json:"Execution Time"`
		}
		if err := json.Unmarshal(raw, &plan); err != nil || len(plan) != 1 {
			t.Fatal("invalid query plan", err)
		}
		var scanned float64
		var indexes []string
		var visit func(planNode)
		visit = func(node planNode) {
			if node.Index != "" {
				indexes = append(indexes, node.Index)
				scanned += (node.Rows + node.Removed) * node.Loops
			}
			for _, child := range node.Plans {
				visit(child)
			}
		}
		visit(plan[0].Plan)
		t.Logf("variant=%s execution_ms=%.3f scanned_index_rows=%.0f indexes=%v", item.name, plan[0].Execution, scanned, indexes)
		if item.name == "explicit_equality" && (scanned > receiptRetentionBatchSize || len(indexes) != 1 || indexes[0] != receiptAccountTimeIndex) {
			t.Fatal("bounded seek traversed unrelated or whole-account history", scanned, indexes)
		}
	}
}

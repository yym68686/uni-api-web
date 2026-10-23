package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestReceiptRetentionStagePreservesErrorClassification(t *testing.T) {
	err := &receiptRetentionStageError{Stage: "delete_receipts", Cause: context.DeadlineExceeded}
	if !errors.Is(err, context.DeadlineExceeded) || databaseErrorClass(err) != "deadline_exceeded" {
		t.Fatal("stage wrapper hid the original failure class")
	}
	if got := err.Error(); got != "receipt retention stage delete_receipts failed" {
		t.Fatal("unexpected stage description", got)
	}
}

func TestReceiptRetentionSeeksOldestBatchAcrossAccountsWithoutDeletingLeasedHistory(t *testing.T) {
	s, first, _ := attributionFixture(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec("CREATE INDEX IF NOT EXISTS " + receiptAccountTimeIndex + " ON console_sub_spend_logs(account_id,at_ms)"); err != nil {
		t.Fatal(err)
	}
	if ready, err := receiptAccountTimeIndexReady(ctx, s.control.db); err != nil || !ready {
		t.Fatal("account/time index not ready", ready, err)
	}
	floor := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := s.engine.DB.Exec("UPDATE storage_state SET evicted_before_ms=?", floor); err != nil {
		t.Fatal(err)
	}
	accounts := []string{first}
	for i := 1; i < 5; i++ {
		id := fmt.Sprintf("%s-%d", first, i)
		if _, err := s.control.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth)
 SELECT $2,owner,name,base,$2||'@fixture.example',encrypted_auth FROM console_sub_accounts WHERE id=$1`, first, id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.control.db.Exec("DELETE FROM console_sub_accounts WHERE id=$1", id) })
		accounts = append(accounts, id)
	}
	leased := accounts[4]
	accounts = accounts[:4]
	// A large, earlier expired prefix belonging to a leased account must never
	// be scanned to find this batch or be selected for deletion.
	if _, err := s.control.db.Exec("UPDATE console_sub_accounts SET usage_lease_until=now()+interval '1 hour' WHERE id=$1", leased); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_spend_logs(account_id,key_id,log_id,at_ms,actual_cost,request_id,model)
 SELECT $1,1,n,$2::bigint-1000000+n,1,'unrelated','m' FROM generate_series(1,50000) n`, leased, floor); err != nil {
		t.Fatal(err)
	}
	for i, id := range accounts {
		if _, err := s.control.db.Exec(`INSERT INTO console_sub_spend_logs(account_id,key_id,log_id,at_ms,actual_cost,request_id,model)
 SELECT $1,(n%7)+1,n,$2::bigint-10000+n*4+$3::bigint,1,'selected','m' FROM generate_series(1,800) n`, id, floor, i); err != nil {
			t.Fatal(err)
		}
	}
	// The time predicate must include the complete timestamp domain, not
	// silently omit legacy rows before Unix epoch or the smallest bigint.
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_spend_logs(account_id,key_id,log_id,at_ms,actual_cost,request_id,model)
 VALUES($1,1,9001,$2,1,'oldest','m')`, first, int64(-1<<63)); err != nil {
		t.Fatal(err)
	}
	read := func(query string) []string {
		t.Helper()
		rows, err := s.control.db.QueryContext(ctx, query, accounts, floor, receiptRetentionBatchSize)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			result = append(result, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	identity := "account_id||':'||key_id||':'||log_id"
	want := read("SELECT " + identity + " FROM console_sub_spend_logs WHERE account_id=ANY($1::text[]) AND at_ms<$2 ORDER BY at_ms LIMIT $3")
	got := read("WITH batch AS (" + receiptExpiredCandidatesSQL + ") SELECT " + identity + " FROM console_sub_spend_logs WHERE ctid IN (SELECT ctid FROM batch) ORDER BY at_ms")
	if len(got) != receiptRetentionBatchSize || !reflect.DeepEqual(got, want) {
		t.Fatal("candidate batch differs from exact global oldest receipts", len(got), len(want))
	}
	if err := s.trimReceiptHistory(ctx); err != nil {
		t.Fatal(err)
	}
	var remaining, leasedRows int
	if err := s.control.db.QueryRow("SELECT count(*) FROM console_sub_spend_logs WHERE account_id=ANY($1::text[])", accounts).Scan(&remaining); err != nil || remaining != 3201-receiptRetentionBatchSize {
		t.Fatal("unexpected bounded deletion", remaining, err)
	}
	if err := s.control.db.QueryRow("SELECT count(*) FROM console_sub_spend_logs WHERE account_id=$1", leased).Scan(&leasedRows); err != nil || leasedRows != 50000 {
		t.Fatal("leased history changed", leasedRows, err)
	}
	if gotAfter := read("SELECT " + identity + " FROM console_sub_spend_logs WHERE account_id=ANY($1::text[]) AND at_ms<$2 ORDER BY at_ms LIMIT $3"); len(gotAfter) == 0 || gotAfter[0] == want[0] {
		t.Fatal("oldest batch did not advance")
	}
}

package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestManagementReceiptEstimatedPlanKeepsBusinessPlanHistoryAndParameters(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Private, syntactically awkward values must remain bound, not SQL text.
	owner := "private'\\owner_日本"
	ids := []string{"private'\\id", "missing); SELECT pg_sleep(30); --"}
	if _, err := readManagementStreamReceiptsUncached(ctx, tx, owner, ids, true); err != nil {
		t.Fatal(err)
	}
	before, err := managementReceiptPreparedPlans(ctx, tx, managementReceiptSelectedStreamSQL)
	if err != nil || len(before) != 1 {
		t.Fatal("business statement was not prepared", err)
	}
	shape, err := managementReceiptEstimatedPlan(ctx, tx, []any{owner, ids})
	if err != nil || shape == "unavailable" || !strings.Contains(shape, "estimated_rows=") {
		t.Fatal("same-connection estimate unavailable", shape, err)
	}
	for _, private := range []string{"private", "日本", "pg_sleep", "console_", "SELECT", "Index Cond", "Filter"} {
		if strings.Contains(shape, private) {
			t.Fatal("estimated plan leaked private content", private)
		}
	}
	after, err := managementReceiptPreparedPlans(ctx, tx, managementReceiptSelectedStreamSQL)
	if err != nil {
		t.Fatal(err)
	}
	mode, generic, custom := managementReceiptPlanDelta(before, after)
	if mode != "unavailable" || generic != 0 || custom != 0 {
		t.Fatal("EXPLAIN advanced business statement plan history", mode, generic, custom)
	}
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatal("estimate changed read-only transaction", err)
	}
}

func TestManagementReceiptPlanShapeDiscardsConditionsAndBoundsTree(t *testing.T) {
	raw := []byte(`[{"Plan":{"Node Type":"Hash Join","Plan Rows":12,"Hash Cond":"private-owner","Plans":[{"Node Type":"Seq Scan","Relation Name":"console_management_receipt_signal","Plan Rows":4096,"Filter":"private-id"},{"Node Type":"Index Scan","Relation Name":"console_sub_accounts","Plan Rows":1,"Index Name":"private-index"}]}}]`)
	want := "receipt_seq_scans=1 receipt_index_scans=0 receipt_index_only_scans=0 receipt_bitmap_heap_scans=0 account_scans=1 estimated_rows=12 nodes=3"
	if got := managementReceiptPlanShape(raw); got != want {
		t.Fatal(got)
	}
	for _, bad := range []string{"", `{}`, `[]`, `[{"Plan":{"Node Type":"Seq Scan","Relation Name":"private"}}]`, strings.Repeat(" ", 1<<20+1), `[{"Plan":{"Node Type":"Seq Scan","Relation Name":"console_management_receipt_signal","Plan Rows":-1}}]`} {
		if got := managementReceiptPlanShape([]byte(bad)); got != "unavailable" {
			t.Fatal("malformed plan accepted", got)
		}
	}
	child := `{"Node Type":"Index Scan","Relation Name":"console_management_receipt_signal","Plan Rows":1}`
	for range 65 {
		child = `{"Node Type":"Nested Loop","Plan Rows":1,"Plans":[` + child + `]}`
	}
	if got := managementReceiptPlanShape([]byte(`[{"Plan":` + child + `}]`)); got != "unavailable" {
		t.Fatal("unbounded plan accepted", got)
	}
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type managementReceiptExplainNode struct {
	Type     string                         `json:"Node Type"`
	Relation string                         `json:"Relation Name"`
	Rows     int64                          `json:"Plan Rows"`
	Plans    []managementReceiptExplainNode `json:"Plans"`
}

func managementReceiptPlanShape(raw []byte) string {
	var root []struct{ Plan managementReceiptExplainNode }
	if len(raw) > 1<<20 || json.Unmarshal(raw, &root) != nil || len(root) != 1 {
		return "unavailable"
	}
	seq, index, indexOnly, bitmap, account, nodes := 0, 0, 0, 0, 0, 0
	valid := true
	var walk func(managementReceiptExplainNode)
	walk = func(node managementReceiptExplainNode) {
		nodes++
		if nodes > 64 || node.Rows < 0 {
			valid = false
			return
		}
		if node.Relation == "console_management_receipt_signal" {
			switch node.Type {
			case "Seq Scan":
				seq++
			case "Index Scan":
				index++
			case "Index Only Scan":
				indexOnly++
			case "Bitmap Heap Scan":
				bitmap++
			}
		} else if node.Relation == "console_sub_accounts" {
			account++
		}
		for _, child := range node.Plans {
			walk(child)
		}
	}
	walk(root[0].Plan)
	if !valid || seq+index+indexOnly+bitmap == 0 {
		return "unavailable"
	}
	// Only fixed labels and integers. Plan conditions, aliases, statement names,
	// relation/index names and all literal parameter values are discarded.
	return fmt.Sprintf("receipt_seq_scans=%d receipt_index_scans=%d receipt_index_only_scans=%d receipt_bitmap_heap_scans=%d account_scans=%d estimated_rows=%d nodes=%d",
		seq, index, indexOnly, bitmap, account, root[0].Plan.Rows, nodes)
}

func managementReceiptEstimatedPlan(ctx context.Context, tx *sql.Tx, args []any) (string, error) {
	if len(args) != 2 {
		return "unavailable", nil
	}
	ids, ok := args[1].([]string)
	if !ok || len(ids) == 0 || len(ids) > 4096 {
		return "unavailable", nil
	}
	// Describe an unnamed EXPLAIN with the original bound parameters. Never
	// EXPLAIN EXECUTE the named business statement: even without ANALYZE that
	// would increment its plan-use history and affect automatic plan selection.
	// This is an estimate made AFTER the read, not its captured execution tree.
	var raw []byte
	err := tx.QueryRowContext(ctx, "EXPLAIN (FORMAT JSON) "+managementReceiptSelectedStreamSQL, append([]any{pgx.QueryExecModeDescribeExec}, args...)...).Scan(&raw)
	if err != nil {
		return "unavailable", err
	}
	return managementReceiptPlanShape(raw), nil
}

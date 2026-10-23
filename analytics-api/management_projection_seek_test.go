package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestManagementPreparationSeekAvoidsScanningEarlierRows(t *testing.T) {
	db := migrationTestDB(t)
	for _, q := range []string{
		`CREATE TABLE console_sub_usage(id text PRIMARY KEY)`,
		`INSERT INTO console_sub_usage SELECT 'p'||lpad(n::text,8,'0') FROM generate_series(1,360000) n`,
		`CREATE TABLE console_sub_models(account_id text,group_id bigint,model text,PRIMARY KEY(account_id,group_id,model))`,
		`INSERT INTO console_sub_models SELECT 'a',1,'m'||lpad(n::text,8,'0') FROM generate_series(1,360000) n`,
		`ANALYZE console_sub_usage`, `ANALYZE console_sub_models`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SET LOCAL plan_cache_mode=force_generic_plan`); err != nil {
		t.Fatal(err)
	}
	for _, models := range []bool{false, true} {
		cursor := managementProjectionIdentity{Account: "a", Group: 1, Model: "m00350000", Receipt: "p00350000"}
		query, args := managementPreparationBatchQuery(models, true, cursor, 128)
		oldQuery := `SELECT id FROM console_sub_usage WHERE NOT $1 OR id>$2 ORDER BY id LIMIT $3 FOR UPDATE`
		oldArgs := []any{true, cursor.Receipt, 128}
		if models {
			oldQuery = `SELECT account_id,group_id,model FROM console_sub_models WHERE NOT $1 OR (account_id,group_id,model)>($2,$3,$4) ORDER BY account_id,group_id,model LIMIT $5 FOR UPDATE`
			oldArgs = []any{true, cursor.Account, cursor.Group, cursor.Model, 128}
		}
		read := func(q string, a []any) [][]any {
			rows, err := tx.Query(q, a...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			cols, _ := rows.Columns()
			out := [][]any{}
			for rows.Next() {
				v := make([]any, len(cols))
				dest := make([]any, len(cols))
				for i := range v {
					dest[i] = &v[i]
				}
				if err := rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				out = append(out, v)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return out
		}
		want, got := read(oldQuery, oldArgs), read(query, args)
		if len(got) != 128 || !reflect.DeepEqual(got, want) {
			t.Fatal("resumed identities differ", models)
		}
		oldPlan, oldRemoved := explainManagementSeek(t, tx, oldQuery, oldArgs)
		plan, removed := explainManagementSeek(t, tx, query, args)
		t.Logf("models=%t old_removed=%d new_removed=%d old=%s new=%s", models, oldRemoved, removed, oldPlan, plan)
		if oldRemoved < 350000 {
			t.Fatal("fixture did not reproduce the leading disjunction scan", oldRemoved)
		}
		if removed != 0 || !strings.Contains(plan, `Index Cond`) {
			t.Fatal("cursor did not become index boundary", plan)
		}
		firstQ, firstArgs := managementPreparationBatchQuery(models, false, cursor, 128)
		if first := read(firstQ, firstArgs); len(first) != 128 || reflect.DeepEqual(first, got) {
			t.Fatal("initial batch reused resumed cursor")
		}
	}
}

func explainManagementSeek(t *testing.T, tx *sql.Tx, q string, args []any) (string, int64) {
	t.Helper()
	// PREPARE/EXECUTE ensures the planner sees a generic plan on this isolated connection.
	name := "seek_" + fmt.Sprint(len(q))
	if _, err := tx.Exec("PREPARE " + name + " AS " + q); err != nil {
		t.Fatal(err)
	}
	defer tx.Exec("DEALLOCATE " + name)
	params := []string{}
	for _, a := range args {
		switch v := a.(type) {
		case string:
			params = append(params, "'"+strings.ReplaceAll(v, "'", "''")+"'")
		default:
			params = append(params, fmt.Sprint(v))
		}
	}
	var raw []byte
	if err := tx.QueryRow("EXPLAIN (ANALYZE,BUFFERS,TIMING FALSE,FORMAT JSON) EXECUTE " + name + "(" + strings.Join(params, ",") + ")").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var root []map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	var removed int64
	var walk func(any)
	walk = func(v any) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		if n, ok := m["Rows Removed by Filter"].(float64); ok {
			removed += int64(n)
		}
		if ps, ok := m["Plans"].([]any); ok {
			for _, p := range ps {
				walk(p)
			}
		}
	}
	walk(root[0]["Plan"])
	return string(raw), removed
}

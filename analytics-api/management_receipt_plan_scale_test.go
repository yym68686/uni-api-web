package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// A local experiment only. Production keeps its existing execution mode until
// an actual slow request, rather than a synthetic fixture, establishes the cause.
type managementReceiptDescribeExperiment struct{ controlQueryDB }

func (d managementReceiptDescribeExperiment) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return d.controlQueryDB.QueryContext(ctx, q, append([]any{pgx.QueryExecModeCacheDescribe}, args...)...)
}

func TestManagementReceiptPlanProductionCardinality(t *testing.T) {
	if os.Getenv("RUN_MANAGEMENT_RECEIPT_PLAN_SCALE") != "1" {
		t.Skip("opt-in isolated synthetic fixture; not production acceptance")
	}
	db := migrationTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `CREATE TABLE console_sub_accounts(id text PRIMARY KEY,owner text);
 CREATE TABLE console_management_receipt_signal(id text PRIMARY KEY,account_id text,valid boolean,evidence_digest bytea,status text,
 input_price double precision,output_price double precision,input_tokens bigint,output_tokens bigint);
 INSERT INTO console_sub_accounts SELECT 'account-'||g,CASE WHEN g=48 THEN 'foreign' ELSE 'owner' END FROM generate_series(0,48) g;
 INSERT INTO console_management_receipt_signal SELECT md5(g::text),'account-'||(g%49),true,decode(repeat('ab',32),'hex'),'matched',
 CASE WHEN g%7=0 THEN NULL ELSE g::double precision/10 END,3,g,20 FROM generate_series(1,473085) g;
 ANALYZE console_sub_accounts; ANALYZE console_management_receipt_signal;`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ids := []string{}
	rows, err := tx.QueryContext(ctx, `SELECT md5((g*113)::text) FROM generate_series(1,4096) g`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	var baseline map[string]managementStreamReceipt
	// Allow PostgreSQL's normal automatic choice; never force the outcome.
	for n := 0; n < 6; n++ {
		baseline, err = readManagementStreamReceiptsUncached(ctx, tx, "owner", ids, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	var name string
	var generic, custom int64
	if err := tx.QueryRowContext(ctx, `SELECT name,generic_plans,custom_plans FROM pg_prepared_statements WHERE statement=$1`, managementReceiptSelectedStreamSQL).Scan(&name, &generic, &custom); err != nil {
		t.Fatal(err)
	}
	t.Logf("synthetic cached_statement generic_executions=%d custom_executions=%d", generic, custom)
	for n := 0; n < 3; n++ {
		for _, mode := range []string{"cached_statement", "cached_description"} {
			var reader controlQueryDB = tx
			if mode == "cached_description" {
				reader = managementReceiptDescribeExperiment{tx}
			}
			start := time.Now()
			got, err := readManagementStreamReceiptsUncached(ctx, reader, "owner", ids, true)
			if err != nil || !reflect.DeepEqual(got, baseline) {
				t.Fatal("candidate changed complete typed receipts or ownership", err)
			}
			t.Logf("synthetic round=%d mode=%s ids=%d rows=%d elapsed_us=%d", n, mode, len(ids), len(got), time.Since(start).Microseconds())
		}
	}
	for _, mode := range []string{"cached_statement", "cached_description"} {
		q := `EXPLAIN (ANALYZE,BUFFERS,TIMING FALSE,FORMAT JSON) ` + managementReceiptSelectedStreamSQL
		args := []any{pgx.QueryExecModeCacheDescribe, "owner", ids}
		if mode == "cached_statement" {
			// All fixture values are locally generated hexadecimal identities.
			q = `EXPLAIN (ANALYZE,BUFFERS,TIMING FALSE,FORMAT JSON) EXECUTE ` + pgx.Identifier{name}.Sanitize() + `('owner','{` + strings.Join(ids, ",") + `}'::text[])`
			args = nil
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var root []map[string]any
		if err := json.Unmarshal(raw, &root); err != nil {
			t.Fatal(err)
		}
		var nodes []string
		var walk func(map[string]any)
		walk = func(plan map[string]any) {
			nodes = append(nodes, fmt.Sprintf("%s estimated=%v actual=%v", plan["Node Type"], plan["Plan Rows"], plan["Actual Rows"]))
			for _, child := range childrenForReceiptPlanExperiment(plan) {
				walk(child)
			}
		}
		plan := root[0]["Plan"].(map[string]any)
		walk(plan)
		t.Logf("synthetic mode=%s nodes=%v shared_hits=%v shared_reads=%v execution_ms=%v", mode, nodes, plan["Shared Hit Blocks"], plan["Shared Read Blocks"], root[0]["Execution Time"])
	}
}

func childrenForReceiptPlanExperiment(plan map[string]any) []map[string]any {
	out := []map[string]any{}
	children, _ := plan["Plans"].([]any)
	for _, child := range children {
		out = append(out, child.(map[string]any))
	}
	return out
}

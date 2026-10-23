package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/jackc/pgx/v5"
)

// Legacy targets number hundreds, not historical requests. Fill only missing
// identities, lock before computing, and verify each small committed batch.
// There is no global activation flag: every missing row keeps the current raw
// summary fallback, and all old/new writers maintain their row transactionally.
func prepareCapabilitySummaryBatch(ctx context.Context, db *sql.DB, budgetSchema string) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='100ms';SET LOCAL statement_timeout='2s'`); err != nil {
		return 0, err
	}
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(8395034)`).Scan(&locked); err != nil {
		return 0, err
	}
	if !locked {
		return 0, errManagementPreparationBusy
	}
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(8395015)`).Scan(&locked); err != nil {
		return 0, err
	}
	if !locked {
		return 0, errManagementPreparationBusy
	}
	rows, err := tx.QueryContext(ctx, `SELECT t.account_id,t.group_id FROM console_sub_targets t WHERE NOT EXISTS(SELECT 1 FROM console_sub_capability_summaries s WHERE s.account_id=t.account_id AND s.group_id=t.group_id) ORDER BY t.account_id,t.group_id LIMIT 32 FOR UPDATE OF t`)
	if err != nil {
		return 0, err
	}
	ids := []subBoundKey{}
	for rows.Next() {
		var id subBoundKey
		if err = rows.Scan(&id.AccountID, &id.GroupID); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, tx.Commit()
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return 0, err
	}
	cte := `WITH expected AS MATERIALIZED (SELECT t.account_id,t.group_id,console_sub_capability_summary_v1(t.compaction) AS compaction,console_sub_capability_summary_v1(t.tool_use) AS tool_use FROM jsonb_to_recordset($1::jsonb) AS k(account_id text,group_id bigint) JOIN console_sub_targets t USING(account_id,group_id)) `
	var room bool
	if err = tx.QueryRowContext(ctx, cte+`SELECT pg_database_size(current_database())+33554432+COALESCE(sum(pg_column_size(to_jsonb(e))+512),0)*4<(SELECT ingest_limit_bytes FROM `+pgx.Identifier{budgetSchema, "storage_state"}.Sanitize()+` WHERE singleton) FROM expected e`, string(raw)).Scan(&room); err != nil {
		return 0, err
	}
	if !room {
		return 0, errManagementProjectionHeadroom
	}
	if _, err = tx.ExecContext(ctx, cte+`INSERT INTO console_sub_capability_summaries SELECT * FROM expected ON CONFLICT(account_id,group_id) DO UPDATE SET compaction=EXCLUDED.compaction,tool_use=EXCLUDED.tool_use`, string(raw)); err != nil {
		return 0, err
	}
	var mismatch bool
	if err = tx.QueryRowContext(ctx, cte+`SELECT EXISTS(SELECT 1 FROM expected e LEFT JOIN console_sub_capability_summaries s USING(account_id,group_id) WHERE to_jsonb(e) IS DISTINCT FROM to_jsonb(s))`, string(raw)).Scan(&mismatch); err != nil {
		return 0, err
	}
	if mismatch {
		return 0, errManagementProjectionMismatch
	}
	return len(ids), tx.Commit()
}

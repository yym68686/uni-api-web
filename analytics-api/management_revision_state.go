package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

func prepareManagementRevisionState(ctx context.Context, db *sql.DB) (string, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='2s';SET LOCAL lock_timeout='100ms'`); err != nil {
		return "", err
	}
	var relation string
	var rowsDelta, invalidDelta int64
	err = tx.QueryRowContext(ctx, `SELECT relation_name,rows_delta,invalid_delta FROM console_management_revision_state WHERE NOT complete ORDER BY relation_name LIMIT 1`).Scan(&relation, &rowsDelta, &invalidDelta)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var count, bad int64
	expr := "0"
	if relation == "console_management_model_signal" || relation == "console_management_receipt_signal" {
		expr = "count(*) FILTER(WHERE NOT valid)"
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*),`+expr+` FROM `+pgx.Identifier{relation}.Sanitize()).Scan(&count, &bad); err != nil {
		return "", err
	}
	// A writer changing this counter after our snapshot causes PostgreSQL's
	// serialization failure, not a baseline that mixes committed generations.
	if _, err = tx.ExecContext(ctx, `UPDATE console_management_revision_state SET rows_base=$2,invalid_base=$3,complete=true WHERE relation_name=$1`, relation, count-rowsDelta, bad-invalidDelta); err != nil {
		return "", err
	}
	return relation, tx.Commit()
}

func readManagementCompactRevision(ctx context.Context, db controlQueryDB, owner string) (string, bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT relation_name,revision,rows_base+rows_delta,invalid_base+invalid_delta,complete FROM console_management_revision_state ORDER BY relation_name`)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	counts := map[string]int64{}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	if err = encoder.Encode("management-current-revisions-v1"); err != nil {
		return "", false, err
	}
	ready := true
	for rows.Next() {
		var name string
		var revision, count, bad int64
		var complete bool
		if err = rows.Scan(&name, &revision, &count, &bad, &complete); err != nil {
			return "", false, err
		}
		counts[name] = count
		ready = ready && complete && count >= 0 && bad == 0
		if err = encoder.Encode([]any{name, revision}); err != nil {
			return "", false, err
		}
	}
	if err = rows.Err(); err != nil {
		return "", false, err
	}
	rows.Close()
	if !ready || len(counts) != 4 || counts["console_sub_models"] != counts["console_management_model_signal"] || counts["console_sub_usage"] != counts["console_management_receipt_signal"] {
		return "", false, nil
	}
	revision, err := finishManagementModelRevision(ctx, db, owner, hash)
	return revision, err == nil, err
}

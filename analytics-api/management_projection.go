package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var errManagementPreparationBusy = errors.New("management projection preparation yielded to another transaction")
var errManagementProjectionMismatch = errors.New("management projection differs from current source rows")
var errManagementProjectionHeadroom = errors.New("management projection preparation needs database headroom")

type managementProjectionStep struct {
	Phase    string `json:"phase"`
	Rows     int    `json:"rows"`
	Complete bool   `json:"complete"`
}

type managementProjectionIdentity struct {
	Account string `json:"account_id"`
	Group   int64  `json:"group_id"`
	Model   string `json:"model"`
	Receipt string `json:"id"`
}

const managementModelSignalColumns = "account_id,group_id,model,valid,present,checked_at,verdict,availability,terminal,model_match,probe_id,has_usage,usage_status,input_price,output_price,input_tokens,output_tokens"
const managementReceiptSignalColumns = "id,account_id,valid,status,input_price,output_price,input_tokens,output_tokens"

// Preparation is explicit, restartable and bounded. Installing writer triggers
// precedes preparation, so updates/inserts behind a durable cursor are covered.
// Source rows are locked before a second statement computes their projection;
// this prevents a backfill snapshot from overwriting a concurrent newer writer.
func prepareManagementProjectionStep(ctx context.Context, db *sql.DB, budgetSchema string) (managementProjectionStep, error) {
	step := managementProjectionStep{}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return step, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='100ms';SET LOCAL statement_timeout='2s'`); err != nil {
		return step, err
	}
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(8395033)`).Scan(&locked); err != nil {
		return step, err
	}
	if !locked {
		return step, errManagementPreparationBusy
	}
	var started bool
	var cursor managementProjectionIdentity
	if err = tx.QueryRowContext(ctx, `SELECT phase,complete,started,cursor_account,cursor_group,cursor_model,cursor_receipt FROM console_management_projection_state WHERE singleton FOR UPDATE`).Scan(&step.Phase, &step.Complete, &started, &cursor.Account, &cursor.Group, &cursor.Model, &cursor.Receipt); err != nil || step.Complete {
		return step, err
	}
	models := step.Phase == "models" || step.Phase == "verify_models"
	verify := strings.HasPrefix(step.Phase, "verify_")
	if !verify {
		// Share admission with ingestion/FIFO; never queue ahead of active work.
		if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(8395015)`).Scan(&locked); err != nil {
			return step, err
		}
		if !locked {
			return step, errManagementPreparationBusy
		}
	}
	query := `SELECT id FROM console_sub_usage WHERE NOT $1 OR id>$2 ORDER BY id LIMIT 512 FOR UPDATE`
	args := []any{started, cursor.Receipt}
	if models {
		query = `SELECT account_id,group_id,model FROM console_sub_models WHERE NOT $1 OR (account_id,group_id,model)>($2,$3,$4) ORDER BY account_id,group_id,model LIMIT 512 FOR UPDATE`
		args = []any{started, cursor.Account, cursor.Group, cursor.Model}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return step, err
	}
	identities := []managementProjectionIdentity{}
	for rows.Next() {
		var id managementProjectionIdentity
		if models {
			err = rows.Scan(&id.Account, &id.Group, &id.Model)
		} else {
			err = rows.Scan(&id.Receipt)
		}
		if err != nil {
			break
		}
		identities = append(identities, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return step, err
	}
	step.Rows = len(identities)
	if step.Rows == 0 {
		next := map[string]string{"models": "receipts", "receipts": "verify_models", "verify_models": "verify_receipts", "verify_receipts": "complete"}[step.Phase]
		if next == "" {
			return step, errManagementProjectionMismatch
		}
		step.Phase, step.Complete = next, next == "complete"
		if _, err = tx.ExecContext(ctx, `UPDATE console_management_projection_state SET phase=$1,complete=$2,started=false,cursor_account='',cursor_group=0,cursor_model='',cursor_receipt='' WHERE singleton`, next, step.Complete); err != nil {
			return step, err
		}
		return step, tx.Commit()
	}
	raw, err := json.Marshal(identities)
	if err != nil {
		return step, err
	}
	columns, relation := managementReceiptSignalColumns, "console_management_receipt_signal"
	cte := `WITH wanted AS (SELECT * FROM jsonb_to_recordset($1::jsonb) AS k(id text)), expected AS MATERIALIZED (
 SELECT r.id,r.account_id,v.valid,v.status,v.input_price,v.output_price,v.input_tokens,v.output_tokens
 FROM wanted k JOIN console_sub_usage r USING(id) CROSS JOIN LATERAL console_management_receipt_signal_value(r.result,r.status) v) `
	keys := "id"
	if models {
		columns, relation, keys = managementModelSignalColumns, "console_management_model_signal", "account_id,group_id,model"
		cte = `WITH wanted AS (SELECT * FROM jsonb_to_recordset($1::jsonb) AS k(account_id text,group_id bigint,model text)), expected AS MATERIALIZED (
 SELECT r.account_id,r.group_id,r.model,v.valid,v.present,v.checked_at,v.verdict,v.availability,v.terminal,v.model_match,v.probe_id,v.has_usage,v.usage_status,v.input_price,v.output_price,v.input_tokens,v.output_tokens
 FROM wanted k JOIN console_sub_models r USING(account_id,group_id,model) CROSS JOIN LATERAL console_management_model_signal_value(r.result) v) `
	}
	if !verify {
		// Include this exact batch's uncompressed projected values, index/tuple
		// overhead and 32 MiB reserve. Defer before allocating, not after filling
		// the budget. This does not change thresholds or perform manual eviction.
		var room bool
		budget := pgx.Identifier{budgetSchema, "storage_state"}.Sanitize()
		if err = tx.QueryRowContext(ctx, cte+`SELECT pg_database_size(current_database())+33554432+COALESCE(sum(pg_column_size(to_jsonb(e))+512),0)*4 < (SELECT ingest_limit_bytes FROM `+budget+` WHERE singleton) FROM expected e`, string(raw)).Scan(&room); err != nil {
			return step, err
		}
		if !room {
			return step, errManagementProjectionHeadroom
		}
		updates := []string{}
		for _, name := range strings.Split(columns, ",") {
			if !strings.Contains(","+keys+",", ","+name+",") {
				updates = append(updates, name+"=EXCLUDED."+name)
			}
		}
		if _, err = tx.ExecContext(ctx, cte+`INSERT INTO `+relation+`(`+columns+`) SELECT `+columns+` FROM expected ON CONFLICT(`+keys+`) DO UPDATE SET `+strings.Join(updates, ","), string(raw)); err != nil {
			return step, err
		}
	}
	// Compare every projected column with the current locked source rows. The
	// foreign key excludes extra/orphan identities; later writer transactions
	// keep these exact values current, including updates behind the cursor.
	var different bool
	if err = tx.QueryRowContext(ctx, cte+`SELECT EXISTS(SELECT 1 FROM expected e LEFT JOIN `+relation+` p USING(`+keys+`) WHERE to_jsonb(e) IS DISTINCT FROM to_jsonb(p))`, string(raw)).Scan(&different); err != nil {
		return step, err
	}
	if different {
		return step, errManagementProjectionMismatch
	}
	last := identities[len(identities)-1]
	if _, err = tx.ExecContext(ctx, `UPDATE console_management_projection_state SET started=true,cursor_account=$1,cursor_group=$2,cursor_model=$3,cursor_receipt=$4 WHERE singleton`, last.Account, last.Group, last.Model, last.Receipt); err != nil {
		return step, err
	}
	return step, tx.Commit()
}

func prepareManagementProjection(ctx context.Context, db *sql.DB, budgetSchema string, delay time.Duration, progress func(managementProjectionStep)) error {
	for {
		step, err := prepareManagementProjectionStep(ctx, db, budgetSchema)
		if err != nil {
			return err
		}
		if progress != nil {
			progress(step)
		}
		if step.Complete {
			return nil
		}
		// Preparation always yields, even when an operator omitted batch-delay.
		if !waitStartup(ctx, max(delay, 100*time.Millisecond)) {
			return ctx.Err()
		}
	}
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

const receiptRetentionBatchSize = 512
const receiptAccountTimeIndex = "console_sub_spend_account_retention"

var errReceiptRetentionIndexRequired = errors.New("receipt retention account-time index requires explicit preparation")

type receiptRetentionStageError struct {
	Stage string
	Cause error
}

func (e *receiptRetentionStageError) Error() string {
	return "receipt retention stage " + e.Stage + " failed"
}
func (e *receiptRetentionStageError) Unwrap() error { return e.Cause }

// State account equality explicitly so the planner knows each lateral seek
// has one account. Tuple bounds alone can be estimated as many account groups,
// making an incremental sort on the account/key primary index appear cheap
// under LIMIT even though it must read the entire selected account first.
const receiptExpiredCandidatesSQL = `SELECT expired.ctid,expired.at_ms FROM unnest($1::text[]) AS a(account_id)
 CROSS JOIN LATERAL (SELECT ctid,at_ms FROM console_sub_spend_logs l
 WHERE l.account_id=a.account_id AND l.at_ms<$2::bigint ORDER BY l.at_ms LIMIT $3) expired
 ORDER BY expired.at_ms LIMIT $3`

const usageExpiredCandidatesSQL = `SELECT expired.ctid,expired.started_at FROM unnest($1::text[]) AS a(account_id)
 CROSS JOIN LATERAL (SELECT ctid,started_at FROM console_sub_usage u
 WHERE u.account_id=a.account_id AND u.status<>'pending' AND u.started_at<$2::bigint/1000
 ORDER BY u.started_at LIMIT $3) expired ORDER BY expired.started_at LIMIT $3`

func receiptAccountTimeIndexReady(ctx context.Context, db *sql.DB) (bool, error) {
	var ready bool
	err := db.QueryRowContext(ctx, `SELECT coalesce((SELECT indisvalid AND indisready AND indrelid='public.console_sub_spend_logs'::regclass
 AND indnkeyatts=2 AND indpred IS NULL AND indexprs IS NULL
 AND pg_get_indexdef(indexrelid,1,true)='account_id' AND pg_get_indexdef(indexrelid,2,true)='at_ms'
 AND (SELECT amname FROM pg_class i JOIN pg_am a ON a.oid=i.relam WHERE i.oid=indexrelid)='btree'
 FROM pg_index WHERE indexrelid=to_regclass('public.console_sub_spend_account_retention')),false)`).Scan(&ready)
	return ready, err
}

func (s *Service) receiptHistoryFloor(ctx context.Context) (int64, error) {
	if s.engine == nil || s.engine.DB == nil {
		return 0, nil
	}
	var floor int64
	err := s.engine.DB.QueryRowContext(ctx, "SELECT receipt_evicted_before_ms FROM storage_state WHERE singleton").Scan(&floor)
	return floor, err
}

// Existing account credentials, task leases, latest model evidence and pending
// reconciliations are not deleted. Inactive account receipt caches are shortened
// to the same retained history boundary, in bounded transactions.
func (s *Service) trimReceiptHistory(ctx context.Context) (err error) {
	stage := "migration_state"
	defer func() {
		if err != nil {
			err = &receiptRetentionStageError{Stage: stage, Cause: err}
		}
	}()
	if s.control == nil {
		return nil
	}
	var phase string
	err = s.engine.DB.QueryRowContext(ctx, "SELECT value FROM meta WHERE name='legacy_migration_phase'").Scan(&phase)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if phase != "" && phase != "complete" {
		return nil
	}
	var floor int64
	stage = "read_floor"
	if err = s.engine.DB.QueryRowContext(ctx, "SELECT evicted_before_ms FROM storage_state WHERE singleton").Scan(&floor); err != nil || floor == 0 {
		return err
	}
	stage = "check_index"
	ready, err := receiptAccountTimeIndexReady(ctx, s.control.db)
	if err != nil {
		return err
	}
	if !ready {
		return errReceiptRetentionIndexRequired
	}
	started := time.Now()
	stage = "begin"
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stage = "set_limits"
	if _, err = tx.ExecContext(ctx, "SET LOCAL lock_timeout='100ms'; SET LOCAL statement_timeout='1500ms'"); err != nil {
		return err
	}
	// Active lease owners retain their rows until their scan has completed.
	// EXISTS can choose a sequential scan assuming an early match, scanning
	// the entire receipt table for each account with no expired receipts. A
	// one-row scalar probe retains the existing account/key/time index order;
	// any matching row is sufficient, including receipts on later keys.
	stage = "select_accounts"
	rows, err := tx.QueryContext(ctx, `SELECT id FROM console_sub_accounts a WHERE (usage_lease_until IS NULL OR usage_lease_until<now()) AND (coalesce((SELECT true FROM console_sub_spend_logs l WHERE l.account_id=a.id AND l.at_ms<$1 ORDER BY l.key_id,l.at_ms LIMIT 1),false) OR EXISTS(SELECT 1 FROM console_sub_usage u WHERE u.account_id=a.id AND u.status<>'pending' AND u.started_at<$1::bigint/1000)) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 4`, floor)
	if err != nil {
		return err
	}
	var accounts []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		accounts = append(accounts, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return nil
	}
	// Seek each account's oldest rows before merging. Limiting a global time
	// scan still traverses unrelated accounts' history before finding any row.
	// The earliest N rows overall must be among the earliest N of each account.
	stage = "delete_receipts"
	deleted, err := tx.ExecContext(ctx, `WITH expired AS MATERIALIZED (`+receiptExpiredCandidatesSQL+`)
 DELETE FROM console_sub_spend_logs WHERE ctid IN (SELECT ctid FROM expired)`, accounts, floor, receiptRetentionBatchSize)
	if err != nil {
		return err
	}
	receipts, _ := deleted.RowsAffected()
	// Completed per-request receipt tasks are history too. Keep pending work
	// and active leases, but do not let finalized request rows grow forever.
	stage = "delete_usage"
	deleted, err = tx.ExecContext(ctx, `WITH expired AS MATERIALIZED (`+usageExpiredCandidatesSQL+`)
 DELETE FROM console_sub_usage WHERE ctid IN (SELECT ctid FROM expired)`, accounts, floor, receiptRetentionBatchSize)
	if err != nil {
		return err
	}
	tasks, _ := deleted.RowsAffected()
	stage = "update_cache"
	_, err = tx.ExecContext(ctx, `UPDATE console_sub_account_spend_cache SET covered_from=greatest(covered_from,$2),wanted_from=greatest(wanted_from,$2),wanted_to=greatest(wanted_to,$2),scan_from=0,scan_to=0,page=1 WHERE account_id=ANY($1::text[])`, accounts, floor)
	if err != nil {
		return err
	}
	stage = "record_floor"
	if _, err = tx.ExecContext(ctx, "UPDATE "+s.engine.historyPath+".storage_state SET receipt_evicted_before_ms=greatest(receipt_evicted_before_ms,$1) WHERE singleton", floor); err != nil {
		return err
	}
	stage = "commit"
	if err = tx.Commit(); err != nil {
		return err
	}
	if receipts+tasks > 0 {
		log.Printf("event=receipt_history_trimmed accounts=%d receipts=%d tasks=%d before_ms=%d elapsed_ms=%d", len(accounts), receipts, tasks, floor, time.Since(started).Milliseconds())
	}
	return nil
}

package main

import (
	"context"
	"database/sql"
	"errors"
)

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
func (s *Service) trimReceiptHistory(ctx context.Context) error {
	if s.control == nil {
		return nil
	}
	var phase string
	err := s.engine.DB.QueryRowContext(ctx, "SELECT value FROM meta WHERE name='legacy_migration_phase'").Scan(&phase)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if phase != "" && phase != "complete" {
		return nil
	}
	var floor int64
	if err = s.engine.DB.QueryRowContext(ctx, "SELECT evicted_before_ms FROM storage_state WHERE singleton").Scan(&floor); err != nil || floor == 0 {
		return err
	}
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SET LOCAL lock_timeout='100ms'; SET LOCAL statement_timeout='1500ms'"); err != nil {
		return err
	}
	// Active lease owners retain their rows until their scan has completed.
	// EXISTS can choose a sequential scan assuming an early match, scanning
	// the entire receipt table for each account with no expired receipts. A
	// one-row scalar probe retains the existing account/key/time index order;
	// any matching row is sufficient, including receipts on later keys.
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
	_, err = tx.ExecContext(ctx, `DELETE FROM console_sub_spend_logs WHERE ctid IN (SELECT ctid FROM console_sub_spend_logs WHERE account_id=ANY($1::text[]) AND at_ms<$2 ORDER BY at_ms LIMIT 5000)`, accounts, floor)
	if err != nil {
		return err
	}
	// Completed per-request receipt tasks are history too. Keep pending work
	// and active leases, but do not let finalized request rows grow forever.
	_, err = tx.ExecContext(ctx, `DELETE FROM console_sub_usage WHERE ctid IN (SELECT ctid FROM console_sub_usage WHERE account_id=ANY($1::text[]) AND status<>'pending' AND started_at<$2::bigint/1000 ORDER BY started_at LIMIT 5000)`, accounts, floor)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE console_sub_account_spend_cache SET covered_from=greatest(covered_from,$2),wanted_from=greatest(wanted_from,$2),wanted_to=greatest(wanted_to,$2),scan_from=0,scan_to=0,page=1 WHERE account_id=ANY($1::text[])`, accounts, floor)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE "+s.engine.historyPath+".storage_state SET receipt_evicted_before_ms=greatest(receipt_evicted_before_ms,$1) WHERE singleton", floor); err != nil {
		return err
	}
	return tx.Commit()
}

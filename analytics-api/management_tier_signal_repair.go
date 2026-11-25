package main

import (
	"context"
	"database/sql"
	"log"
	"time"
)

// Repair only old, invalid projections that the new validator can represent.
// Lock the source before updating its projection, in the same order as the
// normal writer. Never rewrite source results, summaries, or probe receipts.
func repairManagementTierSignals(ctx context.Context, db *sql.DB) (int64, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout='100ms'; SET LOCAL statement_timeout='2s'`); err != nil {
		return 0, err
	}
	var needed bool
	if err := tx.QueryRowContext(ctx, `SELECT invalid_base+invalid_delta>0 FROM console_management_revision_state
 WHERE relation_name='console_management_model_signal'`).Scan(&needed); err != nil || !needed {
		return 0, err
	}
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(8395033)`).Scan(&locked); err != nil || !locked {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `WITH candidates AS MATERIALIZED (
 SELECT m.account_id,m.group_id,m.model,m.result
 FROM console_management_model_signal p
 CROSS JOIN LATERAL (
  SELECT account_id,group_id,model,result FROM console_sub_models
  WHERE (account_id,group_id,model)=(p.account_id,p.group_id,p.model)
    AND result ? 'service_tiers' AND console_management_model_supported_v1(console_sub_model_summary_v1(result))
  FOR NO KEY UPDATE SKIP LOCKED
 ) m
 WHERE NOT p.valid LIMIT 256
 )
 UPDATE console_management_model_signal p SET
 valid=v.valid,present=v.present,evidence_digest=v.evidence_digest,checked_at=v.checked_at,verdict=v.verdict,
 availability=v.availability,terminal=v.terminal,model_match=v.model_match,probe_id=v.probe_id,
 quality_probe_id=v.quality_probe_id,has_usage=v.has_usage,usage_status=v.usage_status,input_price=v.input_price,
 output_price=v.output_price,input_tokens=v.input_tokens,output_tokens=v.output_tokens,service_tiers=NULLIF(c.result->'service_tiers','null'::jsonb)
 FROM candidates c CROSS JOIN LATERAL console_management_model_signal_value(c.result) v
 WHERE (p.account_id,p.group_id,p.model)=(c.account_id,c.group_id,c.model) AND NOT p.valid AND v.valid`)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return n, err
	}
	return n, tx.Commit()
}

func (s *Service) repairManagementTierSignalsLoop(ctx context.Context) {
	for ctx.Err() == nil {
		step, cancel := context.WithTimeout(ctx, 3*time.Second)
		count, err := repairManagementTierSignals(step, s.control.db)
		cancel()
		if count > 0 || err != nil && ctx.Err() == nil {
			log.Printf("management_tier_signal_repair rows=%d class=%s", count, databaseErrorClass(err))
		}
		delay := time.Minute
		if err != nil || count > 0 {
			delay = 5 * time.Second
		}
		if !waitStartup(ctx, delay) {
			return
		}
	}
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"
)

const businessRateFreshness = 24 * time.Hour

func businessRateCutoff(now time.Time) int64 {
	return now.Add(-businessRateFreshness).UnixMilli()
}

type subBusinessRateKey struct {
	account string
	group   int64
	key     int64
}

type subBusinessRateSample struct {
	Group int64   `json:"group_id"`
	Key   int64   `json:"key_id"`
	Model string  `json:"model"`
	At    int64   `json:"at_ms"`
	LogID int64   `json:"log_id"`
	Rate  float64 `json:"rate"`
}

func subSpendReceiptRate(log subSpendLog, actual float64) *float64 {
	if log.verifiedRate != nil {
		return log.verifiedRate
	}
	// Native receipts are optional evidence. Invalid/unrecognized metadata
	// must not affect the spend ledger or become a verified multiplier.
	var mode string
	var total, rate *float64
	if len(log.Mode) > 0 && (json.Unmarshal(log.Mode, &mode) != nil || (mode != "" && mode != "token")) {
		return nil
	}
	if json.Unmarshal(log.Total, &total) != nil || json.Unmarshal(log.Rate, &rate) != nil ||
		subValidCost(total) == nil || *total <= 0 || subValidCost(rate) == nil ||
		math.Abs(actual-*total**rate) > 1e-10+math.Abs(actual)*1e-9 {
		return nil
	}
	return rate
}

func storeSubBusinessRates(ctx context.Context, tx *sql.Tx, account string, rates []subBusinessRateSample, cutoff int64) error {
	if len(rates) == 0 {
		return nil
	}
	raw, err := json.Marshal(rates)
	if err != nil {
		return err
	}
	// Only currently tracked routing keys or confirmed indexed keys are
	// eligible. Never publish a console detection key as business evidence.
	_, err = tx.ExecContext(ctx, `INSERT INTO console_sub_business_rates(account_id,group_id,key_id,model,at_ms,log_id,rate)
 SELECT DISTINCT ON (r.group_id,r.key_id,r.model) $1,r.group_id,r.key_id,r.model,r.at_ms,r.log_id,r.rate
 FROM jsonb_to_recordset($2::jsonb) AS r(group_id bigint,key_id bigint,model text,at_ms bigint,log_id bigint,rate double precision)
 WHERE NOT EXISTS (SELECT 1 FROM console_sub_targets t WHERE t.account_id=$1 AND t.remote_key_id=r.key_id)
 AND (EXISTS (SELECT 1 FROM console_sub_targets t WHERE t.account_id=$1 AND t.group_id=r.group_id AND t.routing_key_id=r.key_id)
 OR EXISTS (SELECT 1 FROM console_sub_key_index i WHERE i.account_id=$1 AND i.group_id=r.group_id AND i.remote_key_id=r.key_id))
 ORDER BY r.group_id,r.key_id,r.model,r.at_ms DESC,r.log_id DESC
 ON CONFLICT(account_id,group_id,key_id,model) DO UPDATE SET at_ms=excluded.at_ms,log_id=excluded.log_id,rate=excluded.rate
 WHERE (excluded.at_ms,excluded.log_id) > (console_sub_business_rates.at_ms,console_sub_business_rates.log_id)
 OR ((excluded.at_ms,excluded.log_id) = (console_sub_business_rates.at_ms,console_sub_business_rates.log_id) AND excluded.rate<>console_sub_business_rates.rate)`, account, string(raw))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM console_sub_business_rates WHERE account_id=$1 AND at_ms<$2`, account, cutoff)
	return err
}

func (s *controlStore) subBusinessRates(ctx context.Context, owner string, now time.Time) (map[subBusinessRateKey]map[string]subObservedRate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.account_id,r.group_id,r.key_id,r.model,r.at_ms,r.rate
 FROM console_sub_accounts a JOIN console_sub_business_rates r ON r.account_id=a.id
 WHERE a.owner=$1 AND r.at_ms BETWEEN $2 AND $3
 AND NOT EXISTS (SELECT 1 FROM console_sub_targets t WHERE t.account_id=r.account_id AND t.remote_key_id=r.key_id)`, owner, businessRateCutoff(now), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[subBusinessRateKey]map[string]subObservedRate{}
	for rows.Next() {
		var key subBusinessRateKey
		var model string
		var at int64
		var rate float64
		if err := rows.Scan(&key.account, &key.group, &key.key, &model, &at, &rate); err != nil {
			return nil, err
		}
		if out[key] == nil {
			out[key] = map[string]subObservedRate{}
		}
		out[key][model] = subObservedRate{Rate: rate, ObservedAt: at / 1000}
	}
	return out, rows.Err()
}

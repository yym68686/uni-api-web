package main

import (
	"context"
	"fmt"
)

// A compact exact claim count preserves cross-window ambiguity when raw facts
// leave FIFO storage. It has a bounded physical budget; if that too is exhausted,
// the database records incomplete claim history instead of inventing certainty.
func (e *Engine) retainEvictedReceiptClaims(ctx context.Context, tx *analyticsTx, partition string) error {
	var complete bool
	var bytes int64
	if err := tx.QueryRowContext(ctx, "SELECT claims_history_complete,pg_total_relation_size('receipt_claim_history'::regclass) FROM storage_state WHERE singleton").Scan(&complete, &bytes); err != nil {
		return err
	}
	if !complete {
		return nil
	}
	if bytes > 256<<20 {
		if _, err := tx.ExecContext(ctx, "TRUNCATE receipt_claim_history"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE storage_state SET claims_history_complete=false WHERE singleton")
		return err
	}
	// Hash the same canonical tuple as receiptOwnerKey, entirely in PostgreSQL.
	// This avoids transferring an evicted partition into application memory.
	var billingRows int64
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+partition+" WHERE kind='billing'").Scan(&billingRows); err != nil {
		return err
	}
	if bytes+billingRows*1024 > 256<<20 {
		if _, err := tx.ExecContext(ctx, "TRUNCATE receipt_claim_history"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE storage_state SET claims_history_complete=false WHERE singleton")
		return err
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO receipt_claim_history(digest,claims)
 SELECT encode(sha256(convert_to(base||E'\n'||hash||E'\n'||rid,'UTF8')),'hex'),count(*)
 FROM (SELECT DISTINCT event_id,%s AS base,coalesce(upstream_key_hash,'') AS hash,unnest(billing_request_ids) AS rid FROM %s WHERE kind='billing') claims
 GROUP BY base,hash,rid ON CONFLICT(digest) DO UPDATE SET claims=receipt_claim_history.claims+excluded.claims`, billingSiteSQL, partition))
	return err
}

func (e *Engine) addRetiredClaims(ctx context.Context, tx *analyticsTx, ownership map[string]int) error {
	keys := make([]string, 0, len(ownership))
	original := map[string]string{}
	for key := range ownership {
		digest := tokenHash(key)
		keys = append(keys, digest)
		original[digest] = key
	}
	if len(keys) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT digest,claims FROM receipt_claim_history WHERE digest=ANY(?::text[])", keys)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var digest string
		var count int
		if err = rows.Scan(&digest, &count); err != nil {
			return err
		}
		ownership[original[digest]] += count
	}
	return rows.Err()
}

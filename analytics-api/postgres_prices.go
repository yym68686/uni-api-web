package main

import (
	"context"
	"errors"
)

// Import an entire validated operator price document atomically. Runtime edits
// use SavePrice; replacement is reserved for explicit migration and tests.
func (e *Engine) applyPrices(ctx context.Context, prices []Price) error {
	seen := map[string]bool{}
	for _, p := range prices {
		if err := validatePrice(p); err != nil {
			return err
		}
		if seen[p.Model] {
			return errors.New("duplicate model price")
		}
		seen[p.Model] = true
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395016)"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM prices"); err != nil {
		return err
	}
	for _, p := range prices {
		if _, err = tx.ExecContext(ctx, `INSERT INTO prices(model,input,output,cache_read,cache_write,cache_write_1h,source,verified,effective_at,charge_cache_write,sale_percent,long_context_premium) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, p.Model, p.Input, p.Output, p.CacheRead, p.CacheWrite, p.CacheWrite1h, p.Source, p.Verified, p.EffectiveAt, p.ChargeCacheWrite, p.SalePercent, p.LongContextPremium); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE storage_state SET revision=revision+1 WHERE singleton"); err != nil {
		return err
	}
	return tx.Commit()
}

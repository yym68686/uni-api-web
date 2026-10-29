package main

import (
	"context"
	"database/sql"
	"log"
)

// Only hydrated management pages immediately normalize every usage to these
// five fields. A valid signal has checked the ENTIRE raw typed payload, not
// just the fields being projected. Missing/unsupported signals retain the raw
// decoder, its errors, and its owner check. No evidence survives this pinned
// snapshot; ordinary account/detail/selection reads remain unchanged.
func (s *Service) attachManagementPageSummaryUsageFrom(ctx context.Context, db controlQueryDB, owner string, accounts []subAccount, reuse *managementReceiptReuse) error {
	tx, pinned := db.(*sql.Tx)
	if !pinned || tx == nil {
		return s.attachManagementPageUsageFrom(ctx, db, owner, accounts, reuse)
	}
	probes := subUsageProbes(accounts)
	if len(probes) == 0 {
		return ctx.Err()
	}
	var complete bool
	if err := tx.QueryRowContext(ctx, `SELECT complete FROM console_management_projection_state WHERE singleton`).Scan(&complete); err != nil {
		return err
	}
	if !complete {
		return s.attachManagementPageUsageFrom(ctx, db, owner, accounts, reuse)
	}
	current := reuse.current(db, owner)
	ids := make([]string, 0, len(probes))
	for id := range probes {
		if current == nil || current(id) == nil {
			ids = append(ids, id)
		}
	}
	end := managementReadSpan(ctx, "hydrate_receipt_signals")
	receipts := make(map[string]managementStreamReceipt, len(ids))
	// Keep every lookup on the existing selected-ID reader. Its full-owner
	// scan for huge predicate sets is inappropriate for a bounded page.
	for start := 0; start < len(ids); start += 4096 {
		batch, err := readManagementStreamReceipts(ctx, tx, owner, ids[start:min(start+4096, len(ids))], false)
		if err != nil {
			end()
			return err
		}
		for id, receipt := range batch {
			if receipt.valid {
				receipts[id] = receipt
			}
		}
	}
	end()
	if managementTraceEnabled(ctx) {
		log.Printf("management_page_receipt_signals requested=%d resolved=%d request_id=%s", len(ids), len(receipts), consoleRequestID(ctx))
	}
	lookup := func(id string) *subUsage {
		if current != nil {
			if value := current(id); value != nil {
				return value
			}
		}
		if receipt, found := receipts[id]; found {
			return receipt.usage.expanded()
		}
		return nil
	}
	ctx = reuse.traceRawIDs(ctx, db, owner)
	return s.subAttachUsageProbesWithCurrentFrom(ctx, db, owner, probes, lookup)
}

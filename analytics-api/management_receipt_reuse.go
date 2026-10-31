package main

import (
	"context"
	"database/sql"
	"log"
)

// Only the predicate stream's verified, owner-scoped receipts may bypass a
// duplicate raw lookup. The transaction pointer is an identity fence, not a
// revision cache: another transaction, pool or owner must use the old reader.
// This holder never enters a paging directory, retained page or public JSON.
type managementReceiptReuse struct {
	tx       *sql.Tx
	owner    string
	receipts map[string]managementStreamReceipt
	// Only a page without price predicates may request deferral. The stream
	// confirms the complete compact proof before activating it for this tx.
	deferModelUsage bool
	// Diagnostic identities only, never evidence for skipping a lookup. A
	// successful account raw read can still have returned no row for an ID.
	accountRawIDs map[string]struct{}
}

type managementAccountReceiptsKey struct{}
type managementRawReceiptObserverKey struct{}

func managementReceiptPhase(ctx context.Context) string {
	if account, _ := ctx.Value(managementAccountReceiptsKey{}).(bool); account {
		return "account"
	}
	return "page"
}

func (r *managementReceiptReuse) capture(db controlQueryDB, owner string, receipts map[string]managementStreamReceipt) {
	tx, pinned := db.(*sql.Tx)
	if r != nil && pinned && tx != nil {
		r.tx, r.owner, r.receipts = tx, owner, receipts
		r.accountRawIDs = nil
	}
}

func (r *managementReceiptReuse) current(db controlQueryDB, owner string) func(string) *subUsage {
	tx, pinned := db.(*sql.Tx)
	if r == nil || !pinned || tx == nil || r.tx != tx || r.owner != owner {
		return nil
	}
	return func(id string) *subUsage {
		receipt, found := r.receipts[id]
		if !found || !receipt.valid {
			return nil
		}
		return receipt.usage.expanded()
	}
}

// Install only for the same pinned snapshot/owner and traced management read.
// This observes overlap AFTER a successful raw lookup; it cannot resolve or
// suppress any ID, and stores no raw receipts or missing-row evidence.
func (r *managementReceiptReuse) traceRawIDs(ctx context.Context, db controlQueryDB, owner string) context.Context {
	if !managementTraceEnabled(ctx) || r.current(db, owner) == nil {
		return ctx
	}
	observe := func(ids []string) {
		phase := managementReceiptPhase(ctx)
		repeated := 0
		for _, id := range ids {
			if _, exists := r.accountRawIDs[id]; exists {
				repeated++
			}
		}
		log.Printf("management_receipt_raw_scope phase=%s requested=%d prior_account=%d repeated=%d request_id=%s", phase, len(ids), len(r.accountRawIDs), repeated, consoleRequestID(ctx))
		if phase == "account" {
			r.accountRawIDs = make(map[string]struct{}, len(ids))
			for _, id := range ids {
				r.accountRawIDs[id] = struct{}{}
			}
		}
	}
	return context.WithValue(ctx, managementRawReceiptObserverKey{}, observe)
}

func (s *Service) attachManagementPageUsageFrom(ctx context.Context, db controlQueryDB, owner string, accounts []subAccount, reuse *managementReceiptReuse) error {
	ctx = reuse.traceRawIDs(ctx, db, owner)
	return s.subAttachUsageWithCurrentFrom(ctx, db, owner, accounts, reuse.current(db, owner))
}

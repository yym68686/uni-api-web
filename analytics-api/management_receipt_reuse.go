package main

import (
	"context"
	"database/sql"
)

// Only the predicate stream's verified, owner-scoped receipts may bypass a
// duplicate raw lookup. The transaction pointer is an identity fence, not a
// revision cache: another transaction, pool or owner must use the old reader.
// This holder never enters a paging directory, retained page or public JSON.
type managementReceiptReuse struct {
	tx       *sql.Tx
	owner    string
	receipts map[string]managementStreamReceipt
}

type managementAccountReceiptsKey struct{}

func (r *managementReceiptReuse) capture(db controlQueryDB, owner string, receipts map[string]managementStreamReceipt) {
	tx, pinned := db.(*sql.Tx)
	if r != nil && pinned && tx != nil {
		r.tx, r.owner, r.receipts = tx, owner, receipts
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

func (s *Service) attachManagementPageUsageFrom(ctx context.Context, db controlQueryDB, owner string, accounts []subAccount, reuse *managementReceiptReuse) error {
	return s.subAttachUsageWithCurrentFrom(ctx, db, owner, accounts, reuse.current(db, owner))
}

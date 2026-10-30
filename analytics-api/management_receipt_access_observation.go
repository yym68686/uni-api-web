package main

import (
	"context"
	"database/sql"
	"fmt"
)

// These counters belong to this backend's CURRENT transaction and update
// during it. Shared/cumulative I/O stats can remain unchanged until the RR
// transaction ends and cannot substitute for this before/after observation.
type managementReceiptAccess struct{ seq, seqRows, index, indexRows int64 }

func managementReceiptAccessSnapshot(ctx context.Context, tx *sql.Tx) (managementReceiptAccess, error) {
	var out managementReceiptAccess
	err := tx.QueryRowContext(ctx, `SELECT seq_scan,seq_tup_read,COALESCE(idx_scan,0),COALESCE(idx_tup_fetch,0)
 FROM pg_catalog.pg_stat_xact_user_tables WHERE relid='console_management_receipt_signal'::regclass`).Scan(&out.seq, &out.seqRows, &out.index, &out.indexRows)
	return out, err
}

func managementReceiptAccessDelta(before, after managementReceiptAccess) string {
	for _, counts := range [][2]int64{{before.seq, after.seq}, {before.seqRows, after.seqRows}, {before.index, after.index}, {before.indexRows, after.indexRows}} {
		if counts[0] < 0 || counts[1] < counts[0] {
			return "available=false"
		}
	}
	delta := managementReceiptAccess{after.seq - before.seq, after.seqRows - before.seqRows, after.index - before.index, after.indexRows - before.indexRows}
	return fmt.Sprintf("available=true seq_scans=%d seq_rows=%d index_scans=%d index_fetched=%d", delta.seq, delta.seqRows, delta.index, delta.indexRows)
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// These counters belong to this backend's CURRENT transaction and update
// during it. Shared/cumulative I/O stats can remain unchanged until the RR
// transaction ends and cannot substitute for this before/after observation.
type managementReceiptAccess struct {
	seq, seqRows, index, indexRows           int64
	heapFetch, heapHit, indexFetch, indexHit int64
}

func managementReceiptAccessSnapshot(ctx context.Context, tx *sql.Tx) (managementReceiptAccess, error) {
	return managementSignalAccessSnapshot(ctx, tx, "console_management_receipt_signal")
}

// Fixed signal relations only. The relation remains a bound regclass value;
// no diagnostic request can choose an arbitrary relation or supply SQL text.
func managementSignalAccessSnapshot(ctx context.Context, tx *sql.Tx, relation string) (managementReceiptAccess, error) {
	var out managementReceiptAccess
	if relation != "console_management_receipt_signal" && relation != "console_management_model_signal" {
		return out, errors.New("unsupported management signal relation")
	}
	err := tx.QueryRowContext(ctx, `SELECT seq_scan,seq_tup_read,COALESCE(idx_scan,0),COALESCE(idx_tup_fetch,0),
 pg_catalog.pg_stat_get_xact_blocks_fetched(relid),pg_catalog.pg_stat_get_xact_blocks_hit(relid),
 index_io.fetched,index_io.hit
 FROM pg_catalog.pg_stat_xact_user_tables AS receipt
 CROSS JOIN LATERAL (
 SELECT COALESCE(sum(pg_catalog.pg_stat_get_xact_blocks_fetched(indexrelid)),0)::bigint AS fetched,
 COALESCE(sum(pg_catalog.pg_stat_get_xact_blocks_hit(indexrelid)),0)::bigint AS hit
 FROM pg_catalog.pg_index WHERE indrelid=receipt.relid
 ) AS index_io
 WHERE relid=$1::regclass AND current_setting('track_counts')='on'`, relation).Scan(
		&out.seq, &out.seqRows, &out.index, &out.indexRows, &out.heapFetch, &out.heapHit, &out.indexFetch, &out.indexHit)
	return out, err
}

func managementReceiptAccessDelta(before, after managementReceiptAccess) string {
	for _, counts := range [][2]int64{{before.seq, after.seq}, {before.seqRows, after.seqRows}, {before.index, after.index}, {before.indexRows, after.indexRows},
		{before.heapFetch, after.heapFetch}, {before.heapHit, after.heapHit}, {before.indexFetch, after.indexFetch}, {before.indexHit, after.indexHit},
		{before.heapFetch - before.heapHit, after.heapFetch - after.heapHit}, {before.indexFetch - before.indexHit, after.indexFetch - after.indexHit}} {
		if counts[0] < 0 || counts[1] < counts[0] {
			return "available=false"
		}
	}
	// A shared-buffer miss reads through the operating system; its page cache
	// may satisfy that read. These are NOT physical-disk operations or timings.
	return fmt.Sprintf("available=true seq_scans=%d seq_rows=%d index_scans=%d index_fetched=%d heap_buffer_hits=%d heap_buffer_reads=%d index_buffer_hits=%d index_buffer_reads=%d",
		after.seq-before.seq, after.seqRows-before.seqRows, after.index-before.index, after.indexRows-before.indexRows,
		after.heapHit-before.heapHit, (after.heapFetch-after.heapHit)-(before.heapFetch-before.heapHit),
		after.indexHit-before.indexHit, (after.indexFetch-after.indexHit)-(before.indexFetch-before.indexHit))
}

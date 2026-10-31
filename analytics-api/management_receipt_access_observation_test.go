package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestManagementReceiptAccessMeasuresOnlyCurrentTransactionRead(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before, err := managementReceiptAccessSnapshot(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	// Force only this tiny isolated fixture to exercise actual scan accounting.
	if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan=off; SET LOCAL enable_bitmapscan=off`); err != nil {
		t.Fatal(err)
	}
	got, err := readManagementStreamReceiptsUncached(ctx, tx, "owner", []string{"a-1-gpt-5.5"}, true)
	if err != nil || len(got) != 1 {
		t.Fatal("fixture receipt unavailable", err)
	}
	after, err := managementReceiptAccessSnapshot(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if delta := managementReceiptAccessDelta(before, after); !strings.HasPrefix(delta, "available=true seq_scans=0 seq_rows=0 index_scans=1 index_fetched=1 heap_buffer_hits=") ||
		after.heapFetch <= before.heapFetch || after.indexFetch <= before.indexFetch {
		t.Fatal("actual local transaction read was not counted", delta)
	}
	// Re-reading metadata must not itself touch the observed receipt relation.
	unchanged, err := managementReceiptAccessSnapshot(ctx, tx)
	if err != nil || unchanged != after {
		t.Fatal("metadata contaminated receipt accounting", err)
	}
	// The pool uses another backend while this transaction pins its connection.
	// Reads by that backend must not appear in this transaction's deltas.
	var otherCount int64
	if err := s.control.db.QueryRowContext(ctx, `SELECT count(*) FROM console_management_receipt_signal`).Scan(&otherCount); err != nil || otherCount == 0 {
		t.Fatal("other connection did not read the fixture", err)
	}
	unchanged, err = managementReceiptAccessSnapshot(ctx, tx)
	if err != nil || unchanged != after {
		t.Fatal("another backend contaminated receipt accounting", err)
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan=on; SET LOCAL enable_indexscan=off; SET LOCAL enable_bitmapscan=off`); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM console_management_receipt_signal`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	seq, err := managementReceiptAccessSnapshot(ctx, tx)
	if err != nil || seq.seq-after.seq != 1 || seq.seqRows-after.seqRows != count || seq.index != after.index || seq.indexRows != after.indexRows {
		t.Fatal("actual sequential rows were not counted", seq, after, count, err)
	}
}

func TestManagementReceiptAccessRejectsMissingOrRegressingCounts(t *testing.T) {
	for _, counts := range []managementReceiptAccess{{seq: -1}, {seqRows: -1}, {index: -1}, {indexRows: -1},
		{heapFetch: -1}, {heapHit: -1}, {indexFetch: -1}, {indexHit: -1}, {heapHit: 1}, {indexHit: 1}} {
		if managementReceiptAccessDelta(counts, managementReceiptAccess{}) != "available=false" || managementReceiptAccessDelta(managementReceiptAccess{}, counts) != "available=false" {
			t.Fatal("invalid count appeared as zero")
		}
	}
	if managementReceiptAccessDelta(managementReceiptAccess{}, managementReceiptAccess{}) != "available=true seq_scans=0 seq_rows=0 index_scans=0 index_fetched=0 heap_buffer_hits=0 heap_buffer_reads=0 index_buffer_hits=0 index_buffer_reads=0" {
		t.Fatal("valid zero delta was lost")
	}
}

func TestManagementReceiptAccessBufferDeltaIsNotCumulative(t *testing.T) {
	before := managementReceiptAccess{heapFetch: 100, heapHit: 80, indexFetch: 200, indexHit: 180}
	after := managementReceiptAccess{heapFetch: 110, heapHit: 87, indexFetch: 220, indexHit: 199}
	if delta := managementReceiptAccessDelta(before, after); delta != "available=true seq_scans=0 seq_rows=0 index_scans=0 index_fetched=0 heap_buffer_hits=7 heap_buffer_reads=3 index_buffer_hits=19 index_buffer_reads=1" {
		t.Fatal("wrong request-local buffer delta", delta)
	}
	// A miss counter can regress even when the individual access/hit counters
	// increase. Never publish that as a negative read count.
	after.heapHit = 91
	if managementReceiptAccessDelta(before, after) != "available=false" {
		t.Fatal("regressing heap read count appeared available")
	}
	after.heapHit = 87
	after.indexHit = 201
	if managementReceiptAccessDelta(before, after) != "available=false" {
		t.Fatal("regressing index read count appeared available")
	}
}

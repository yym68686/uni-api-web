package main

import (
	"context"
	"database/sql"
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
	if delta := managementReceiptAccessDelta(before, after); delta != "available=true seq_scans=0 seq_rows=0 index_scans=1 index_fetched=1" {
		t.Fatal("actual local transaction read was not counted", delta)
	}
	// Re-reading metadata must not itself touch the observed receipt relation.
	unchanged, err := managementReceiptAccessSnapshot(ctx, tx)
	if err != nil || unchanged != after {
		t.Fatal("metadata contaminated receipt accounting", err)
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
	for _, counts := range []managementReceiptAccess{{-1, 0, 0, 0}, {0, -1, 0, 0}, {0, 0, -1, 0}, {0, 0, 0, -1}} {
		if managementReceiptAccessDelta(counts, managementReceiptAccess{}) != "available=false" || managementReceiptAccessDelta(managementReceiptAccess{}, counts) != "available=false" {
			t.Fatal("invalid count appeared as zero")
		}
	}
	if managementReceiptAccessDelta(managementReceiptAccess{}, managementReceiptAccess{}) != "available=true seq_scans=0 seq_rows=0 index_scans=0 index_fetched=0" {
		t.Fatal("valid zero delta was lost")
	}
}

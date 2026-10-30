package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func managementPackingFixture(group int64) *managementPageModelStream {
	e := &managementPageModelStream{group: managementPageModelGroup{"a", group}, bytes: 256}
	for m := 0; m < 288; m++ {
		raw := []byte(fmt.Sprintf(`{"model":"model-%d","checked_at":%d,"availability":{"id":"probe-%d-%d","status":"success","message":"%s","ttft_ms":123},"quality":{"message":"%s"}}`, m, group*288+int64(m), group, m, bytes.Repeat([]byte("a"), 280), bytes.Repeat([]byte("b"), 280)))
		row := managementPageModelRecord{"a", group, fmt.Sprint("model-", m), "done", "summary", raw}
		e.rows = append(e.rows, row)
		e.bytes += row.retainedBytes()
	}
	return e
}

func TestManagementPackedModelRowsPreserveExactBytesAndIsolation(t *testing.T) {
	e := managementPackingFixture(1)
	// These are SQL bytes, not necessarily valid JSON. Retention must neither
	// normalize them nor swallow a later decoder error, including nil vs empty.
	for _, raw := range [][]byte{nil, {}, []byte(` { "bad" : `), {0, 255, 1}, []byte(`{"extra":{"not_in_current_struct":"模型🙂"}}`)} {
		r := managementPageModelRecord{"a", 1, "模型🙂", "", "", raw}
		e.rows = append(e.rows, r)
		e.bytes += r.retainedBytes()
	}
	want := e.rows
	oldBytes := e.bytes
	(&managementModelPacker{}).pack(context.Background(), e)
	if len(e.rows) != 0 || len(e.packed) == 0 || e.bytes >= oldBytes || e.bytes != 256+len(e.packed) {
		t.Fatal("packing retained two graphs, bypassed accounting or did not reduce bytes")
	}
	got, ok := e.unpack(context.Background())
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatal("packing changed raw values, nil/empty bytes or ordering")
	}
	got[0].result[0] = 'x'
	got[0].account = "mutated"
	again, ok := e.unpack(context.Background())
	if !ok || !reflect.DeepEqual(again, want) {
		t.Fatal("consumer mutated immutable packed rows")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, ok := e.unpack(context.Background())
			if !ok || !reflect.DeepEqual(rows, want) {
				t.Error("concurrent reconstruction changed rows")
			}
		}()
	}
	wg.Wait()
}

func TestManagementPackedModelRowsRejectDamagedOrUnboundedPayload(t *testing.T) {
	base := managementPackingFixture(1)
	(&managementModelPacker{}).pack(context.Background(), base)
	for _, name := range []string{"gzip", "crc", "short", "length", "count", "group", "account", "bound", "cancel"} {
		t.Run(name, func(t *testing.T) {
			e := *base
			e.packed = bytes.Clone(e.packed)
			ctx := context.Background()
			switch name {
			case "gzip":
				e.packed[0] ^= 255
			case "crc":
				e.packed[len(e.packed)-5] ^= 255
			case "short":
				e.packed = e.packed[:len(e.packed)-9]
			case "length":
				e.packedBytes--
			case "count":
				e.packedRows++
			case "group":
				e.group.group++
			case "account":
				e.group.account = "foreign"
			case "bound":
				e.packedBytes = managementPackedModelLimit + 1
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if rows, ok := e.unpack(ctx); ok || len(rows) != 0 {
				t.Fatal("partial or invalid cache group was published")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := managementPackingFixture(1)
	(&managementModelPacker{}).pack(ctx, e)
	if e.rows == nil || len(e.packed) != 0 {
		t.Fatal("canceled batch changed representation")
	}
}

func TestManagementPackedModelRowsRejectMalformedLengthFields(t *testing.T) {
	// A correct gzip CRC does not establish valid internal length/count fields.
	prefix := binary.AppendUvarint(nil, 1)
	prefix = binary.AppendUvarint(prefix, 1)
	prefix = append(prefix, 'a')
	prefix = binary.AppendVarint(prefix, -123)
	for _, tail := range [][]byte{
		{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
		{10, 0, 0, 0},   // model length exceeds remaining bytes
		{0, 0, 0, 5},    // result length exceeds remaining bytes
		{0, 0, 0, 0, 1}, // complete row followed by trailing garbage
	} {
		raw := append(bytes.Clone(prefix), tail...)
		var output bytes.Buffer
		writer := gzip.NewWriter(&output)
		if _, err := writer.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		e := managementPageModelStream{group: managementPageModelGroup{"a", -123}, packed: output.Bytes(), packedBytes: len(raw), packedRows: 1}
		if rows, ok := e.unpack(context.Background()); ok || len(rows) != 0 {
			t.Fatal("valid CRC hid malformed inner payload")
		}
	}
}

func TestManagementPackedModelRowsKeepThreeWidePagesWithinOriginalBudget(t *testing.T) {
	var before, after managementPageModelStreams
	version := managementPageModelVersion{managementDirectoryVersion{&sql.DB{}, "owner", "1", 2}, "3"}
	for page := 0; page < 3; page++ {
		packer := managementModelPacker{}
		for g := 0; g < 25; g++ {
			e := managementPackingFixture(int64(page*25 + g))
			e.version = version
			before.put([]*managementPageModelStream{e})
			packed := *e
			packer.pack(context.Background(), &packed)
			after.put([]*managementPageModelStream{&packed})
		}
	}
	if len(before.current(version)) >= 75 || len(after.current(version)) != 75 || after.bytes > managementPageModelBudget {
		t.Fatal("same-budget admission did not prevent wide-page eviction", len(before.entries), len(after.entries), after.bytes)
	}
	t.Logf("raw_retained_groups=%d packed_retained_groups=%d raw_store_bytes=%d packed_store_bytes=%d budget=%d", len(before.entries), len(after.entries), before.bytes, after.bytes, managementPageModelBudget)
	for _, e := range after.current(version) {
		want := managementPackingFixture(e.group.group)
		got, ok := e.unpack(context.Background())
		if !ok || !reflect.DeepEqual(got, want.rows) {
			t.Fatal("admission lost complete off-model evidence")
		}
	}
}

func TestManagementPackedModelStreamRetainsDecoderErrorsAndCancellation(t *testing.T) {
	database, tx := &sql.DB{}, &sql.Tx{}
	v := managementDirectoryVersion{database, "owner", "1", 2}
	e := managementPackingFixture(1)
	e.version = managementPageModelVersion{v, "3"}
	(&managementModelPacker{}).pack(context.Background(), e)
	directory := &managementDirectoryScope{store: &managementModelDirectories{}, database: database, tx: tx, owner: "owner", accountRevision: "1", modelRevision: 2, ready: true, directory: &managementModelDirectory{version: v, ordered: true}}
	scope := &managementPageModelScope{store: &managementPageModelStreams{}, tx: tx, summaries: "3"}
	scope.store.put([]*managementPageModelStream{e})
	ctx := context.WithValue(context.Background(), managementDirectoryScopeKey{}, directory)
	ctx = context.WithValue(ctx, managementPageModelScopeKey{}, scope)
	sentinel := errors.New("consumer decode error")
	calls := 0
	err := readManagementPageModelStream(ctx, tx, "owner", []subBoundKey{{AccountID: "a", GroupID: 1}}, func(row managementPageModelRecord, _ *managementRowsTrace) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) || calls != 1 {
		t.Fatal("packed rows swallowed original decode error", err, calls)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	err = readManagementPageModelStream(ctx, tx, "owner", []subBoundKey{{AccountID: "a", GroupID: 1}}, func(managementPageModelRecord, *managementRowsTrace) error {
		t.Fatal("canceled consumer called")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was hidden", err)
	}
}

func TestManagementPackedModelCorruptionFallsBackToSameTransactionSQL(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`UPDATE console_sub_model_summaries SET result=jsonb_set(result,'{packing_fixture}',to_jsonb(repeat('x',5000))) WHERE account_id='a' AND group_id=1 AND result IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	directory := &managementDirectoryScope{store: &s.modelDirectories, database: s.control.db, tx: tx, owner: "owner"}
	ctx = context.WithValue(ctx, managementDirectoryScopeKey{}, directory)
	proof, _, err := s.managementReadProof(ctx, tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	directory.accountRevision = proof.Epochs["console_sub_accounts"]
	if _, err := readManagementModelDirectory(ctx, tx, "owner", true, func() ([]managementStreamModel, error) { return readManagementModelSourceDirectory(ctx, tx, "owner") }); err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, managementPageModelScopeKey{}, &managementPageModelScope{store: &s.pageModelStreams, tx: tx, summaries: proof.Epochs["console_sub_model_summaries"]})
	wanted := []subBoundKey{{AccountID: "a", GroupID: 1}}
	var original []managementPageModelRecord
	if err := readManagementPageModelStream(ctx, tx, "owner", wanted, func(row managementPageModelRecord, _ *managementRowsTrace) error {
		original = append(original, row)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(s.pageModelStreams.entries) != 1 || len(s.pageModelStreams.entries[0].packed) == 0 {
		t.Fatal("fixture did not enter compressed storage")
	}
	s.pageModelStreams.entries[0].packed[0] ^= 255
	var got []managementPageModelRecord
	if err := readManagementPageModelStream(ctx, tx, "owner", wanted, func(row managementPageModelRecord, _ *managementRowsTrace) error { got = append(got, row); return nil }); err != nil || !reflect.DeepEqual(got, original) {
		t.Fatal("corruption did not reread the complete same-RR group", err)
	}
	if len(s.pageModelStreams.entries[0].packed) == 0 {
		t.Fatal("successful fallback did not replace corrupted group")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok := s.pageModelStreams.entries[0].unpack(ctx); ok {
		t.Fatal("canceled packed group reused")
	}
	// The sentinel is fixture-only data, not interpreted or dropped by storage.
	if !strings.Contains(string(got[0].result)+string(got[1].result)+string(got[2].result), "packing_fixture") {
		t.Fatal("SQL's additional fields were lost")
	}
}

func BenchmarkManagementModelPackingWidePage(b *testing.B) {
	entries := make([]*managementPageModelStream, 25)
	for g := range entries {
		entries[g] = managementPackingFixture(int64(g))
	}
	b.Run("pack_25x288", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			packer := managementModelPacker{}
			for _, e := range entries {
				copy := *e
				packer.pack(context.Background(), &copy)
			}
		}
	})
	for _, name := range []string{"raw_clone_decode", "packed_decode"} {
		b.Run(name, func(b *testing.B) {
			copy := make([]*managementPageModelStream, len(entries))
			packer := managementModelPacker{}
			for i, e := range entries {
				row := *e
				if name == "packed_decode" {
					packer.pack(context.Background(), &row)
				}
				copy[i] = &row
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for _, e := range copy {
					rows, ok := e.unpack(context.Background())
					if !ok {
						b.Fatal("unpack failed")
					}
					for _, r := range rows {
						row := r
						if len(e.packed) == 0 {
							row = r.clone()
						}
						var result *subResult
						if err := json.Unmarshal(row.result, &result); err != nil {
							b.Fatal(err)
						}
					}
				}
			}
		})
	}
}

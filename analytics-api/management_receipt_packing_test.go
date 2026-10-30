package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
	"unsafe"
)

func receiptPackingFixture(batch, count int) ([]string, map[string]managementStreamReceipt) {
	ids := make([]string, count)
	rows := make(map[string]managementStreamReceipt, count)
	price, tokens := math.SmallestNonzeroFloat64, int64(math.MaxInt64)
	for i := range ids {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", batch, i)))
		ids[i] = fmt.Sprintf("receipt-%x", digest)
		rows[ids[i]] = managementStreamReceipt{valid: true, evidence: bytes.Clone(digest[:]), usage: managementStreamUsage{Status: "matched", InputPrice: &price, OutputTokens: &tokens}}
	}
	return ids, rows
}

func TestManagementPackedReceiptsRetainWidePagesWithinOriginalBudget(t *testing.T) {
	scope, ctx := receiptSetScopeForTest()
	enableManagementReceiptSetScope(ctx, scope.tx, true)
	var allIDs [][]string
	var expected []map[string]managementStreamReceipt
	unpackedBytes := 0
	for batch := 0; batch < 9; batch++ {
		ids, want := receiptPackingFixture(batch, 4096)
		delete(want, ids[len(ids)-1])
		ids[len(ids)-1] = fmt.Sprintf("missing-%d", batch)
		allIDs, expected = append(allIDs, ids), append(expected, want)
		got, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, ids, batch == 0, func() (map[string]managementStreamReceipt, error) { return want, nil })
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("initial complete receipt set changed", err)
		}
		entry := scope.store.entries[len(scope.store.entries)-1]
		if entry.bodySize == 0 || cap(entry.body) != len(entry.body) || cap(entry.absent) != len(entry.absent) {
			t.Fatal("wide entry was not packed or retained unaccounted capacity")
		}
		unpackedBytes += entry.bytes + entry.bodySize - len(entry.body) + max(0, entry.absentSize-len(entry.absent))
	}
	if unpackedBytes <= managementReceiptSetBudget || len(scope.store.entries) != 9 || scope.store.bytes >= managementReceiptSetBudget {
		t.Fatal("fixture did not establish same-budget retention across wide pages", unpackedBytes, scope.store.bytes, len(scope.store.entries))
	}
	accounted := 0
	for _, entry := range scope.store.entries {
		wantBytes := int(unsafe.Sizeof(managementReceiptSet{})) + len(entry.version.owner) + len(entry.version.stamp) + len(entry.version.ids) + len(entry.body) + len(entry.absent)
		if wantBytes != entry.bytes {
			t.Fatal("compressed entry escaped the byte budget", wantBytes, entry.bytes)
		}
		accounted += entry.bytes
	}
	if accounted != scope.store.bytes {
		t.Fatal("store retained unaccounted data")
	}
	for batch, ids := range allIDs {
		got, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, ids, batch == 0, func() (map[string]managementStreamReceipt, error) {
			t.Fatal("still-current packed receipts unnecessarily reached SQL")
			return nil, nil
		})
		if err != nil || !reflect.DeepEqual(got, expected[batch]) {
			t.Fatal("packed roundtrip changed complete typed evidence", batch, err)
		}
	}
	// Different filters still consume the existing subset reader, including a
	// certified projection absence, without turning it into raw SQL absence.
	ids := []string{allIDs[0][0], allIDs[8][0], "missing-0", "unknown"}
	got, missing, err := managementReceiptSubset(ctx, scope.tx, scope.version.owner, ids)
	if err != nil || len(got) != 2 || !reflect.DeepEqual(missing, []string{"unknown"}) {
		t.Fatal("packed subset or projection absence changed", missing, err)
	}
	*got[ids[0]].usage.InputPrice = 9
	again, _, err := managementReceiptSubset(ctx, scope.tx, scope.version.owner, ids[:1])
	if err != nil || !reflect.DeepEqual(again[ids[0]], expected[0][ids[0]]) {
		t.Fatal("decoded evidence aliased immutable storage", err)
	}
	t.Logf("unpacked_bytes=%d packed_bytes=%d entries=%d budget=%d", unpackedBytes, accounted, len(scope.store.entries), managementReceiptSetBudget)
}

func TestManagementPackedRawAbsencesStaySeparateAndFallbackWhenCorrupt(t *testing.T) {
	scope, ctx := receiptSetScopeForTest()
	enableManagementReceiptSetScope(ctx, scope.tx, true)
	ids, _ := receiptPackingFixture(0, 1024)
	if err := readManagementRawReceiptAbsences(ctx, scope.tx, scope.version.owner, ids, map[string]*subUsage{}, func([]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	entry := scope.store.entries[0]
	if entry.absentSize == 0 || len(entry.body) != 0 {
		t.Fatal("raw absence was not packed independently")
	}
	if err := readManagementRawReceiptAbsences(ctx, scope.tx, scope.version.owner, ids, map[string]*subUsage{}, func([]string) error {
		t.Fatal("complete raw absence was reread")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, missing, err := managementReceiptSubset(ctx, scope.tx, scope.version.owner, ids)
	if err != nil || len(got) != 0 || !reflect.DeepEqual(missing, ids) {
		t.Fatal("packed raw absence leaked into projection reuse", err)
	}
	entry.absent = entry.absent[:len(entry.absent)-1]
	calls := 0
	if err := readManagementRawReceiptAbsences(ctx, scope.tx, scope.version.owner, ids, map[string]*subUsage{}, func(raw []string) error {
		calls++
		if !reflect.DeepEqual(raw, ids) {
			t.Fatal("corruption suppressed unverified IDs")
		}
		return nil
	}); err != nil || calls != 1 {
		t.Fatal("corrupt raw absence did not fall back", err, calls)
	}
}

func TestManagementPackedReceiptDataRejectsCorruptionAndCancellation(t *testing.T) {
	raw := bytes.Repeat([]byte("receipt payload\x00\xff"), 1024)
	e := &managementReceiptSet{body: raw, bytes: len(raw) + 256}
	e.pack(context.Background())
	if e.bodySize != len(raw) || len(e.body) >= len(raw) {
		t.Fatal("fixture did not pack")
	}
	for _, variant := range []string{"valid", "gzip", "crc", "short", "long_size", "short_size", "oversize", "negative", "cancel"} {
		t.Run(variant, func(t *testing.T) {
			body, size := bytes.Clone(e.body), e.bodySize
			ctx := context.Background()
			switch variant {
			case "gzip":
				body[0] ^= 255
			case "crc":
				body[len(body)-5] ^= 255
			case "short":
				body = body[:len(body)-1]
			case "long_size":
				size++
			case "short_size":
				size--
			case "oversize":
				size = managementReceiptSetBudget + 1
			case "negative":
				size = -1
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got, valid := unpackManagementReceiptData(ctx, body, size)
			if variant == "valid" {
				if !valid || !bytes.Equal(got, raw) {
					t.Fatal("roundtrip changed exact bytes")
				}
			} else if valid || got != nil {
				t.Fatal("corrupt or canceled data was accepted")
			}
		})
	}
}

func TestManagementPackedReceiptCorruptionCannotSuppressSQL(t *testing.T) {
	for _, part := range []string{"body", "absent"} {
		t.Run(part, func(t *testing.T) {
			scope, ctx := receiptSetScopeForTest()
			enableManagementReceiptSetScope(ctx, scope.tx, true)
			ids, want := receiptPackingFixture(1, 512)
			missing, _ := receiptPackingFixture(2, 512)
			ids = append(ids, missing...)
			read := func() (map[string]managementStreamReceipt, error) { return want, nil }
			if _, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, ids, false, read); err != nil {
				t.Fatal(err)
			}
			entry := scope.store.entries[0]
			if entry.bodySize == 0 || entry.absentSize == 0 {
				t.Fatal("fixture must pack both independent payloads")
			}
			if part == "body" {
				entry.body[len(entry.body)-5] ^= 255
			} else {
				entry.absent[len(entry.absent)-5] ^= 255
			}
			got, pending, err := managementReceiptSubset(ctx, scope.tx, scope.version.owner, ids)
			if err != nil || len(got) != 0 || !reflect.DeepEqual(pending, ids) {
				t.Fatal("damaged payload was partially reused", err)
			}
			if part == "body" {
				calls := 0
				got, err := readManagementReceiptSet(ctx, scope.tx, scope.version.owner, ids, false, func() (map[string]managementStreamReceipt, error) {
					calls++
					return want, nil
				})
				if err != nil || calls != 1 || !reflect.DeepEqual(got, want) {
					t.Fatal("damaged exact hit did not reread complete SQL", err, calls)
				}
			}
		})
	}
}

func TestManagementReceiptPackingPreservesUnpackedAdmissionRules(t *testing.T) {
	for _, kind := range []string{"small", "incompressible", "oversize", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			raw := bytes.Repeat([]byte("receipt"), 1024)
			ctx := context.Background()
			switch kind {
			case "small":
				raw = raw[:100]
			case "incompressible":
				raw = nil
				for i := 0; i < 256; i++ {
					digest := sha256.Sum256([]byte(fmt.Sprint(i)))
					raw = append(raw, digest[:]...)
				}
			case "oversize":
				raw = bytes.Repeat([]byte("x"), managementReceiptSetBudget+1)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			e := &managementReceiptSet{body: raw, bytes: len(raw) + 256}
			before := e.bytes
			e.pack(ctx)
			if !bytes.Equal(e.body, raw) || e.bodySize != 0 || e.bytes != before {
				t.Fatal("unchanged admission must keep original bounded bytes")
			}
		})
	}
}

func BenchmarkManagementReceiptPackedDecode(b *testing.B) {
	_, receipts := receiptPackingFixture(0, 4096)
	rows := make([]managementCachedReceipt, 0, len(receipts))
	for id, r := range receipts {
		rows = append(rows, managementCachedReceipt{id, r.valid, r.evidence, r.usage})
	}
	raw, _ := json.Marshal(rows)
	for _, packed := range []bool{false, true} {
		b.Run(fmt.Sprintf("packed=%t", packed), func(b *testing.B) {
			e := &managementReceiptSet{body: raw, bytes: len(raw) + 256}
			if packed {
				e.pack(context.Background())
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				body, valid := unpackManagementReceiptData(context.Background(), e.body, e.bodySize)
				var got []managementCachedReceipt
				if !valid || json.Unmarshal(body, &got) != nil || len(got) != len(rows) {
					b.Fatal("decode failed")
				}
			}
			b.ReportMetric(float64(e.bytes), "retained_bytes")
		})
	}
}

func BenchmarkManagementReceiptPack(b *testing.B) {
	_, receipts := receiptPackingFixture(0, 4096)
	rows := make([]managementCachedReceipt, 0, len(receipts))
	for id, r := range receipts {
		rows = append(rows, managementCachedReceipt{id, r.valid, r.evidence, r.usage})
	}
	raw, _ := json.Marshal(rows)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := &managementReceiptSet{body: raw, bytes: len(raw) + 256}
		e.pack(context.Background())
		if e.bodySize != len(raw) {
			b.Fatal("fixture failed to pack")
		}
	}
}

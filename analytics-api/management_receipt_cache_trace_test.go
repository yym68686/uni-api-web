package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"testing"
)

func TestManagementReceiptCacheAdmissionDistinguishesEvictions(t *testing.T) {
	for _, name := range []string{"count", "bytes", "both", "replace", "proof", "oversize", "nil"} {
		t.Run(name, func(t *testing.T) {
			store := &managementReceiptSets{}
			version := managementReceiptSetVersion{database: &sql.DB{}, owner: "private-owner", stamp: "private-proof"}
			entry := func(id string, size int) *managementReceiptSet {
				v := version
				v.ids = id
				return &managementReceiptSet{version: v, bytes: size}
			}
			var state managementReceiptCacheAdmission
			want := managementReceiptCacheAdmission{stored: true}
			switch name {
			case "count", "both":
				size := 100
				if name == "both" {
					size = managementReceiptSetBudget / managementReceiptSetLimit
				}
				for i := 0; i < managementReceiptSetLimit; i++ {
					store.put(entry(fmt.Sprint(i), size))
				}
				state = store.put(entry("next", size))
				want.entries, want.bytes = managementReceiptSetLimit, managementReceiptSetLimit*size
				if name == "count" {
					want.countEvictions = 1
				} else {
					want.countAndByteEvictions = 1
				}
			case "bytes":
				store.put(entry("first", managementReceiptSetBudget/2))
				state = store.put(entry("next", managementReceiptSetBudget/2+1))
				want.entries, want.bytes, want.byteEvictions = 1, managementReceiptSetBudget/2+1, 1
			case "replace":
				store.put(entry("same", 100))
				state = store.put(entry("same", 200))
				want.entries, want.bytes, want.replacements = 1, 200, 1
			case "proof":
				store.put(entry("first", 100))
				store.put(entry("second", 100))
				foreign := entry("foreign", 100)
				foreign.version.owner = "another-private-owner"
				store.put(foreign)
				otherDB := entry("other-db", 100)
				otherDB.version.database = &sql.DB{}
				store.put(otherDB)
				version.stamp = "next-private-proof"
				state = store.put(entry("next", 200))
				want.entries, want.bytes, want.versionEvictions = 3, 400, 2
			case "oversize", "nil":
				store.put(entry("existing", 100))
				if name == "nil" {
					state = store.put(nil)
				} else {
					state = store.put(entry("large", managementReceiptSetBudget+1))
				}
				want = managementReceiptCacheAdmission{}
				if len(store.entries) != 1 || store.bytes != 100 {
					t.Fatal("rejected observation changed retained entries")
				}
			}
			if state != want {
				t.Fatalf("admission counts differ: got %+v want %+v", state, want)
			}
			total := 0
			for _, e := range store.entries {
				total += e.bytes
			}
			if total != store.bytes || len(store.entries) > managementReceiptSetLimit || total > managementReceiptSetBudget {
				t.Fatal("diagnostic changed cache budget or accounting")
			}
		})
	}
}

func TestManagementReceiptCacheLookupObservesOnlyExactProofSets(t *testing.T) {
	store := &managementReceiptSets{}
	v := managementReceiptSetVersion{database: &sql.DB{}, owner: "private-owner", stamp: "private-proof", ids: "private-set"}
	current := &managementReceiptSet{version: v, bytes: 100}
	store.put(current)
	// Construct simultaneous old/current entries as a concurrent read can see
	// before a later admission removes the older proof. No lookup mutates them.
	for _, kind := range []string{"old", "strict", "raw", "owner", "database"} {
		other := v
		other.stamp = "old-private-proof"
		switch kind {
		case "strict":
			other.strict = true
		case "raw":
			other.rawAbsence = true
		case "owner":
			other.owner = "other-private-owner"
		case "database":
			other.database = &sql.DB{}
		}
		store.entries = append(store.entries, &managementReceiptSet{version: other, bytes: 100})
		store.bytes += 100
	}
	got, state := store.get(v)
	if got != current || state != (managementReceiptCacheState{entries: 6, bytes: 600, currentEntries: 1, previousProofSets: 1}) {
		t.Fatalf("lookup observed wrong scope: %+v", state)
	}
	v.ids = "not-retained"
	got, state = store.get(v)
	if got != nil || state.previousProofSets != 0 || len(store.entries) != 6 || store.bytes != 600 {
		t.Fatal("miss changed retention or matched a different set")
	}
}

func TestManagementReceiptCacheTraceIsScalarAndOutsideLock(t *testing.T) {
	scope, base := receiptSetScopeForTest()
	enableManagementReceiptSetScope(base, scope.tx, true)
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	ctx := context.WithValue(managementTraceContext(base), consoleRequestIDKey{}, "cache-trace-request")
	read := func() (map[string]managementStreamReceipt, error) {
		return map[string]managementStreamReceipt{"private-receipt-id": {valid: true, evidence: []byte("private-evidence")}}, nil
	}
	for _, call := range []context.Context{base, ctx} {
		if _, err := readManagementReceiptSet(call, scope.tx, scope.version.owner, []string{"private-receipt-id"}, false, read); err != nil {
			t.Fatal(err)
		}
		if call == base && output.Len() != 0 {
			t.Fatal("untraced receipt read emitted diagnostics")
		}
	}
	if !strings.Contains(output.String(), "management_receipt_cache_lookup hit=true strict=false requested=1 entries=1 ") {
		t.Fatal("missing scalar lookup state", output.String())
	}
	output.Reset()
	// log.Writer attempts to take the cache lock. This would deadlock if
	// store observation were emitted inside the critical section.
	log.SetOutput(managementReceiptTraceLockWriter{store: scope.store, output: &output})
	if err := readManagementRawReceiptAbsences(ctx, scope.tx, scope.version.owner, []string{"private-absent-id"}, map[string]*subUsage{}, func([]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{"management_receipt_cache_store raw_absence=true", "evicted_version=0 replaced=0 evicted_count=0 evicted_bytes=0 evicted_both=0", "phase=page request_id=cache-trace-request"} {
		if !strings.Contains(text, expected) {
			t.Fatal("missing scalar admission observation", expected, text)
		}
	}
	for _, secret := range []string{"private-owner", "private-proof", "private-receipt-id", "private-absent-id", "private-evidence"} {
		if strings.Contains(text, secret) {
			t.Fatal("cache trace leaked source evidence")
		}
	}
}

type managementReceiptTraceLockWriter struct {
	store  *managementReceiptSets
	output *bytes.Buffer
}

func (w managementReceiptTraceLockWriter) Write(p []byte) (int, error) {
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	return w.output.Write(p)
}

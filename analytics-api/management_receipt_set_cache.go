package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

// This scope requires management's complete, same-transaction source proof.
// Its version covers receipt source/projection revisions and account ownership;
// unrelated page inputs still invalidate the full page, not these exact reads.
// It never enters selection, detail reads, paging state or public JSON.
type managementReceiptSetScopeKey struct{}
type managementReceiptSetScope struct {
	store    *managementReceiptSets
	tx       *sql.Tx
	version  managementReceiptSetVersion
	complete atomic.Bool
}
type managementReceiptSetVersion struct {
	database          *sql.DB
	owner, stamp, ids string
	strict            bool
	rawAbsence        bool
}
type managementReceiptSet struct {
	version managementReceiptSetVersion
	body    []byte
	// Only IDs requested by a successful complete read and absent from its
	// result. The version's rawAbsence kind separates raw SQL certificates from
	// projection certificates; neither kind can substitute for the other.
	absent []byte
	rows   int
	bytes  int
}
type managementReceiptSets struct {
	mu      sync.Mutex
	entries []*managementReceiptSet
	bytes   int
}
type managementCachedReceipt struct {
	ID       string
	Valid    bool
	Evidence []byte
	Usage    managementStreamUsage
}

const managementReceiptSetBudget = 8 << 20

// Wide pages admit two projection batches plus a small raw-absence set. Eight
// entries evicted current certificates after only three pages at <4 MiB. Keep
// the same total byte budget and a separate bound for small-set lookup work.
const managementReceiptSetLimit = 32

// Scalar observations only. Never retain or log source IDs, ownership values,
// proof stamps or receipt payloads for cache diagnostics.
type managementReceiptCacheState struct {
	entries, bytes, currentEntries, previousProofSets int
}

type managementReceiptCacheAdmission struct {
	stored                                               bool
	entries, bytes                                       int
	versionEvictions, replacements                       int
	countEvictions, byteEvictions, countAndByteEvictions int
}

func managementReceiptSetScopeFromProof(store *managementReceiptSets, database *sql.DB, tx *sql.Tx, owner string, proof managementFreshProof) *managementReceiptSetScope {
	// Rates' proof gate checks the compact receipt revision and all membership,
	// ownership and key epochs. Receipt inputs are a subset of those sources.
	if proof.ReceiptStamp == "" || managementRateScopeFromProof(nil, database, tx, owner, proof) == nil {
		return nil
	}
	return &managementReceiptSetScope{store: store, tx: tx, version: managementReceiptSetVersion{database: database, owner: owner, stamp: proof.ReceiptStamp}}
}

func enableManagementReceiptSetScope(ctx context.Context, db controlQueryDB, complete bool) {
	if !complete {
		return
	}
	scope, _ := ctx.Value(managementReceiptSetScopeKey{}).(*managementReceiptSetScope)
	tx, pinned := db.(*sql.Tx)
	if scope != nil && pinned && tx != nil && tx == scope.tx {
		scope.complete.Store(true)
	}
}

func managementReceiptSetScopeFor(ctx context.Context, db controlQueryDB, owner string, ids []string) *managementReceiptSetScope {
	scope, _ := ctx.Value(managementReceiptSetScopeKey{}).(*managementReceiptSetScope)
	tx, pinned := db.(*sql.Tx)
	if scope == nil || scope.store == nil || !scope.complete.Load() || !pinned || tx == nil || tx != scope.tx || scope.version.database == nil || owner != scope.version.owner || scope.version.stamp == "" || len(ids) == 0 || len(ids) > 4096 {
		return nil
	}
	return scope
}

func (c *managementReceiptSets) get(version managementReceiptSetVersion) (*managementReceiptSet, managementReceiptCacheState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := managementReceiptCacheState{entries: len(c.entries), bytes: c.bytes}
	var found *managementReceiptSet
	for _, entry := range c.entries {
		v := entry.version
		if v.database == version.database && v.owner == version.owner {
			if v.stamp == version.stamp {
				state.currentEntries++
			} else if v.ids == version.ids && v.strict == version.strict && v.rawAbsence == version.rawAbsence {
				state.previousProofSets++
			}
		}
		if entry.version == version {
			found = entry
		}
	}
	return found, state
}

func (c *managementReceiptSets) put(entry *managementReceiptSet) (state managementReceiptCacheAdmission) {
	if entry == nil || entry.bytes > managementReceiptSetBudget {
		return state
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	remove := func(i int) {
		c.bytes -= c.entries[i].bytes
		copy(c.entries[i:], c.entries[i+1:])
		c.entries[len(c.entries)-1] = nil
		c.entries = c.entries[:len(c.entries)-1]
	}
	for i := len(c.entries) - 1; i >= 0; i-- {
		old := c.entries[i].version
		if old.database == entry.version.database && old.owner == entry.version.owner && (old.stamp != entry.version.stamp || old == entry.version) {
			if old.stamp != entry.version.stamp {
				state.versionEvictions++
			} else {
				state.replacements++
			}
			remove(i)
		}
	}
	for len(c.entries) > 0 && (len(c.entries) >= managementReceiptSetLimit || c.bytes+entry.bytes > managementReceiptSetBudget) {
		count, bytes := len(c.entries) >= managementReceiptSetLimit, c.bytes+entry.bytes > managementReceiptSetBudget
		switch {
		case count && bytes:
			state.countAndByteEvictions++
		case count:
			state.countEvictions++
		case bytes:
			state.byteEvictions++
		}
		remove(0)
	}
	c.entries = append(c.entries, entry)
	c.bytes += entry.bytes
	state.stored, state.entries, state.bytes = true, len(c.entries), c.bytes
	return state
}

func storeManagementReceiptSet(ctx context.Context, store *managementReceiptSets, entry *managementReceiptSet) bool {
	state := store.put(entry)
	// Logging is deliberately outside the cache lock. Admission/eviction rules
	// and immutable cached values are unchanged by observation.
	if managementTraceEnabled(ctx) {
		log.Printf("management_receipt_cache_store raw_absence=%t strict=%t stored=%t entry_rows=%d entry_bytes=%d entries=%d bytes=%d evicted_version=%d replaced=%d evicted_count=%d evicted_bytes=%d evicted_both=%d phase=%s request_id=%s", entry.version.rawAbsence, entry.version.strict, state.stored, entry.rows, entry.bytes, state.entries, state.bytes, state.versionEvictions, state.replacements, state.countEvictions, state.byteEvictions, state.countAndByteEvictions, managementReceiptPhase(ctx), consoleRequestID(ctx))
	}
	return state.stored
}

// Reuse only the selected-ID query's exact set and strictness. Missing receipts
// remain missing map entries; they still take the existing raw fallback later.
// Only encoded values are retained, so evidence bytes and numeric pointers are
// independently reconstructed on a hit. Large owner scans stay on their reader.
func readManagementReceiptSet(ctx context.Context, db controlQueryDB, owner string, ids []string, strict bool, read func() (map[string]managementStreamReceipt, error)) (map[string]managementStreamReceipt, error) {
	scope := managementReceiptSetScopeFor(ctx, db, owner, ids)
	if scope == nil {
		return read()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	wanted := slices.Clone(ids)
	slices.Sort(wanted)
	wanted = slices.Compact(wanted)
	encodedIDs, _ := json.Marshal(wanted)
	version := scope.version
	version.ids, version.strict = tokenHash(string(encodedIDs)), strict
	entry, state := scope.store.get(version)
	if managementTraceEnabled(ctx) {
		log.Printf("management_receipt_cache_lookup hit=%t strict=%t requested=%d entries=%d bytes=%d current_entries=%d previous_proof_sets=%d phase=%s request_id=%s", entry != nil, strict, len(wanted), state.entries, state.bytes, state.currentEntries, state.previousProofSets, managementReceiptPhase(ctx), consoleRequestID(ctx))
	}
	if entry != nil {
		var rows []managementCachedReceipt
		if json.Unmarshal(entry.body, &rows) == nil {
			var out map[string]managementStreamReceipt
			if rows != nil {
				out = make(map[string]managementStreamReceipt, len(rows))
			}
			for _, row := range rows {
				out[row.ID] = managementStreamReceipt{valid: row.Valid, evidence: row.Evidence, usage: row.Usage}
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if managementTraceEnabled(ctx) {
				log.Printf("management_receipt_set_cache hit=true requested=%d rows=%d bytes=%d request_id=%s", len(wanted), entry.rows, entry.bytes, consoleRequestID(ctx))
			}
			return out, nil
		}
	}
	out, err := read()
	if err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rows []managementCachedReceipt
	if out != nil {
		rows = make([]managementCachedReceipt, 0, len(out))
	}
	for id, receipt := range out {
		rows = append(rows, managementCachedReceipt{id, receipt.valid, receipt.evidence, receipt.usage})
	}
	body, encodeErr := json.Marshal(rows)
	var absentIDs []string
	for _, id := range wanted {
		if _, found := out[id]; !found {
			absentIDs = append(absentIDs, id)
		}
	}
	var absent []byte
	var absentErr error
	if len(absentIDs) != 0 {
		absent, absentErr = json.Marshal(absentIDs)
	}
	n := int(unsafe.Sizeof(managementReceiptSet{})) + len(version.owner) + len(version.stamp) + len(version.ids) + len(body) + len(absent)
	stored := encodeErr == nil && absentErr == nil && n <= managementReceiptSetBudget
	if stored {
		version.owner, version.stamp = strings.Clone(version.owner), strings.Clone(version.stamp)
		stored = storeManagementReceiptSet(ctx, scope.store, &managementReceiptSet{version: version, body: append(make([]byte, 0, len(body)), body...), absent: append(make([]byte, 0, len(absent)), absent...), rows: len(out), bytes: n})
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_receipt_set_cache hit=false stored=%t requested=%d rows=%d request_id=%s", stored, len(wanted), len(out), consoleRequestID(ctx))
	}
	return out, nil
}

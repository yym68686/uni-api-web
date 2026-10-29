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
}
type managementReceiptSet struct {
	version managementReceiptSetVersion
	body    []byte
	rows    int
	bytes   int
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

func (c *managementReceiptSets) get(version managementReceiptSetVersion) *managementReceiptSet {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		if entry.version == version {
			return entry
		}
	}
	return nil
}

func (c *managementReceiptSets) put(entry *managementReceiptSet) {
	if entry == nil || entry.bytes > managementReceiptSetBudget {
		return
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
			remove(i)
		}
	}
	for len(c.entries) > 0 && (len(c.entries) >= 8 || c.bytes+entry.bytes > managementReceiptSetBudget) {
		remove(0)
	}
	c.entries = append(c.entries, entry)
	c.bytes += entry.bytes
}

// Reuse only the selected-ID query's exact set and strictness. Missing receipts
// remain missing map entries; they still take the existing raw fallback later.
// Only encoded values are retained, so evidence bytes and numeric pointers are
// independently reconstructed on a hit. Large owner scans stay on their reader.
func readManagementReceiptSet(ctx context.Context, db controlQueryDB, owner string, ids []string, strict bool, read func() (map[string]managementStreamReceipt, error)) (map[string]managementStreamReceipt, error) {
	scope, _ := ctx.Value(managementReceiptSetScopeKey{}).(*managementReceiptSetScope)
	tx, pinned := db.(*sql.Tx)
	if scope == nil || scope.store == nil || !scope.complete.Load() || !pinned || tx == nil || tx != scope.tx || scope.version.database == nil || owner != scope.version.owner || scope.version.stamp == "" || len(ids) == 0 || len(ids) > 4096 {
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
	if entry := scope.store.get(version); entry != nil {
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
	n := int(unsafe.Sizeof(managementReceiptSet{})) + len(version.owner) + len(version.stamp) + len(version.ids) + len(body)
	stored := encodeErr == nil && n <= managementReceiptSetBudget
	if stored {
		version.owner, version.stamp = strings.Clone(version.owner), strings.Clone(version.stamp)
		scope.store.put(&managementReceiptSet{version: version, body: append(make([]byte, 0, len(body)), body...), rows: len(out), bytes: n})
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_receipt_set_cache hit=false stored=%t requested=%d rows=%d request_id=%s", stored, len(wanted), len(out), consoleRequestID(ctx))
	}
	return out, nil
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"unsafe"
)

// Only management's read-only RR snapshot installs this scope. ExportStamp
// includes compact receipt revisions (including rate_result), ownership,
// targets/current keys, usage membership and retained-rate epochs. A polling
// clock or a gateway export timestamp is not an input to the rate query.
type managementRateScopeKey struct{}
type managementRateScope struct {
	store   *managementRateSnapshots
	tx      *sql.Tx
	version managementRateVersion
}
type managementRateVersion struct {
	database     *sql.DB
	owner, stamp string
}
type managementRateSnapshot struct {
	version        managementRateVersion
	body           []byte
	groups, models int
	bytes          int
}
type managementRateSnapshots struct {
	mu      sync.Mutex
	entries []*managementRateSnapshot
	bytes   int
}

const managementRateBudget = 4 << 20

func managementRateScopeFromProof(store *managementRateSnapshots, database *sql.DB, tx *sql.Tx, owner string, proof managementFreshProof) *managementRateScope {
	if proof.ExportStamp == "" {
		return nil
	}
	for _, relation := range []string{"console_sub_accounts", "console_sub_targets", "console_sub_usage", "console_sub_observed_rates"} {
		if proof.Epochs[relation] == "" {
			return nil
		}
	}
	return &managementRateScope{store: store, tx: tx, version: managementRateVersion{database, owner, proof.ExportStamp}}
}

func (c *managementRateSnapshots) get(version managementRateVersion) *managementRateSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		if entry.version == version {
			return entry
		}
	}
	return nil
}

func (c *managementRateSnapshots) put(entry *managementRateSnapshot) {
	if entry == nil || entry.bytes > managementRateBudget {
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
	for i, old := range c.entries {
		if old.version.database == entry.version.database && old.version.owner == entry.version.owner {
			remove(i)
			break
		}
	}
	for len(c.entries) > 0 && (len(c.entries) >= 2 || c.bytes+entry.bytes > managementRateBudget) {
		remove(0)
	}
	c.entries = append(c.entries, entry)
	c.bytes += entry.bytes
}

// The caller still checks projection completeness in the current transaction.
// Only successful prepared reads are retained; unsupported candidates fall back
// to the original decoder outside this helper and cannot seed a cache entry.
// Encoded bytes have exact capacity and are never exposed to a caller. Decode
// into a new typed map on each hit, preserving nil/empty maps and float values.
func readManagementRates(ctx context.Context, db controlQueryDB, database *sql.DB, owner, accountID string, read func() (map[string]map[string]subObservedRate, error)) (map[string]map[string]subObservedRate, error) {
	scope, _ := ctx.Value(managementRateScopeKey{}).(*managementRateScope)
	tx, pinned := db.(*sql.Tx)
	if scope == nil || scope.store == nil || !pinned || tx == nil || tx != scope.tx || database == nil || database != scope.version.database || owner != scope.version.owner || scope.version.stamp == "" || accountID != "" {
		return read()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if entry := scope.store.get(scope.version); entry != nil {
		var out map[string]map[string]subObservedRate
		if json.Unmarshal(entry.body, &out) == nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if managementTraceEnabled(ctx) {
				log.Printf("management_rate_cache hit=true groups=%d models=%d bytes=%d request_id=%s", entry.groups, entry.models, entry.bytes, consoleRequestID(ctx))
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
	body, encodeErr := json.Marshal(out)
	version := scope.version
	n := int(unsafe.Sizeof(managementRateSnapshot{})) + len(version.owner) + len(version.stamp) + len(body)
	stored := encodeErr == nil && n <= managementRateBudget
	if stored {
		version.owner, version.stamp = strings.Clone(version.owner), strings.Clone(version.stamp)
		entry := &managementRateSnapshot{version: version, body: append(make([]byte, 0, len(body)), body...), bytes: n, groups: len(out)}
		for _, models := range out {
			entry.models += len(models)
		}
		scope.store.put(entry)
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_rate_cache hit=false stored=%t request_id=%s", stored, consoleRequestID(ctx))
	}
	return out, nil
}

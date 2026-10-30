package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"unsafe"
)

// Retain only the narrow SQL projection before receipts, gateway facts, filters
// or display hydration are applied. Both source and projection revisions are
// necessary: source counters intentionally exclude result-only updates.
type managementPredicateValue struct {
	Account, Model                         string
	Group                                  int64
	Valid, Present, HasUsage               bool
	Evidence                               []byte
	Probe, Quality                         string
	CheckedAt                              int64
	Verdict, Availability, Terminal, Match string
	Usage                                  managementStreamUsage
}

type managementPredicateVersion struct {
	directory managementDirectoryVersion
	signal    int64
	selection string
}

type managementPredicateSet struct {
	version managementPredicateVersion
	body    []byte
	rows    int
	bytes   int
}

type managementPredicateSets struct {
	mu      sync.Mutex
	entries []*managementPredicateSet
	bytes   int
}

const managementPredicateBudget = 4 << 20

func managementPredicateCacheVersion(ctx context.Context, db controlQueryDB, owner, keys string, seek, strict bool) (*managementPredicateSets, managementPredicateVersion) {
	scope := managementDirectoryScopeFor(ctx, db, owner)
	if !seek || !strict || scope == nil || scope.predicates == nil || !scope.ready || scope.accountRevision == "" || scope.signalRevision == nil || *scope.signalRevision < 0 || keys == "" {
		return nil, managementPredicateVersion{}
	}
	return scope.predicates, managementPredicateVersion{
		directory: managementDirectoryVersion{scope.database, owner, scope.accountRevision, scope.modelRevision},
		signal:    *scope.signalRevision, selection: tokenHash(keys),
	}
}

func (c *managementPredicateSets) get(ctx context.Context, version managementPredicateVersion) ([]managementPredicateValue, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	var entry *managementPredicateSet
	for _, candidate := range c.entries {
		if candidate.version == version {
			entry = candidate
			break
		}
	}
	c.mu.Unlock()
	if entry == nil {
		return nil, false
	}
	// The immutable bytes never leave the cache. Every request receives its own
	// evidence/usage pointers; same-snapshot receipt joins cannot mutate a hit.
	var out []managementPredicateValue
	if json.Unmarshal(entry.body, &out) != nil || ctx.Err() != nil {
		return nil, false
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_predicate_cache hit=true rows=%d bytes=%d request_id=%s", entry.rows, entry.bytes, consoleRequestID(ctx))
	}
	return out, true
}

func (c *managementPredicateSets) put(ctx context.Context, version managementPredicateVersion, values []managementPredicateValue, positions managementStreamPositionIndex) {
	if c == nil || ctx.Err() != nil || len(values) != len(positions.values) {
		return
	}
	seen := make(map[managementStreamIdentity]bool, len(values))
	for _, value := range values {
		key := managementStreamIdentity{value.Account, value.Group, value.Model}
		_, exists := positions.values[key]
		if !exists || seen[key] || !value.Valid || len(value.Evidence) != sha256.Size {
			return // Never retain partial, invalid, duplicate or off-source output.
		}
		seen[key] = true
	}
	body, err := json.Marshal(values)
	n := int(unsafe.Sizeof(managementPredicateSet{})) + len(version.directory.owner) + len(version.directory.account) + len(version.selection) + len(body)
	stored := err == nil && n <= managementPredicateBudget && ctx.Err() == nil
	if stored {
		version.directory.owner = strings.Clone(version.directory.owner)
		version.directory.account = strings.Clone(version.directory.account)
		version.selection = strings.Clone(version.selection)
		entry := &managementPredicateSet{version, append(make([]byte, 0, len(body)), body...), len(values), n}
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
			if old.directory.database == version.directory.database && old.directory.owner == version.directory.owner &&
				(old.directory != version.directory || old.signal != version.signal || old.selection == version.selection) {
				remove(i)
			}
		}
		for len(c.entries) > 0 && (len(c.entries) >= 2 || c.bytes+entry.bytes > managementPredicateBudget) {
			remove(0)
		}
		c.entries = append(c.entries, entry)
		c.bytes += entry.bytes
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_predicate_cache hit=false stored=%t rows=%d request_id=%s", stored, len(values), consoleRequestID(ctx))
	}
}

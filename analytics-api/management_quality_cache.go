package main

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"strings"
	"unsafe"
)

// Only management's read-only repeatable-read transaction installs this scope.
// The query depends on account ownership, original history and immutable group
// links. Their existing statement epochs are read in that same transaction;
// there is no TTL, retained binding attribution, or action-path cache.
type managementQualityScopeKey struct{}
type managementQualityScope struct {
	store   *managementTargetStreams
	tx      *sql.Tx
	version managementQualityVersion
}
type managementQualityVersion struct {
	database                        *sql.DB
	owner, accounts, history, links string
}
type managementQualityRecord struct {
	account, source, verdict string
	group, at                int64
	history                  qualitySummary
	raw                      []byte
}
type managementQualityStream struct {
	version managementQualityVersion
	rows    []managementQualityRecord
	bytes   int
	order   uint64
}

func managementQualityScopeFromProof(store *managementTargetStreams, database *sql.DB, tx *sql.Tx, owner string, proof managementFreshProof) *managementQualityScope {
	version := managementQualityVersion{database, owner, proof.Epochs["console_sub_accounts"], proof.Epochs["console_quality_history"], proof.Epochs["console_quality_group_links"]}
	if proof.ExportStamp == "" || version.accounts == "" || version.history == "" || version.links == "" {
		return nil
	}
	return &managementQualityScope{store: store, tx: tx, version: version}
}

func (r managementQualityRecord) retainedBytes() int {
	return int(unsafe.Sizeof(r)) + len(r.account) + len(r.source) + len(r.verdict) + len(r.raw)
}

func (r managementQualityRecord) clone() managementQualityRecord {
	r.account, r.source, r.verdict = strings.Clone(r.account), strings.Clone(r.source), strings.Clone(r.verdict)
	r.raw = bytes.Clone(r.raw)
	return r
}

func (c *managementTargetStreams) getQuality(version managementQualityVersion) *managementQualityStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.quality {
		if entry.version == version {
			return entry
		}
	}
	return nil
}

func (c *managementTargetStreams) putQuality(entry *managementQualityStream) {
	if entry == nil || entry.bytes > managementTargetBudget {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, old := range c.quality {
		if old.version.database == entry.version.database && old.version.owner == entry.version.owner {
			c.removeQuality(i)
			break
		}
	}
	for len(c.entries)+len(c.quality) > 0 && (len(c.entries)+len(c.quality) >= 2 || c.bytes+entry.bytes > managementTargetBudget) {
		c.removeOldestStream()
	}
	c.order++
	entry.order = c.order
	c.quality = append(c.quality, entry)
	c.bytes += entry.bytes
}

type managementQualityConsumer func(managementQualityRecord, *managementRowsTrace) error

// Retain complete raw rows only after every original typed JSON decoder has
// succeeded. A hit reruns those decoders with independent bytes and request-local
// bindings; it cannot mask malformed data or share mutable result pointers.
func readManagementQualityStream(ctx context.Context, db controlQueryDB, owner, accountID string, read func(managementQualityConsumer) error, consume managementQualityConsumer) error {
	scope, _ := ctx.Value(managementQualityScopeKey{}).(*managementQualityScope)
	tx, pinned := db.(*sql.Tx)
	if scope == nil || scope.store == nil || !pinned || tx == nil || tx != scope.tx || scope.version.database == nil || scope.version.owner != owner || accountID != "" || scope.version.accounts == "" || scope.version.history == "" || scope.version.links == "" {
		return read(consume)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry := scope.store.getQuality(scope.version); entry != nil {
		trace := newManagementRowsTrace(ctx, "shared_quality_cached_rows")
		defer trace.finish()
		for _, row := range entry.rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := consume(row.clone(), trace); err != nil {
				return err
			}
		}
		if managementTraceEnabled(ctx) {
			log.Printf("management_quality_cache hit=true rows=%d bytes=%d request_id=%s", len(entry.rows), entry.bytes, consoleRequestID(ctx))
		}
		return ctx.Err()
	}
	version := scope.version
	version.owner, version.accounts = strings.Clone(version.owner), strings.Clone(version.accounts)
	version.history, version.links = strings.Clone(version.history), strings.Clone(version.links)
	entry := &managementQualityStream{version: version, bytes: int(unsafe.Sizeof(managementQualityStream{})) + len(version.owner) + len(version.accounts) + len(version.history) + len(version.links)}
	admit := entry.bytes <= managementTargetBudget
	err := read(func(row managementQualityRecord, trace *managementRowsTrace) error {
		if err := consume(row, trace); err != nil {
			return err
		}
		if admit {
			n := row.retainedBytes()
			if n > managementTargetBudget-entry.bytes {
				admit, entry.rows = false, nil
			} else {
				entry.rows = append(entry.rows, row.clone())
				entry.bytes += n
			}
		}
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && admit {
		entry.rows = append(make([]managementQualityRecord, 0, len(entry.rows)), entry.rows...)
		scope.store.putQuality(entry)
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_quality_cache hit=false stored=%t request_id=%s", err == nil && admit, consoleRequestID(ctx))
	}
	return err
}

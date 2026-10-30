package main

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"strings"
	"sync"
	"unsafe"
)

// Retain only the raw target stream, before model/receipt/quality attachment.
// All versions come from the SAME read-only RR transaction as the reader. The
// existing statement triggers cover every target/capability column and account
// ownership. Never use a time-based lifetime or reuse an expanded result graph.
type managementTargetScopeKey struct{}
type managementTargetScope struct {
	store   *managementTargetStreams
	tx      *sql.Tx
	version managementTargetVersion
}
type managementTargetVersion struct {
	database                       *sql.DB
	owner, accounts, targets, caps string
}
type managementTargetRecord struct {
	account                  string
	target                   subTarget
	result, billing, compact []byte
	tools                    []byte
}

func (r managementTargetRecord) retainedBytes() int {
	n := int(unsafe.Sizeof(r))
	for _, value := range []string{r.account, r.target.Name, r.target.Platform, r.target.Channel, r.target.State, r.target.Message, r.target.CompactionState, r.target.ToolUseState} {
		n += len(value)
	}
	return n + len(r.result) + len(r.billing) + len(r.compact) + len(r.tools)
}
func (r managementTargetRecord) clone() managementTargetRecord {
	r.account = strings.Clone(r.account)
	r.target.Name, r.target.Platform = strings.Clone(r.target.Name), strings.Clone(r.target.Platform)
	r.target.Channel, r.target.State = strings.Clone(r.target.Channel), strings.Clone(r.target.State)
	r.target.Message = strings.Clone(r.target.Message)
	r.target.CompactionState, r.target.ToolUseState = strings.Clone(r.target.CompactionState), strings.Clone(r.target.ToolUseState)
	r.result, r.billing = bytes.Clone(r.result), bytes.Clone(r.billing)
	r.compact, r.tools = bytes.Clone(r.compact), bytes.Clone(r.tools)
	return r
}

func (r managementTargetRecord) rawOnly() bool {
	t := r.target
	return t.Result == nil && t.Billing == nil && t.Compaction == nil && t.ToolUse == nil && t.Models == nil && t.ObservedRates == nil && t.QualityCheck == nil && t.CheckSourceID == "" && !t.DetailsOmitted
}

type managementTargetStream struct {
	version managementTargetVersion
	rows    []managementTargetRecord
	bytes   int
	order   uint64
}
type managementTargetStreams struct {
	mu      sync.Mutex
	entries []*managementTargetStream
	quality []*managementQualityStream
	bytes   int
	order   uint64
}

// Target and quality raw streams share the original retention ceilings. This
// does not add a second byte budget or permit extra retained stream entries.
const managementTargetBudget = 8 << 20

func (c *managementTargetStreams) get(version managementTargetVersion) *managementTargetStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		if entry.version == version {
			return entry
		}
	}
	return nil
}
func (c *managementTargetStreams) put(entry *managementTargetStream) {
	if entry == nil || entry.bytes > managementTargetBudget {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, old := range c.entries {
		if old.version.database == entry.version.database && old.version.owner == entry.version.owner {
			c.removeTarget(i)
			break
		}
	}
	for len(c.entries)+len(c.quality) > 0 && (len(c.entries)+len(c.quality) >= 2 || c.bytes+entry.bytes > managementTargetBudget) {
		c.removeOldestStream()
	}
	c.order++
	entry.order = c.order
	c.entries = append(c.entries, entry)
	c.bytes += entry.bytes
}

// All removal/admission helpers are called with the shared mutex held.
func (c *managementTargetStreams) removeTarget(i int) {
	c.bytes -= c.entries[i].bytes
	copy(c.entries[i:], c.entries[i+1:])
	c.entries[len(c.entries)-1] = nil
	c.entries = c.entries[:len(c.entries)-1]
}

func (c *managementTargetStreams) removeQuality(i int) {
	c.bytes -= c.quality[i].bytes
	copy(c.quality[i:], c.quality[i+1:])
	c.quality[len(c.quality)-1] = nil
	c.quality = c.quality[:len(c.quality)-1]
}

func (c *managementTargetStreams) removeOldestStream() {
	if len(c.quality) > 0 && (len(c.entries) == 0 || c.quality[0].order < c.entries[0].order) {
		c.removeQuality(0)
	} else {
		c.removeTarget(0)
	}
}

type managementTargetConsumer func(managementTargetRecord, *managementRowsTrace) error

// Only the full management snapshot with complete shared-quality projection
// can enter here. Other account/detail/action readers retain their SQL stream.
// The callback still runs the original typed JSON decoders for every row.
func readManagementTargetStream(ctx context.Context, db controlQueryDB, owner string, enabled bool, read func(managementTargetConsumer) error, consume managementTargetConsumer) error {
	scope, _ := ctx.Value(managementTargetScopeKey{}).(*managementTargetScope)
	tx, pinned := db.(*sql.Tx)
	if !enabled || scope == nil || scope.store == nil || !pinned || tx == nil || tx != scope.tx || scope.version.database == nil || scope.version.owner != owner || scope.version.accounts == "" || scope.version.targets == "" || scope.version.caps == "" {
		return read(consume)
	}
	if err := ctx.Err(); err != nil {
		return subAccountReadFailure{"检测结果暂不可用", err}
	}
	if entry := scope.store.get(scope.version); entry != nil {
		trace := newManagementRowsTrace(ctx, "target_cached_rows")
		defer trace.finish()
		for _, row := range entry.rows {
			if err := ctx.Err(); err != nil {
				return subAccountReadFailure{"检测结果读取失败", err}
			}
			// Keep consumers independent even if a future decoder edits raw bytes.
			if err := consume(row.clone(), trace); err != nil {
				return subAccountReadFailure{"检测结果读取失败", err}
			}
		}
		if managementTraceEnabled(ctx) {
			log.Printf("management_target_cache hit=true rows=%d bytes=%d request_id=%s", len(entry.rows), entry.bytes, consoleRequestID(ctx))
		}
		return ctx.Err()
	}
	version := scope.version
	version.owner = strings.Clone(version.owner)
	version.accounts, version.targets, version.caps = strings.Clone(version.accounts), strings.Clone(version.targets), strings.Clone(version.caps)
	entry := &managementTargetStream{version: version, bytes: 256 + len(version.owner) + len(version.accounts) + len(version.targets) + len(version.caps)}
	admit := entry.bytes <= managementTargetBudget
	err := read(func(row managementTargetRecord, trace *managementRowsTrace) error {
		if err := consume(row, trace); err != nil {
			return err
		}
		if admit && !row.rawOnly() {
			admit, entry.rows = false, nil
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
	if err == nil && ctx.Err() == nil && admit {
		// Do not retain append slack beyond the byte admission accounting.
		entry.rows = append(make([]managementTargetRecord, 0, len(entry.rows)), entry.rows...)
		scope.store.put(entry)
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_target_cache hit=false stored=%t request_id=%s", err == nil && ctx.Err() == nil && admit, consoleRequestID(ctx))
	}
	return err
}

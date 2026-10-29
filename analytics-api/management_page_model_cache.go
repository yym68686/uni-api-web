package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"unsafe"
)

// Retain the unexpanded SQL stream, before queue/receipt/quality attachment.
// Ownership, source metadata and summary epochs must all match the current RR
// transaction. A missing directory/proof keeps the original SQL reader.
type managementPageModelScopeKey struct{}
type managementPageModelScope struct {
	store     *managementPageModelStreams
	tx        *sql.Tx
	summaries string
}
type managementPageModelVersion struct {
	managementDirectoryVersion
	summaries string
}
type managementPageModelRecord struct {
	account               string
	group                 int64
	model, state, message string
	result                []byte
}

func (r managementPageModelRecord) clone() managementPageModelRecord {
	r.account, r.model = strings.Clone(r.account), strings.Clone(r.model)
	r.state, r.message = strings.Clone(r.state), strings.Clone(r.message)
	r.result = bytes.Clone(r.result)
	return r
}
func (r managementPageModelRecord) retainedBytes() int {
	return int(unsafe.Sizeof(r)) + len(r.account) + len(r.model) + len(r.state) + len(r.message) + len(r.result)
}

type managementPageModelStream struct {
	version managementPageModelVersion
	group   managementPageModelGroup
	rows    []managementPageModelRecord
	bytes   int
}
type managementPageModelGroup struct {
	account string
	group   int64
}

func pageModelGroup(group subBoundKey) managementPageModelGroup {
	return managementPageModelGroup{group.AccountID, group.GroupID}
}

type managementPageModelStreams struct {
	mu      sync.Mutex
	entries []*managementPageModelStream
	bytes   int
}

const managementPageModelBudget = 8 << 20
const managementPageModelGroups = 128

func (c *managementPageModelStreams) current(version managementPageModelVersion) map[managementPageModelGroup]*managementPageModelStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[managementPageModelGroup]*managementPageModelStream{}
	for _, e := range c.entries {
		if e.version == version {
			out[e.group] = e
		}
	}
	return out
}
func (c *managementPageModelStreams) put(entries []*managementPageModelStream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	remove := func(i int) {
		c.bytes -= c.entries[i].bytes
		copy(c.entries[i:], c.entries[i+1:])
		c.entries[len(c.entries)-1] = nil
		c.entries = c.entries[:len(c.entries)-1]
	}
	for _, entry := range entries {
		if entry == nil || entry.bytes > managementPageModelBudget {
			continue
		}
		for i := len(c.entries) - 1; i >= 0; i-- {
			old := c.entries[i]
			if old.version.database == entry.version.database && old.version.owner == entry.version.owner && (old.version != entry.version || old.group == entry.group) {
				remove(i)
			}
		}
		for len(c.entries) > 0 && (len(c.entries) >= managementPageModelGroups || c.bytes+entry.bytes > managementPageModelBudget) {
			remove(0)
		}
		c.entries = append(c.entries, entry)
		c.bytes += entry.bytes
	}
}

type managementPageModelConsumer func(managementPageModelRecord, *managementRowsTrace) error

func readManagementPageModelSQL(ctx context.Context, db controlQueryDB, owner string, wanted []subBoundKey, consume managementPageModelConsumer) error {
	raw, err := json.Marshal(wanted)
	if err != nil {
		return err
	}
	query, args, err := managementPageModelQuery(ctx, db, owner, wanted, raw)
	if err != nil {
		return err
	}
	stage := "page_models"
	if query == managementPageDirectoryModelsSQL {
		stage = "page_model_summaries"
	}
	trace := newManagementRowsTrace(ctx, stage)
	defer trace.finish()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	trace.queryReady()
	defer rows.Close()
	for trace.next(rows) {
		var row managementPageModelRecord
		if err = rows.Scan(&row.account, &row.group, &row.model, &row.state, &row.message, &row.result); err != nil {
			return err
		}
		if err = consume(row, trace); err != nil {
			return err
		}
	}
	return rows.Err()
}

func managementPageModelScopeFor(ctx context.Context, db controlQueryDB, owner string, wanted []subBoundKey) (*managementPageModelScope, managementPageModelVersion) {
	scope, _ := ctx.Value(managementPageModelScopeKey{}).(*managementPageModelScope)
	directory := managementDirectoryScopeFor(ctx, db, owner)
	tx, pinned := db.(*sql.Tx)
	if scope == nil || scope.store == nil || !pinned || tx == nil || scope.tx != tx || scope.summaries == "" || directory == nil || !directory.ready || directory.accountRevision == "" || directory.directory == nil || !directory.directory.ordered || len(wanted) == 0 || len(wanted) > managementPageModelGroups {
		return nil, managementPageModelVersion{}
	}
	version := managementPageModelVersion{managementDirectoryVersion{directory.database, owner, directory.accountRevision, directory.modelRevision}, scope.summaries}
	if directory.directory.version != version.managementDirectoryVersion {
		return nil, managementPageModelVersion{}
	}
	seen := map[managementPageModelGroup]bool{}
	for _, group := range wanted {
		if seen[pageModelGroup(group)] {
			return nil, managementPageModelVersion{}
		}
		seen[pageModelGroup(group)] = true
	}
	return scope, version
}

func readManagementPageModelStream(ctx context.Context, db controlQueryDB, owner string, wanted []subBoundKey, consume managementPageModelConsumer) error {
	read := func(groups []subBoundKey, consume managementPageModelConsumer) error {
		return readManagementPageModelSQL(ctx, db, owner, groups, consume)
	}
	scope, version := managementPageModelScopeFor(ctx, db, owner, wanted)
	if scope == nil {
		return read(wanted, consume)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current := scope.store.current(version)
	missing := []subBoundKey{}
	trace := newManagementRowsTrace(ctx, "page_model_cached_rows")
	defer func() { trace.finish() }()
	reused := 0
	for _, group := range wanted {
		entry := current[pageModelGroup(group)]
		if entry == nil {
			missing = append(missing, group)
			continue
		}
		for _, row := range entry.rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := consume(row.clone(), trace); err != nil {
				return err
			}
		}
		reused++
	}
	trace.finish()
	trace = nil
	// Admission is all-or-nothing for the missing batch. Errors/cancellation or
	// oversized evidence never publish a partially read group as complete.
	version.owner, version.account, version.summaries = strings.Clone(version.owner), strings.Clone(version.account), strings.Clone(version.summaries)
	pending := map[managementPageModelGroup]*managementPageModelStream{}
	entries := []*managementPageModelStream{}
	retained := 0
	for _, group := range missing {
		key := managementPageModelGroup{strings.Clone(group.AccountID), group.GroupID}
		e := &managementPageModelStream{version: version, group: key, rows: []managementPageModelRecord{}, bytes: 256 + len(version.owner) + len(version.account) + len(version.summaries) + len(key.account)}
		pending[key] = e
		entries = append(entries, e)
		retained += e.bytes
	}
	admit := retained <= managementPageModelBudget
	var err error
	if len(missing) > 0 {
		err = read(missing, func(row managementPageModelRecord, trace *managementRowsTrace) error {
			if err := consume(row, trace); err != nil {
				return err
			}
			if admit {
				e := pending[managementPageModelGroup{row.account, row.group}]
				n := row.retainedBytes()
				if e == nil || n > managementPageModelBudget-retained {
					admit, pending, entries = false, nil, nil
				} else {
					e.rows = append(e.rows, row.clone())
					e.bytes += n
					retained += n
				}
			}
			return nil
		})
	}
	if err == nil {
		err = ctx.Err()
	}
	stored := 0
	if err == nil && admit {
		for _, e := range entries {
			e.rows = append(make([]managementPageModelRecord, 0, len(e.rows)), e.rows...)
		}
		scope.store.put(entries)
		stored = len(entries)
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_page_model_cache reused_groups=%d raw_groups=%d stored_groups=%d request_id=%s", reused, len(missing), stored, consoleRequestID(ctx))
	}
	return err
}

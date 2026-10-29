package main

import (
	"context"
	"database/sql"
	"log"
	"slices"
	"strings"
	"sync"
)

// Only the five columns of managementModelSourceStreamSQL are retained. The
// existing counters cover every change to these columns and account ownership;
// predicates, receipts, display evidence and gateway facts are NEVER retained
// here. Only the complete management snapshot path installs this private scope.
type managementDirectoryScopeKey struct{}
type managementDirectoryScope struct {
	store           *managementModelDirectories
	database        *sql.DB
	tx              *sql.Tx
	owner           string
	accountRevision string
	modelRevision   int64
	ready           bool
	directory       *managementModelDirectory // Current read only; never a retained mutable envelope.
}

func managementDirectoryScopeFor(ctx context.Context, db controlQueryDB, owner string) *managementDirectoryScope {
	scope, _ := ctx.Value(managementDirectoryScopeKey{}).(*managementDirectoryScope)
	tx, pinned := db.(*sql.Tx)
	if scope == nil || scope.store == nil || scope.database == nil || !pinned || tx == nil || scope.tx != tx || scope.owner != owner {
		return nil
	}
	return scope
}

func managementDirectoryModelRevision(ctx context.Context, db controlQueryDB, owner string, revision *int64) {
	if scope := managementDirectoryScopeFor(ctx, db, owner); scope != nil {
		scope.directory = nil
		scope.ready = revision != nil && *revision >= 0
		if scope.ready {
			scope.modelRevision = *revision
		}
	}
}

type managementDirectoryVersion struct {
	database *sql.DB
	owner    string
	account  string
	model    int64
}

// A dictionary keeps the 190k-row source directory below the bounded budget
// without retaining repeated account/model/state/message string allocations.
// Rows keep the exact source order. Expansion is a NEW request-local graph.
type managementDirectoryRow struct {
	account, model, state, message uint32
	group                          int64
}
type managementModelDirectory struct {
	version managementDirectoryVersion
	strings []string
	rows    []managementDirectoryRow
	order   []uint32
	ordered bool
	bytes   int
}
type managementModelDirectories struct {
	mu      sync.Mutex
	entries []*managementModelDirectory
	bytes   int
}

const managementDirectoryBudget = 16 << 20
const managementDirectoryRowBytes = 24 // four uint32 indices and one int64

func packManagementModelDirectory(version managementDirectoryVersion, models []managementStreamModel) *managementModelDirectory {
	base := 256
	for _, value := range []string{version.owner, version.account} {
		if len(value) > managementDirectoryBudget-base {
			return nil
		}
		base += len(value)
	}
	if models == nil || len(models) > (managementDirectoryBudget-base)/managementDirectoryRowBytes {
		return nil
	}
	version.owner, version.account = strings.Clone(version.owner), strings.Clone(version.account)
	entry := &managementModelDirectory{version: version, rows: make([]managementDirectoryRow, len(models)), bytes: base + len(models)*managementDirectoryRowBytes}
	index := map[string]uint32{}
	add := func(value string) (uint32, bool) {
		if i, exists := index[value]; exists {
			return i, true
		}
		if len(value) > managementDirectoryBudget-entry.bytes-16 {
			return 0, false
		}
		i := uint32(len(entry.strings)) // bounded rows/budget keep this below 2^32.
		entry.bytes += 16 + len(value)
		entry.strings = append(entry.strings, strings.Clone(value))
		index[value] = i
		return i, true
	}
	for i, model := range models {
		// Refuse an accidentally expanded envelope, rather than caching any
		// result or silently discarding new fields on a future caller's input.
		if model.item.Model.Result != nil || model.item.Model.Message != "" || model.evidence != nil || model.probe != "" || model.quality != "" {
			return nil
		}
		values := []string{model.item.Account, model.item.Model.Model, model.item.Model.State, model.message}
		var packed [4]uint32
		for j, value := range values {
			var ok bool
			if packed[j], ok = add(value); !ok {
				return nil
			}
		}
		entry.rows[i] = managementDirectoryRow{packed[0], packed[1], packed[2], packed[3], model.item.Group}
	}
	// Drop append capacity so the retained backing array matches admission.
	entry.strings = append(make([]string, 0, len(entry.strings)), entry.strings...)
	entry.prepareOrder()
	return entry
}

// The three immutable identity columns determine the existing C-byte order.
// Keep indices, not pointers to any request's mutable predicate/result graph.
func (entry *managementModelDirectory) prepareOrder() {
	compare := func(a, b uint32) int {
		left, right := entry.rows[a], entry.rows[b]
		if c := strings.Compare(entry.strings[left.account], entry.strings[right.account]); c != 0 {
			return c
		}
		if left.group < right.group {
			return -1
		}
		if left.group > right.group {
			return 1
		}
		return strings.Compare(entry.strings[left.model], entry.strings[right.model])
	}
	inOrder := true
	for i := 1; i < len(entry.rows); i++ {
		c := compare(uint32(i-1), uint32(i))
		if c == 0 {
			return // Source PK is unique; keep arbitrary duplicate callers on the old sorter.
		}
		inOrder = inOrder && c < 0
	}
	if inOrder {
		entry.ordered = true
		return
	}
	const indexBytes = 4
	if len(entry.rows) > (managementDirectoryBudget-entry.bytes)/indexBytes {
		return // Directory admission itself remains unchanged at the byte boundary.
	}
	order := make([]uint32, len(entry.rows))
	for i := range order {
		order[i] = uint32(i)
	}
	slices.SortFunc(order, compare)
	for i := 1; i < len(order); i++ {
		if compare(order[i-1], order[i]) == 0 {
			return
		}
	}
	entry.order, entry.ordered = order, true
	entry.bytes += len(order) * indexBytes
}

func managementDirectoryStreamOrder(ctx context.Context, db controlQueryDB, owner string, models []managementStreamModel) []*managementStreamModel {
	scope := managementDirectoryScopeFor(ctx, db, owner)
	if scope == nil || !scope.ready || scope.directory == nil {
		return managementOrderedStreamModels(models)
	}
	entry := scope.directory
	version := managementDirectoryVersion{scope.database, owner, scope.accountRevision, scope.modelRevision}
	if entry.version != version || !entry.ordered || len(entry.rows) != len(models) {
		return managementOrderedStreamModels(models)
	}
	// Guard against a future caller changing identity/order between expansion
	// and this stage. Predicate, receipt and result changes are intentionally
	// not part of the ordering identity and are never copied from the cache.
	for i, row := range entry.rows {
		item := models[i].item
		if item.Account != entry.strings[row.account] || item.Group != row.group || item.Model.Model != entry.strings[row.model] {
			return managementOrderedStreamModels(models)
		}
	}
	var order []*managementStreamModel
	if entry.order != nil {
		order = make([]*managementStreamModel, len(entry.order))
		for i, position := range entry.order {
			order[i] = &models[position]
		}
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_model_order reuse=true rows=%d request_id=%s", len(models), consoleRequestID(ctx))
	}
	return order
}

func (entry *managementModelDirectory) expand() []managementStreamModel {
	models := make([]managementStreamModel, len(entry.rows))
	for i, row := range entry.rows {
		models[i].item.Account = entry.strings[row.account]
		models[i].item.Group = row.group
		models[i].item.Model.Model = entry.strings[row.model]
		models[i].item.Model.State = entry.strings[row.state]
		models[i].message = entry.strings[row.message]
	}
	return models
}

func (c *managementModelDirectories) get(version managementDirectoryVersion) *managementModelDirectory {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, entry := range c.entries {
		if entry.version == version {
			copy(c.entries[i:], c.entries[i+1:])
			c.entries[len(c.entries)-1] = entry
			return entry
		}
	}
	return nil
}

func (c *managementModelDirectories) put(entry *managementModelDirectory) {
	if entry == nil || entry.bytes > managementDirectoryBudget {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	remove := func(i int) {
		c.bytes -= c.entries[i].bytes
		copy(c.entries[i:], c.entries[i+1:])
		c.entries[len(c.entries)-1] = nil // No evicted entry in backing-array capacity.
		c.entries = c.entries[:len(c.entries)-1]
	}
	for i, previous := range c.entries {
		if previous.version.database == entry.version.database && previous.version.owner == entry.version.owner {
			remove(i)
			break
		}
	}
	for len(c.entries) > 0 && (len(c.entries) >= 2 || c.bytes+entry.bytes > managementDirectoryBudget) {
		remove(0)
	}
	c.entries = append(c.entries, entry)
	c.bytes += entry.bytes
}

func readManagementModelDirectory(ctx context.Context, db controlQueryDB, owner string, compact bool, read func() ([]managementStreamModel, error)) ([]managementStreamModel, error) {
	scope := managementDirectoryScopeFor(ctx, db, owner)
	if scope != nil {
		scope.directory = nil
	}
	if !compact || scope == nil || !scope.ready || scope.accountRevision == "" {
		return read()
	}
	version := managementDirectoryVersion{scope.database, owner, scope.accountRevision, scope.modelRevision}
	if entry := scope.store.get(version); entry != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		models := entry.expand()
		scope.directory = entry
		if managementTraceEnabled(ctx) {
			log.Printf("management_model_directory_cache hit=true rows=%d bytes=%d request_id=%s", len(models), entry.bytes, consoleRequestID(ctx))
		}
		return models, ctx.Err()
	}
	models, err := read()
	if err != nil || ctx.Err() != nil {
		return models, err
	}
	entry := packManagementModelDirectory(version, models)
	if entry != nil {
		scope.store.put(entry)
		scope.directory = entry
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_model_directory_cache hit=false rows=%d stored=%t request_id=%s", len(models), entry != nil, consoleRequestID(ctx))
	}
	return models, err
}

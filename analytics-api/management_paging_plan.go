package main

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
)

// Keep the evaluated ordering/facets and group metadata, never all model
// payloads. Every use proves this directory is CURRENT before hydrating its page.
type managementPagingRow struct {
	ID      string
	Account string
	Group   int64
	Members []subBoundKey
}
type managementPagingPlan struct {
	Input  managementQueryInput
	Prices []Price
	Page   managementQueryPage
	Rows   []managementPagingRow
}

// Decode once at admission. Account headers are always refreshed; the target
// index points into the immutable directory for direct lookups on site pages.
// Configured aliases and duplicate row IDs keep the general hydration path.
type managementPagingData struct {
	Input  managementQueryInput
	Prices []Price
}

type managementListPagingData struct {
	data    managementPagingData
	site    map[string]subTarget
	headers []subAccount
}

type managementListPagingPlan struct {
	page managementQueryPage
	rows []managementPagingRow
	data *managementListPagingData
}

func managementPagingKey(owner string, filters managementQueryFilters) string {
	return managementComputedKey(owner, filters, "paging-directory")
}
func (s *Service) rememberManagementPagingPlan(owner string, filters managementQueryFilters, snapshot managementQuerySnapshot) bool {
	return s.rememberManagementPagingPlanMode(owner, filters, snapshot, false)
}

func (s *Service) rememberManagementPagingPlanMode(owner string, filters managementQueryFilters, snapshot managementQuerySnapshot, list bool) bool {
	if snapshot.Proof == nil || snapshot.Proof.Stamp == "" || !snapshot.View.ProjectedModels {
		log.Printf("management_paging_store stored=false reason=unprepared projected=%t", snapshot.View.ProjectedModels)
		return false
	}
	in := snapshot.View.Input
	if list && snapshot.DirectoryAccounts != nil {
		in.Accounts = snapshot.DirectoryAccounts
	}
	plan := managementPagingPlan{Input: in, Prices: snapshot.View.Prices, Page: snapshot.Page}
	plan.Page.Rows = nil
	plan.Input.Accounts = make([]subAccount, len(in.Accounts))
	for i, a := range in.Accounts {
		plan.Input.Accounts[i] = a
		if list {
			plan.Input.Accounts[i].Balance = nil
		}
		plan.Input.Accounts[i].Targets = make([]subTarget, len(a.Targets))
		for j, t := range a.Targets {
			t.Models = nil
			plan.Input.Accounts[i].Targets[j] = t
		}
	}
	filtered := snapshot.Filtered
	if filtered == nil {
		// Preserve the helper's existing behavior for snapshots constructed by
		// other callers. A present empty result must not fall back to raw rows.
		result := filterManagementQueryRows(snapshot.Rows, filters, snapshot.Evidence)
		filtered = &result
	}
	for _, row := range filtered.Rows {
		identity := managementPagingRow{ID: row.ID, Account: row.Account.ID, Group: row.Target.GroupID}
		if row.Configured != nil {
			for _, member := range row.Configured.Members {
				identity.Members = append(identity.Members, subBoundKey{AccountID: member.AccountID, GroupID: member.GroupID})
			}
		}
		plan.Rows = append(plan.Rows, identity)
	}
	if len(plan.Rows) != plan.Page.Total {
		log.Printf("management_paging_store stored=false reason=count")
		return false
	}
	if list {
		body, err := json.Marshal(managementPagingData{plan.Input, plan.Prices})
		if err != nil || len(body) > 8<<20 {
			log.Printf("management_paging_store stored=false reason=data_encoding_or_size bytes=%d", len(body))
			return false
		}
		// Account balances get read from the live proof, not the directory.
		// Everything else is shared only when the entire serialized input
		// matches. Filter-dependent differences never alias the same graph.
		key := managementPagingKey(owner, filters)
		dataKey := tokenHash(owner + "\x00" + string(body))
		var slice struct {
			Page managementQueryPage
			Rows []managementPagingRow
		}
		slice.Page, slice.Rows = plan.Page, plan.Rows
		raw, err := json.Marshal(slice)
		if err != nil || len(raw) > 4<<20 || json.Unmarshal(raw, &slice) != nil {
			return false
		}
		entry := managementComputedPage{ListPlan: &managementListPagingPlan{page: slice.Page, rows: slice.Rows}, Bytes: 2*len(raw) + 1024}
		stored, shared := s.managementPaging.put(key, dataKey, snapshot.Proof, entry, body, filters == managementResetFilters)
		log.Printf("management_paging_store stored=%t bytes=%d rows=%d shared=%t", stored, len(raw), len(plan.Rows), shared)
		return stored
	}
	raw, err := json.Marshal(plan)
	if err != nil || len(raw) > 8<<20 {
		log.Printf("management_paging_store stored=false reason=encoding_or_size bytes=%d", len(raw))
		return false
	}
	s.rememberManagementEntry(managementPagingKey(owner, filters), snapshot.Proof, managementComputedPage{Plan: raw, Bytes: len(raw)})
	log.Printf("management_paging_store stored=true bytes=%d rows=%d", len(raw), len(plan.Rows))
	return true
}

func (s *Service) retainedManagementPagingPlan(ctx context.Context, owner string, filters managementQueryFilters) (managementComputedPage, bool) {
	key := managementPagingKey(owner, filters)
	if managementListView(ctx) {
		if entry, ok := s.managementPaging.get(key); ok {
			return entry, true
		}
	}
	return s.managementComputed.get(key)
}

// This is the console Reset filters default (not the no-parameter API default).
var managementResetFilters = managementQueryFilters{GroupStatus: "all"}

// Check the database generation at most once per maintenance interval. This
// is only a scheduling hint; readers still perform the full live gateway and
// database fence before using either precomputed result.
func (s *Service) warmManagementResetPlanIfNeeded(ctx context.Context, owner string) (bool, error) {
	entry, retained := s.retainedManagementPagingPlan(ctx, owner, managementResetFilters)
	if retained {
		proof, at, err := s.managementReadProof(ctx, s.control.db, owner)
		if err != nil {
			return false, err
		}
		if managementProofStamp(proof, entry.Proof.CurrentExport) == entry.Proof.Stamp &&
			(entry.Proof.Until.IsZero() || at.Before(entry.Proof.Until)) {
			first, exists := s.managementComputed.get(managementPageComputedKey(ctx, owner, managementResetFilters, ""))
			if exists && first.Proof.Stamp == entry.Proof.Stamp {
				return false, nil
			}
			// Only the optional page may have timed out. Reuse the directory
			// and retry that page without running the full directory query.
			_, _, err := s.currentManagementPagingPage(ctx, owner, managementResetFilters, "", false)
			return false, err
		}
	}
	return s.warmManagementResetPlan(ctx, owner)
}

func (s *Service) warmManagementResetPlan(ctx context.Context, owner string) (bool, error) {
	snapshot, err := s.readManagementResetDirectory(ctx, owner)
	if err != nil {
		return false, err
	}
	if ctx.Err() != nil || s.managementReaders.Load() != 0 {
		return false, context.Canceled
	}
	return s.retainAndWarmManagementResetDirectory(ctx, owner, snapshot, s.currentManagementPagingPage)
}

func (s *Service) retainAndWarmManagementResetDirectory(ctx context.Context, owner string, snapshot managementQuerySnapshot, warm func(context.Context, string, managementQueryFilters, string, bool) (managementPageResponse, bool, error)) (bool, error) {
	if !s.rememberManagementPagingPlanMode(owner, managementResetFilters, snapshot, managementListView(ctx)) {
		return false, nil
	}
	// The directory has already passed the full fence. A timeout or an incoming
	// foreground read during optional first-page hydration must not discard it.
	// Hydrate through the normal verified paging path: no partial page reaches
	// the response cache, and changes since compilation still invalidate it.
	_, _, err := warm(ctx, owner, managementResetFilters, "", false)
	return true, err
}

func (s *Service) currentManagementPagingPage(ctx context.Context, owner string, filters managementQueryFilters, cursor string, rebase bool) (managementPageResponse, bool, error) {
	ctx = managementTraceContext(ctx)
	entry, exists := s.retainedManagementPagingPlan(ctx, owner, filters)
	if !exists || (len(entry.Plan) == 0 && entry.ListPlan == nil) {
		log.Printf("management_paging_miss reason=absent request_id=%s", consoleRequestID(ctx))
		return managementPageResponse{}, false, nil
	}
	var snapshot managementCurrentSnapshot
	proof, now, valid, err := s.currentManagementProofRetaining(ctx, owner, entry.Proof, &snapshot)
	if err != nil || !valid {
		log.Printf("management_paging_miss reason=proof failed=%t request_id=%s", err != nil, consoleRequestID(ctx))
		return managementPageResponse{}, false, err
	}
	tx := snapshot.tx
	defer tx.Rollback()
	var plan managementPagingPlan
	decodeEnd := managementReadSpan(ctx, "paging_directory_decode")
	if entry.ListPlan != nil {
		plan = managementPagingPlan{
			Input: entry.ListPlan.data.data.Input, Prices: entry.ListPlan.data.data.Prices,
			Page: entry.ListPlan.page, Rows: entry.ListPlan.rows,
		}
	} else if err = json.Unmarshal(entry.Plan, &plan); err != nil {
		decodeEnd()
		return managementPageResponse{}, false, err
	}
	decodeEnd()
	if len(plan.Rows) != plan.Page.Total {
		return managementPageResponse{}, false, errManagementRevision
	}
	page := plan.Page
	page.Rows = []managementQueryRow{}
	page.Offset = 0
	page.NextCursor = ""
	page.PreviousCursor = ""
	if cursor != "" {
		decoded, err := decodeManagementCursor(s.control.key, owner, cursor)
		if err != nil || decoded.Filter != tokenHash(mustJSON(filters)) {
			return managementPageResponse{}, false, errManagementCursor
		}
		if !rebase && decoded.Revision != page.Revision {
			return managementPageResponse{}, false, errManagementRevision
		}
		page.Offset = decoded.Offset
		if page.Offset >= page.Total && page.Offset != 0 {
			if !rebase {
				return managementPageResponse{}, false, errManagementCursor
			}
			page.Offset = max(0, ((page.Total-1)/managementPageSize)*managementPageSize)
		}
	}
	end := min(page.Total, page.Offset+managementPageSize)
	for _, r := range plan.Rows[page.Offset:end] {
		row := managementQueryRow{ID: r.ID, Account: subAccount{ID: r.Account}, Target: subTarget{GroupID: r.Group}}
		if len(r.Members) > 0 {
			row.Configured = &managementQueryChannel{}
			for _, m := range r.Members {
				row.Configured.Members = append(row.Configured.Members, managementChannel{subInstalledChannel: subInstalledChannel{AccountID: m.AccountID, GroupID: m.GroupID}})
			}
		}
		page.Rows = append(page.Rows, row)
	}
	encode := func(offset int) string {
		return encodeManagementCursor(s.control.key, owner, managementPageCursor{Version: 1, Offset: offset, Revision: page.Revision, Filter: tokenHash(mustJSON(filters))})
	}
	if end < page.Total {
		page.NextCursor = encode(end)
	}
	if page.Offset > 0 {
		page.PreviousCursor = encode(max(0, page.Offset-managementPageSize))
	}
	view := managementDatabaseView{Input: plan.Input, Prices: plan.Prices, ProjectedModels: true}
	directory := snapshot.directory
	ctx = context.WithValue(ctx, managementDirectoryScopeKey{}, directory)
	current := proof
	if !entry.Proof.Until.IsZero() {
		// Decoding the directory may cross a running check's deadline. The
		// original third proof checked the database clock after that work too.
		// Retain that guard without repeating all versions, headers and prices.
		var hydrationAt time.Time
		if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&hydrationAt); err != nil {
			return managementPageResponse{}, false, err
		}
		if !hydrationAt.Before(entry.Proof.Until) {
			log.Printf("management_paging_miss reason=deadline request_id=%s", consoleRequestID(ctx))
			return managementPageResponse{}, false, nil
		}
	}
	// This is the SAME second RR transaction that proved the plan current
	// after live gateway authorization. A resident source
	// directory with the exact model/ownership versions can supply its already
	// read scalar fields; an eviction/mismatch keeps hydration on its old SQL.
	directory.accountRevision = current.Epochs["console_sub_accounts"]
	if directory.ready && directory.accountRevision != "" {
		directory.directory = s.modelDirectories.get(managementDirectoryVersion{s.control.db, owner, directory.accountRevision, directory.modelRevision})
	}
	ctx = context.WithValue(ctx, managementReceiptSetScopeKey{}, managementReceiptSetScopeFromProof(&s.receiptSets, s.control.db, tx, owner, current))
	ctx = context.WithValue(ctx, managementPageModelScopeKey{}, &managementPageModelScope{store: &s.pageModelStreams, tx: tx, summaries: current.Epochs["console_sub_model_summaries"]})
	ctx = withManagementDisplayVersion(ctx, tx, owner, current.DisplayVersion)
	headers := map[string]subAccount{}
	for _, a := range current.Accounts {
		headers[a.ID] = a
	}
	var responseHeaders []subAccount
	var compactAccounts []subAccount
	scoped := managementListView(ctx) && entry.ListPlan != nil
	if scoped {
		positions := map[string]int{}
		for _, identity := range plan.Rows[page.Offset:end] {
			target, found := entry.ListPlan.data.site[identity.ID]
			fresh, owned := headers[identity.Account]
			// Legacy configured rows and site identities can collide. Retain
			// last-wins reconstruction for every ID in that namespace.
			if !found || !owned || identity.Account == "" || len(identity.Members) > 0 || strings.HasPrefix(identity.ID, "configured:") {
				scoped = false
				break
			}
			i, exists := positions[fresh.ID]
			if !exists {
				i = len(compactAccounts)
				positions[fresh.ID] = i
				fresh.Targets = nil
				compactAccounts = append(compactAccounts, fresh)
			}
			compactAccounts[i].Targets = append(compactAccounts[i].Targets, target)
		}
	}
	// The retained input is immutable. Hydration replaces target values, so
	// copy the slices on the general path, never the nested evidence.
	actual := view.Input.Accounts
	if entry.ListPlan != nil {
		actual = entry.ListPlan.data.headers
	}
	responseHeaders = make([]subAccount, len(actual))
	if !scoped {
		view.Input.Accounts = append([]subAccount{}, view.Input.Accounts...)
	}
	for i, a := range actual {
		fresh, ok := headers[a.ID]
		if !ok {
			return managementPageResponse{}, false, nil
		}
		responseHeaders[i] = fresh
		responseHeaders[i].Targets = []subTarget{}
		if entry.ListPlan != nil {
			responseHeaders[i].ActiveGroups, responseHeaders[i].CompletedGroups = a.ActiveGroups, a.CompletedGroups
		}
		if !scoped {
			fresh.Targets = append([]subTarget{}, view.Input.Accounts[i].Targets...)
			view.Input.Accounts[i] = fresh
		}
	}
	contextView := view
	if scoped {
		view.Input.Accounts = compactAccounts
		view.Input.Channels, view.Input.Imports, view.Input.Checks = nil, nil, nil
	}
	page.ReadAtMS = now.UnixMilli()
	if err = s.hydrateManagementPageFrom(ctx, tx, owner, &view, &page, filters); err != nil {
		return managementPageResponse{}, false, err
	}
	if managementListView(ctx) {
		managementListRows(&page, managementOverviewView(ctx))
	}
	if err = ctx.Err(); err != nil {
		return managementPageResponse{}, false, err
	}
	if entry.ListPlan == nil {
		responseHeaders = managementAccountHeaders(view.Input.Accounts)
	}
	response := managementPageResponse{Owner: owner, managementQueryPage: page, Accounts: responseHeaders, SelectionToken: encode(0), Context: managementContextForPage(contextView, page, filters.Model)}
	// Do not hold a database connection while encoding the retained response.
	_ = tx.Rollback()
	// Build the canonical representation once; use the same complete columns for
	// the response and retained page. rememberManagementColumnPage independently
	// clones the bytes so neither caller nor response can mutate retained state.
	current.Facts, current.CurrentExport = entry.Proof.Facts, entry.Proof.CurrentExport
	response.currentProof = &current
	// Both forms of page one have the same current public response. Normal
	// cursor reads already verified its revision; refresh reads rebase to now.
	cacheCursor := cursor
	if page.Offset == 0 {
		cacheCursor = ""
	}
	if rebase && cacheCursor != "" {
		cacheCursor += "\x00refresh"
	}
	response.preparedColumns = s.rememberManagementPage(managementPageComputedKey(ctx, owner, filters, cacheCursor), &current, response)
	return response, true, nil
}

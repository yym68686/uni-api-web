package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
)

// Keep the evaluated ordering/facets and group metadata, never all model
// payloads. The same bounded store accounts for these bytes and rendered pages.
// Every use proves this directory is CURRENT before hydrating only its page.
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

func managementPagingKey(owner string, filters managementQueryFilters) string {
	return managementComputedKey(owner, filters, "paging-directory")
}
func (s *Service) rememberManagementPagingPlan(owner string, filters managementQueryFilters, snapshot managementQuerySnapshot) {
	if snapshot.Proof == nil || snapshot.Proof.Stamp == "" || !snapshot.View.ProjectedModels {
		log.Printf("management_paging_store stored=false reason=unprepared projected=%t", snapshot.View.ProjectedModels)
		return
	}
	plan := managementPagingPlan{Input: snapshot.View.Input, Prices: snapshot.View.Prices, Page: snapshot.Page}
	plan.Page.Rows = nil
	plan.Input.Accounts = make([]subAccount, len(snapshot.View.Input.Accounts))
	for i, a := range snapshot.View.Input.Accounts {
		plan.Input.Accounts[i] = a
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
		return
	}
	raw, err := json.Marshal(plan)
	if err != nil || len(raw) > 8<<20 {
		log.Printf("management_paging_store stored=false reason=encoding_or_size bytes=%d", len(raw))
		return
	}
	s.rememberManagementEntry(managementPagingKey(owner, filters), snapshot.Proof, managementComputedPage{Plan: raw, Bytes: len(raw)})
	log.Printf("management_paging_store stored=true bytes=%d rows=%d", len(raw), len(plan.Rows))
}

func (s *Service) currentManagementPagingPage(ctx context.Context, owner string, filters managementQueryFilters, cursor string, rebase bool) (managementPageResponse, bool, error) {
	ctx = managementTraceContext(ctx)
	entry, exists := s.managementComputed.get(managementPagingKey(owner, filters))
	if !exists || len(entry.Plan) == 0 {
		log.Printf("management_paging_miss reason=absent request_id=%s", consoleRequestID(ctx))
		return managementPageResponse{}, false, nil
	}
	proof, _, valid, err := s.currentManagementProof(ctx, owner, entry.Proof)
	if err != nil || !valid {
		log.Printf("management_paging_miss reason=proof failed=%t request_id=%s", err != nil, consoleRequestID(ctx))
		return managementPageResponse{}, false, err
	}
	var plan managementPagingPlan
	decodeEnd := managementReadSpan(ctx, "paging_directory_decode")
	if err = json.Unmarshal(entry.Plan, &plan); err != nil {
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
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return managementPageResponse{}, false, err
	}
	defer tx.Rollback()
	directory := &managementDirectoryScope{store: &s.modelDirectories, database: s.control.db, tx: tx, owner: owner}
	ctx = context.WithValue(ctx, managementDirectoryScopeKey{}, directory)
	current, now, err := s.managementReadProof(ctx, tx, owner)
	if err != nil {
		return managementPageResponse{}, false, err
	}
	if managementProofStamp(current, entry.Proof.CurrentExport) != proof.Stamp || current.HeadersRevision != proof.HeadersRevision || (!entry.Proof.Until.IsZero() && !now.Before(entry.Proof.Until)) {
		log.Printf("management_paging_miss reason=concurrent_change request_id=%s", consoleRequestID(ctx))
		return managementPageResponse{}, false, nil
	}
	// This RR transaction just proved the plan current. A resident source
	// directory with the exact model/ownership versions can supply its already
	// read scalar fields; an eviction/mismatch keeps hydration on its old SQL.
	directory.accountRevision = current.Epochs["console_sub_accounts"]
	if directory.ready && directory.accountRevision != "" {
		directory.directory = s.modelDirectories.get(managementDirectoryVersion{s.control.db, owner, directory.accountRevision, directory.modelRevision})
	}
	ctx = context.WithValue(ctx, managementReceiptSetScopeKey{}, managementReceiptSetScopeFromProof(&s.receiptSets, s.control.db, tx, owner, current))
	headers := map[string]subAccount{}
	for _, a := range current.Accounts {
		headers[a.ID] = a
	}
	for i, a := range view.Input.Accounts {
		fresh, ok := headers[a.ID]
		if !ok {
			return managementPageResponse{}, false, nil
		}
		fresh.Targets = a.Targets
		view.Input.Accounts[i] = fresh
	}
	page.ReadAtMS = now.UnixMilli()
	if err = s.hydrateManagementPageFrom(ctx, tx, owner, &view, &page, filters); err != nil {
		return managementPageResponse{}, false, err
	}
	if err = ctx.Err(); err != nil {
		return managementPageResponse{}, false, err
	}
	response := managementPageResponse{Owner: owner, managementQueryPage: page, Accounts: managementAccountHeaders(view.Input.Accounts), SelectionToken: encode(0), Context: managementContextForPage(view, page, filters.Model)}
	// Do not hold a database connection while encoding the retained response.
	_ = tx.Rollback()
	// Build the canonical representation once; use the same complete columns for
	// the response and retained page. rememberManagementColumnPage independently
	// clones the bytes so neither caller nor response can mutate retained state.
	current.Facts, current.CurrentExport = entry.Proof.Facts, entry.Proof.CurrentExport
	cacheCursor := cursor
	if rebase {
		cacheCursor += "\x00refresh"
	}
	response.preparedColumns = s.rememberManagementPage(managementComputedKey(owner, filters, cacheCursor), &current, response)
	return response, true, nil
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
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
	for _, row := range filterManagementQueryRows(snapshot.Rows, filters, snapshot.Evidence).Rows {
		identity := managementPagingRow{ID: row.ID, Account: row.Account.ID, Group: row.Target.GroupID}
		if row.Configured != nil {
			for _, member := range row.Configured.Members {
				identity.Members = append(identity.Members, subBoundKey{AccountID: member.AccountID, GroupID: member.GroupID})
			}
		}
		plan.Rows = append(plan.Rows, identity)
	}
	if len(plan.Rows) != plan.Page.Total {
		return
	}
	raw, err := json.Marshal(plan)
	if err != nil || len(raw) > 8<<20 {
		return
	}
	s.rememberManagementEntry(managementPagingKey(owner, filters), snapshot.Proof, managementComputedPage{Plan: raw, Bytes: len(raw)})
}

func (s *Service) currentManagementPagingPage(ctx context.Context, owner string, filters managementQueryFilters, cursor string, rebase bool) (managementPageResponse, bool, error) {
	c := &s.managementComputed
	c.mu.Lock()
	entry, exists := c.pages[managementPagingKey(owner, filters)]
	c.mu.Unlock()
	if !exists || len(entry.Plan) == 0 {
		return managementPageResponse{}, false, nil
	}
	proof, _, valid, err := s.currentManagementProof(ctx, owner, entry.Proof)
	if err != nil || !valid {
		return managementPageResponse{}, false, err
	}
	var plan managementPagingPlan
	if err = json.Unmarshal(entry.Plan, &plan); err != nil {
		return managementPageResponse{}, false, err
	}
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
	current, now, err := s.managementReadProof(ctx, tx, owner)
	if err != nil {
		return managementPageResponse{}, false, err
	}
	if current.Stamp != proof.Stamp || current.HeadersRevision != proof.HeadersRevision || (!entry.Proof.Until.IsZero() && !now.Before(entry.Proof.Until)) {
		return managementPageResponse{}, false, nil
	}
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
	return managementPageResponse{Owner: owner, managementQueryPage: page, Accounts: managementAccountHeaders(view.Input.Accounts), SelectionToken: encode(0), Context: managementContextForPage(view, page, filters.Model)}, true, nil
}

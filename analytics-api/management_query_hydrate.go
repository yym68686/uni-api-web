package main

import (
	"context"
	"encoding/json"
	"sort"
)

// Batch the page's summary ranges per owned account before joining models.
// A point lookup for every model causes thousands of random heap reads on a
// large page. OFFSET 0 retains the account/range scan; NOT MATERIALIZED streams
// summary payloads rather than writing their complete CTE to a temporary file.
// The source row and legacy summary fallback remain in the same RR snapshot.
const managementPageModelsSQL = `WITH wanted AS MATERIALIZED (
 SELECT w.* FROM jsonb_to_recordset($2::jsonb) AS w(account_id text,group_id bigint)
 JOIN console_sub_accounts a ON a.id=w.account_id WHERE a.owner=$1),
 batches AS MATERIALIZED (SELECT account_id,array_agg(group_id) AS groups FROM wanted GROUP BY account_id),
 summaries AS NOT MATERIALIZED (
 SELECT ms.account_id,ms.group_id,ms.model,ms.result FROM batches b CROSS JOIN LATERAL (
 SELECT ms.account_id,ms.group_id,ms.model,ms.result FROM console_sub_model_summaries ms
 WHERE ms.account_id=b.account_id AND ms.group_id=ANY(b.groups) OFFSET 0) ms)
 SELECT m.account_id,m.group_id,m.model,m.state,m.message,COALESCE(ms.result,console_sub_model_summary_v1(m.result))
 FROM wanted JOIN console_sub_models m USING(account_id,group_id)
 LEFT JOIN summaries ms USING(account_id,group_id,model)`

// Predicates determine the complete result first. Only the page's groups and
// every bound member needed to assemble its public aliases then read display
// evidence, under the caller's SAME repeatable-read transaction.
func (s *Service) hydrateManagementPageFrom(ctx context.Context, db controlQueryDB, owner string, view *managementDatabaseView, page *managementQueryPage, filters managementQueryFilters) error {
	// Release request-only receipt evidence before the view can be retained or
	// returned. It must never outlive this hydration or cross a transaction.
	defer func() { view.receiptReuse = nil }()
	if !view.ProjectedModels || len(page.Rows) == 0 {
		return nil
	}
	groups := map[string]bool{}
	for _, row := range page.Rows {
		if row.Account.ID != "" {
			groups[qualityGroupID(row.Account.ID, row.Target.GroupID)] = true
		}
		if row.Configured != nil {
			for _, member := range row.Configured.Members {
				groups[qualityGroupID(member.AccountID, member.GroupID)] = true
			}
		}
	}
	subset := []subAccount{}
	wanted := []subBoundKey{}
	for _, account := range view.Input.Accounts {
		copy := account
		copy.Targets = []subTarget{}
		for _, target := range account.Targets {
			if groups[qualityGroupID(account.ID, target.GroupID)] {
				target.Models = []subModelResult{}
				copy.Targets = append(copy.Targets, target)
				wanted = append(wanted, subBoundKey{AccountID: account.ID, GroupID: target.GroupID})
			}
		}
		if len(copy.Targets) > 0 {
			subset = append(subset, copy)
		}
	}
	if len(wanted) > 0 {
		stages := newManagementReadTimeline(ctx, "hydrate_models")
		defer stages.finish()
		index := map[string]*subTarget{}
		for i := range subset {
			for j := range subset[i].Targets {
				target := &subset[i].Targets[j]
				index[qualityGroupID(subset[i].ID, target.GroupID)] = target
			}
		}
		err := readManagementPageModelStream(ctx, db, owner, wanted, func(row managementPageModelRecord, readTrace *managementRowsTrace) error {
			model := subModelResult{Model: row.model, State: row.state, Message: row.message}
			decodeStart := readTrace.decodeStart()
			var err error
			if len(row.result) > 0 {
				err = json.Unmarshal(row.result, &model.Result)
			}
			readTrace.decoded(decodeStart, len(row.result))
			if err != nil {
				return err
			}
			compactSubModelResult(model.Result)
			if target := index[qualityGroupID(row.account, row.group)]; target != nil {
				target.Models = append(target.Models, model)
			}
			return nil
		})
		if err != nil {
			return err
		}
		// Sorting the complete JSON rows in PostgreSQL spills page evidence to
		// disk. Only each target's model order is observable; preserve its UTF-8
		// COLLATE "C" order with the same byte ordering in Go after streaming.
		for _, target := range index {
			sort.Slice(target.Models, func(i, j int) bool {
				return target.Models[i].Model < target.Models[j].Model
			})
		}
		stages.next("hydrate_queue")
		if err = s.subAttachQueuedChecksFrom(ctx, db, owner, subset); err != nil {
			return err
		}
		stages.next("hydrate_receipts")
		if err = s.attachManagementPageSummaryUsageFrom(ctx, db, owner, subset, view.receiptReuse); err != nil {
			return err
		}
		stages.next("hydrate_normalize")
		if err = normalizeManagementAccounts(subset); err != nil {
			return err
		}
		for i := range view.Input.Accounts {
			for j := range view.Input.Accounts[i].Targets {
				target := &view.Input.Accounts[i].Targets[j]
				if full := index[qualityGroupID(view.Input.Accounts[i].ID, target.GroupID)]; full != nil {
					*target = *full
				}
			}
		}
	}
	fullRows, err := buildHydratedManagementRows(view.Input, groups, filters.Model, filters.AccountID)
	if err != nil {
		return err
	}
	byID := make(map[string]managementQueryRow, len(fullRows))
	for _, row := range fullRows {
		byID[row.ID] = row
	}
	for i, row := range page.Rows {
		full, found := byID[row.ID]
		if !found {
			return errManagementRevision
		}
		page.Rows[i] = full
	}
	return ctx.Err()
}

// Filtering, revision and selection have already used the complete model
// catalog. This final pass only replaces page rows. Do not reconstruct every
// off-page model check again. Retain every configured binding, including those
// outside the page: legacy rows can share an ID across different engines and
// the existing replacement map deliberately keeps its last row. Keep all target
// headers, capabilities and configured checks so binding selection and off-page
// decode errors remain identical. The original complete view is not modified.
func buildHydratedManagementRows(in managementQueryInput, groups map[string]bool, model, account string) ([]managementQueryRow, error) {
	bound := make(map[string]bool, len(in.Channels))
	for _, channel := range in.Channels {
		bound[qualityGroupID(channel.AccountID, channel.GroupID)] = true
	}
	in.Accounts = append([]subAccount(nil), in.Accounts...)
	for i := range in.Accounts {
		a := &in.Accounts[i]
		a.Targets = append([]subTarget(nil), a.Targets...)
		for j := range a.Targets {
			t := &a.Targets[j]
			id := qualityGroupID(a.ID, t.GroupID)
			if !groups[id] && !bound[id] {
				t.Models = nil
			}
		}
	}
	return buildManagementQueryRows(in, model, account)
}

package main

import (
	"context"
	"slices"
	"sort"
)

// Keep both scans within the same owned group. Joining two independent
// account-sized batches lets a poor estimate rescan all groups for each model;
// here the only join is between one group's indexed model/summary ranges.
const managementPageModelsSQL = `WITH wanted AS MATERIALIZED (
 SELECT w.* FROM jsonb_to_recordset($2::jsonb) AS w(account_id text,group_id bigint)
 JOIN console_sub_accounts a ON a.id=w.account_id WHERE a.owner=$1)
 SELECT w.account_id,w.group_id,g.model,g.state,g.message,g.result FROM wanted w
 CROSS JOIN LATERAL (
 SELECT m.model,m.state,m.message,COALESCE(ms.result,console_sub_model_summary_v1(m.result)) AS result
 FROM (SELECT model,state,message,result FROM console_sub_models
       WHERE account_id=w.account_id AND group_id=w.group_id OFFSET 0) m
 LEFT JOIN (SELECT model,result FROM console_sub_model_summaries
            WHERE account_id=w.account_id AND group_id=w.group_id OFFSET 0) ms USING(model)
 OFFSET 0) g`

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
	fullGroups := map[string]bool{}
	var projectedGroups map[string]bool
	overview := managementOverviewView(ctx) && filters.Model == ""
	for _, row := range page.Rows {
		if row.Account.ID != "" {
			id := qualityGroupID(row.Account.ID, row.Target.GroupID)
			groups[id] = true
			if overview && row.Configured != nil {
				fullGroups[id] = true
			}
		}
		if row.Configured != nil {
			for _, member := range row.Configured.Members {
				id := qualityGroupID(member.AccountID, member.GroupID)
				groups[id], fullGroups[id] = true, true
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
		activeSync := map[string]bool{}
		for i := range subset {
			a := &subset[i]
			activeSync[a.ID] = a.JobKind == "sync" && managementPending(a.State)
			for j := range subset[i].Targets {
				target := &subset[i].Targets[j]
				index[qualityGroupID(subset[i].ID, target.GroupID)] = target
			}
		}
		// Preserve negotiated single-model omissions and diagnostic replays.
		// For every full-summary page (not just page 1), read only its
		// already-resolved groups. Unchanged exact versions need no receipt
		// lookups for those models. Account metadata and queues remain live.
		if !managementListView(ctx) && !managementSelectedReceipts(ctx) && !managementSelectedModels(ctx) && len(wanted) <= 128 &&
			ctx.Value(managementReceiptPlanKey{}) == nil && ctx.Value(managementModelReadKey{}) == nil {
			cached, err := readManagementDisplayGroups(ctx, db, owner, wanted)
			if err != nil {
				return err
			}
			missing := wanted[:0]
			for _, g := range wanted {
				id := qualityGroupID(g.AccountID, g.GroupID)
				if models, found := cached[id]; found {
					index[id].Models = models
				} else {
					missing = append(missing, g)
				}
			}
			wanted = missing
		}
		consume := func(row managementPageModelRecord, readTrace *managementRowsTrace) error {
			decodeStart := readTrace.decodeStart()
			err := appendManagementDisplayModel(index, row)
			readTrace.decoded(decodeStart, len(row.result))
			return err
		}
		// Complete previews may contain hundreds of groups. Keep each model
		// read at display-page scale: expanding the entire directory can exceed
		// its bounded SQL-input budget and force an expensive all-model join.
		// Reuse this exact RR transaction for EVERY batch; do not truncate rows,
		// open new snapshots, or weaken configured/custom-model full fallbacks.
		const modelBatchGroups = 25
		projectedGroups = map[string]bool{}
		overviews := map[string]managementPageGroupOverview{}
		for start := 0; start < len(wanted); start += modelBatchGroups {
			batch := wanted[start:min(start+modelBatchGroups, len(wanted))]
			if overview {
				scoped := make([]subBoundKey, 0, len(batch))
				for _, g := range batch {
					if !activeSync[g.AccountID] && !fullGroups[qualityGroupID(g.AccountID, g.GroupID)] {
						scoped = append(scoped, g)
					}
				}
				if len(scoped) > 0 {
					summaries, err := readManagementPageOverview(ctx, db, owner, scoped)
					if err != nil {
						return err
					}
					rich := make([]subBoundKey, 0, len(batch)-len(summaries))
					for _, g := range batch {
						id := qualityGroupID(g.AccountID, g.GroupID)
						if summary, ok := summaries[id]; ok {
							overviews[id] = summary
						} else {
							rich = append(rich, g)
						}
					}
					batch = rich
				}
			}
			if len(batch) == 0 {
				continue
			}
			projected, err := readManagementDisplayModelStream(ctx, db, owner, batch, fullGroups, filters.Model, consume)
			if err != nil {
				return err
			}
			for id := range projected {
				projectedGroups[id] = true
			}
		}
		// Sorting the complete JSON rows in PostgreSQL spills page evidence to
		// disk. Only each target's model order is observable; preserve its UTF-8
		// COLLATE "C" order with the same byte ordering in Go after streaming.
		for _, target := range index {
			sort.Slice(target.Models, func(i, j int) bool {
				return target.Models[i].Model < target.Models[j].Model
			})
		}
		// Paging directories carry identities rather than checks. Determine the
		// custom-model fallback only after reading the page's actual model list.
		// Built-ins always have a check, even when it is untested.
		if filters.Model != "" && !slices.Contains(subModels, filters.Model) {
			for id, target := range index {
				if !slices.ContainsFunc(target.Models, func(check subModelResult) bool { return check.Model == filters.Model }) {
					fullGroups[id] = true
				}
			}
		}
		stages.next("hydrate_queue")
		if err := s.subAttachQueuedChecksFrom(ctx, db, owner, subset); err != nil {
			return err
		}
		if !managementListView(ctx) {
			stages.next("hydrate_receipts")
			usageAccounts := subset
			if managementSelectedReceipts(ctx) && filters.Model != "" {
				usageAccounts = managementPageUsageAccounts(subset, filters.Model, fullGroups)
			}
			if err := s.attachManagementPageSummaryUsageFrom(ctx, db, owner, usageAccounts, view.receiptReuse); err != nil {
				return err
			}
		}
		stages.next("hydrate_normalize")
		if err := normalizeManagementAccounts(subset); err != nil {
			return err
		}
		for id, summary := range overviews {
			target := index[id]
			if summary.legacy && target.Result != nil {
				summary.counts.Untested--
				summary.counts.add(subModelResult{State: "done", Result: target.Result})
			}
			target.ListOverview, target.Models = &summary.counts, summary.busy
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
		if managementSelectedReceipts(ctx) && filters.Model != "" && full.Configured == nil && !fullGroups[qualityGroupID(full.Account.ID, full.Target.GroupID)] {
			page.Rows[i].Target.ReceiptModel = filters.Model
		}
		if projectedGroups[qualityGroupID(full.Account.ID, full.Target.GroupID)] && full.Configured == nil {
			page.Rows[i].Target.SummaryModel = filters.Model
		}
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

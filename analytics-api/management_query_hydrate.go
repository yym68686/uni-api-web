package main

import (
	"context"
	"encoding/json"
)

// Predicates determine the complete result first. Only the page's groups and
// every bound member needed to assemble its public aliases then read display
// evidence, under the caller's SAME repeatable-read transaction.
func (s *Service) hydrateManagementPageFrom(ctx context.Context, db controlQueryDB, owner string, view *managementDatabaseView, page *managementQueryPage, filters managementQueryFilters) error {
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
		index := map[string]*subTarget{}
		for i := range subset {
			for j := range subset[i].Targets {
				target := &subset[i].Targets[j]
				index[qualityGroupID(subset[i].ID, target.GroupID)] = target
			}
		}
		raw, err := json.Marshal(wanted)
		if err != nil {
			return err
		}
		rows, err := db.QueryContext(ctx, `SELECT m.account_id,m.group_id,m.model,m.state,m.message,COALESCE(ms.result,console_sub_model_summary_v1(m.result))
 FROM jsonb_to_recordset($2::jsonb) AS wanted(account_id text,group_id bigint)
 JOIN console_sub_models m USING(account_id,group_id)
 JOIN console_sub_accounts a ON a.id=m.account_id
 LEFT JOIN console_sub_model_summaries ms USING(account_id,group_id,model)
 WHERE a.owner=$1 ORDER BY m.account_id COLLATE "C",m.group_id,m.model COLLATE "C"`, owner, string(raw))
		if err != nil {
			return err
		}
		for rows.Next() {
			var account string
			var group int64
			var model subModelResult
			var raw []byte
			if err = rows.Scan(&account, &group, &model.Model, &model.State, &model.Message, &raw); err != nil {
				break
			}
			if len(raw) > 0 {
				if err = json.Unmarshal(raw, &model.Result); err != nil {
					break
				}
			}
			compactSubModelResult(model.Result)
			if target := index[qualityGroupID(account, group)]; target != nil {
				target.Models = append(target.Models, model)
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if err = s.subAttachQueuedChecksFrom(ctx, db, owner, subset); err != nil {
			return err
		}
		if err = s.subAttachUsageFrom(ctx, db, owner, subset); err != nil {
			return err
		}
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
	fullRows, err := buildManagementQueryRows(view.Input, filters.Model, filters.AccountID)
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

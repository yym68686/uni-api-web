package main

import (
	"context"
	"encoding/json"
)

// The catalog still contributes every name, including untested models. For
// success/error predicates without a named model the ordinary site predicate
// is existential; send one result per group, not one signal per model.
// Configured aliases are different: competing members' pending/latest checks
// must be chosen BEFORE testing their result. Attach only the candidate models
// named by the live mappings and let the existing builder make that choice.
func attachManagementAvailability(ctx context.Context, db controlQueryDB, owner string, in *managementQueryInput, status string) (map[string]bool, error) {
	defer managementReadSpan(ctx, "database_availability_groups")()
	matches := make(map[string]bool)
	rows, err := db.QueryContext(ctx, `SELECT p.account_id,p.group_id FROM console_management_model_signal p
 JOIN console_sub_accounts a ON a.id=p.account_id AND a.owner=$1
 WHERE p.present AND p.availability=$2 GROUP BY p.account_id,p.group_id`, owner, status)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var g subBoundKey
		if err := rows.Scan(&g.AccountID, &g.GroupID); err != nil {
			rows.Close()
			return nil, err
		}
		matches[qualityGroupID(g.AccountID, g.GroupID)] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]map[string]bool)
	for _, member := range in.Channels {
		if member.AccountID == "" {
			continue
		}
		id := qualityGroupID(member.AccountID, member.GroupID)
		names := wanted[id]
		if names == nil {
			names = make(map[string]bool)
			wanted[id] = names
		}
		for _, name := range member.Models {
			names[managementUpstream(member, name)] = true
		}
	}
	type aliasKey struct {
		Account string `json:"account_id"`
		Group   int64  `json:"group_id"`
		Model   string `json:"model"`
	}
	var keys []aliasKey
	positions := make(map[aliasKey]*subModelResult)
	for i := range in.Accounts {
		a := &in.Accounts[i]
		for j := range a.Targets {
			t := &a.Targets[j]
			id := qualityGroupID(a.ID, t.GroupID)
			names := wanted[id]
			astra := false
			for k := range t.Models {
				model := &t.Models[k]
				astra = astra || model.Model == "gpt-6-astra"
				if names[model.Model] {
					key := aliasKey{a.ID, t.GroupID, model.Model}
					keys = append(keys, key)
					positions[key] = model
				}
			}
			if !astra && t.Result != nil && t.Result.Availability.Status == status {
				// managementModelChecks synthesizes this result only when the
				// model is a built-in and is missing (queued models count).
				for _, name := range subModels {
					if name == "gpt-6-astra" {
						matches[id] = true
						break
					}
				}
			}
		}
	}
	if len(keys) == 0 {
		return matches, ctx.Err()
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	rows, err = db.QueryContext(ctx, `SELECT w.account_id,w.group_id,w.model,s.checked_at,s.availability
 FROM jsonb_to_recordset($2::jsonb) AS w(account_id text,group_id bigint,model text)
 JOIN console_sub_accounts a ON a.id=w.account_id AND a.owner=$1
 CROSS JOIN LATERAL (
   SELECT checked_at,availability FROM console_management_model_signal p
   WHERE p.account_id=w.account_id AND p.group_id=w.group_id AND p.model=w.model AND p.present OFFSET 0
 ) s`, owner, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key aliasKey
		var result subResult
		if err := rows.Scan(&key.Account, &key.Group, &key.Model, &result.CheckedAt, &result.Availability.Status); err != nil {
			return nil, err
		}
		if model := positions[key]; model != nil {
			model.Result = &result
		}
	}
	return matches, rows.Err()
}

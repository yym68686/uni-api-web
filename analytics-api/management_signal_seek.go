package main

import "slices"

// A filtered compact read already knows the exact source identities under its
// current ownership/model version. Seek only a small set through the existing
// PK, instead of scanning every projection row for model=ANY. Authorize that
// set once before the parameterized probes; do not scan accounts per model.
const managementModelSeekSQL = `WITH owned AS MATERIALIZED (
 SELECT wanted.* FROM jsonb_to_recordset($2::jsonb) AS wanted(account_id text,group_id bigint,model text)
 JOIN console_sub_accounts a ON a.id=wanted.account_id WHERE a.owner=$1)
 ` + managementModelStreamSelectSQL + `
 FROM owned wanted
 CROSS JOIN LATERAL (SELECT signal.* FROM console_management_model_signal signal
 WHERE signal.account_id=wanted.account_id AND signal.group_id=wanted.group_id AND signal.model=wanted.model OFFSET 0) p`

type managementModelSeekKey struct {
	Account string `json:"account_id"`
	Group   int64  `json:"group_id"`
	Model   string `json:"model"`
}

// Bound JSON/probe work and retain the scan for broad predicates. Only the
// built-in scoped position index is eligible; nil means a legacy/full read.
// The source FK plus the existing missing-evidence check keep projection
// completeness/error behavior: a missing required row still forces fallback.
func managementModelSeekKeys(models []managementStreamModel, positions managementStreamPositionIndex) ([]managementModelSeekKey, bool) {
	if positions.only == nil || positions.values == nil || len(positions.values) > 4096 || len(positions.values) > len(models)/8 {
		return nil, false
	}
	order := make([]int, 0, len(positions.values))
	bytes := 0
	for key, position := range positions.values {
		if position < 0 || position >= len(models) || !positions.only[key.model] {
			return nil, false
		}
		item := models[position].item
		if item.Account != key.account || item.Group != key.group || item.Model.Model != key.model {
			return nil, false
		}
		// A shared long dictionary string could otherwise expand thousands of
		// times in JSON. Include worst-case escaping and fixed field overhead.
		if len(key.account) > (1<<20)/6 || len(key.model) > (1<<20)/6 {
			return nil, false
		}
		bytes += 6*(len(key.account)+len(key.model)) + 80
		if bytes > 1<<20 {
			return nil, false
		}
		order = append(order, position)
	}
	slices.Sort(order)
	keys := make([]managementModelSeekKey, 0, len(order))
	for _, position := range order {
		item := models[position].item
		keys = append(keys, managementModelSeekKey{item.Account, item.Group, item.Model.Model})
	}
	return keys, true
}

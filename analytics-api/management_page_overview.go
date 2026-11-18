package main

import (
	"context"
	"encoding/json"
)

// The existing transactionally maintained signals contain the whole-model
// validity flag plus the scalars required for the operations table. Correlate
// both sides to the group so PostgreSQL can use both primary-key prefixes
// instead of doing a separate random signal lookup for every model.
const managementPageOverviewSQL = `WITH wanted AS MATERIALIZED (
 SELECT w.account_id,w.group_id FROM jsonb_to_recordset($2::jsonb) AS w(account_id text,group_id bigint)
 JOIN console_sub_accounts a ON a.id=w.account_id WHERE a.owner=$1)
 SELECT w.account_id,w.group_id,g.model,g.state,g.valid,g.present,g.checked_at,g.availability,g.terminal
 FROM wanted w
 CROSS JOIN LATERAL (
 SELECT m.model,m.state,s.valid,s.present,s.checked_at,s.availability,s.terminal
 FROM (SELECT model,state FROM console_sub_models
       WHERE account_id=w.account_id AND group_id=w.group_id OFFSET 0) m
 LEFT JOIN (SELECT model,valid,present,checked_at,availability,terminal FROM console_management_model_signal
            WHERE account_id=w.account_id AND group_id=w.group_id OFFSET 0) s USING(model)
 OFFSET 0) g`

func readManagementPageOverview(ctx context.Context, db controlQueryDB, owner string, wanted []subBoundKey, slots map[string]*subTarget) (bool, error) {
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT complete FROM console_management_projection_state WHERE singleton`).Scan(&complete); err != nil || !complete {
		return false, err
	}
	raw, err := json.Marshal(wanted)
	if err != nil {
		return false, err
	}
	trace := newManagementRowsTraceDerivedDecode(ctx, "page_overview_signals")
	defer trace.finish()
	rows, err := db.QueryContext(ctx, managementPageOverviewSQL, owner, string(raw))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	trace.queryReady()
	models := make(map[string][]subModelResult, len(wanted))
	for trace.next(rows) {
		var account string
		var group int64
		var m subModelResult
		var valid, present *bool
		var at *int64
		var status, terminal *string
		if err := rows.Scan(&account, &group, &m.Model, &m.State, &valid, &present, &at, &status, &terminal); err != nil {
			return false, err
		}
		trace.counted(0)
		if valid == nil || !*valid || present == nil || at == nil || status == nil || terminal == nil {
			return false, nil // Preserve the authoritative full-summary fallback.
		}
		if *present {
			m.Result = &subResult{CheckedAt: *at, Availability: subProbe{Status: *status, TerminalStatus: *terminal}}
		}
		key := qualityGroupID(account, group)
		models[key] = append(models[key], m)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for key, list := range models {
		if slot := slots[key]; slot != nil {
			slot.Models = list
		}
	}
	return true, ctx.Err()
}

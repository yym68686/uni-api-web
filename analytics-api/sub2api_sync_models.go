package main

import (
	"context"
	"database/sql"
)

// One source statement keeps row-summary writes ahead of statement revision
// counters. Repeating one-row upserts in a transaction held the counters from
// the first model while trying to acquire the summary epoch for later models;
// concurrent result writers acquire those same locks in the opposite order.
func queueSubSyncModels(ctx context.Context, tx *sql.Tx, account string, groups []subRemoteGroup, discovered map[int64][]string) error {
	type entry struct {
		Group int64  `json:"group_id"`
		Model string `json:"model"`
		State string `json:"state"`
	}
	entries := []entry{}
	for _, group := range groups {
		seen := map[string]bool{}
		candidates := append(append(append([]string{}, subModels...), discovered[group.ID]...), subSyncScopeModels(ctx)...)
		for _, model := range candidates {
			if seen[model] {
				continue
			}
			seen[model] = true
			state := "idle"
			if subSyncIncludesModel(ctx, model) {
				state = "queued"
			}
			entries = append(entries, entry{group.ID, model, state})
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO console_sub_models(account_id,group_id,model,state)
 SELECT $1,x.group_id,x.model,x.state FROM jsonb_to_recordset($2::jsonb) AS x(group_id bigint,model text,state text)
 ORDER BY x.group_id,x.model
 ON CONFLICT(account_id,group_id,model) DO UPDATE SET state='queued',message='' WHERE EXCLUDED.state='queued'`, account, mustJSON(entries))
	return err
}

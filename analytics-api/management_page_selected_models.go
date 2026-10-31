package main

import (
	"context"
	"encoding/json"
	"log"
	"slices"
)

// An explicitly negotiated single-model display needs complete summary payloads
// only for its selected model and shared Astra quality. Model names/state/message
// still come from the SAME transaction's complete, owned source directory. The
// client must read the complete group before displaying other model results.
// Configured aliases and legacy/unprepared directories retain the full reader.
const managementPageSelectedModelsSQL = `WITH wanted AS MATERIALIZED (
 SELECT w.* FROM jsonb_to_recordset($2::jsonb) AS w(account_id text,group_id bigint,model text,state text,message text)
 JOIN console_sub_accounts a ON a.id=w.account_id WHERE a.owner=$1)
 SELECT w.account_id,w.group_id,w.model,w.state,w.message,COALESCE(ms.result,console_sub_model_summary_v1(raw.result))
 FROM wanted w
 LEFT JOIN LATERAL (SELECT result FROM console_sub_model_summaries ms
 WHERE ms.account_id=w.account_id AND ms.group_id=w.group_id AND ms.model=w.model OFFSET 0) ms ON true
 LEFT JOIN LATERAL (SELECT result FROM console_sub_models source
 WHERE ms.result IS NULL AND source.account_id=w.account_id AND source.group_id=w.group_id AND source.model=w.model OFFSET 0) raw ON true`

func readManagementDisplayModelStream(ctx context.Context, db controlQueryDB, owner string, wanted []subBoundKey, fullGroups map[string]bool, model string, consume managementPageModelConsumer) (map[string]bool, error) {
	fullRead := func() (map[string]bool, error) {
		return nil, readManagementPageModelStream(ctx, db, owner, wanted, consume)
	}
	if !managementSelectedModels(ctx) || !managementSelectedReceipts(ctx) || !slices.Contains(subModels, model) {
		return fullRead()
	}
	metadata, ok := managementPageDirectoryModels(ctx, db, owner, wanted)
	if !ok {
		return fullRead()
	}
	projected := map[string]bool{}
	full := []subBoundKey{}
	for _, group := range wanted {
		id := qualityGroupID(group.AccountID, group.GroupID)
		if fullGroups[id] {
			full = append(full, group)
		} else {
			projected[id] = true
		}
	}
	if len(projected) == 0 {
		return fullRead()
	}
	selected := []managementPageDirectoryModel{}
	metadataTrace := newManagementRowsTrace(ctx, "page_model_metadata")
	defer func() { metadataTrace.finish() }()
	for _, row := range metadata {
		if !projected[qualityGroupID(row.Account, row.Group)] {
			continue
		}
		if row.Model == model || row.Model == checkModel {
			selected = append(selected, row)
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := consume(managementPageModelRecord{account: row.Account, group: row.Group, model: row.Model, state: row.State, message: row.Message}, metadataTrace); err != nil {
			return nil, err
		}
	}
	metadataTrace.finish()
	metadataTrace = nil
	encoded, err := json.Marshal(selected)
	if err != nil {
		return nil, err
	}
	trace := newManagementRowsTrace(ctx, "page_selected_models")
	defer func() { trace.finish() }()
	rows, err := db.QueryContext(ctx, managementPageSelectedModelsSQL, owner, string(encoded))
	if err != nil {
		return nil, err
	}
	trace.queryReady()
	for trace.next(rows) {
		var row managementPageModelRecord
		if err = rows.Scan(&row.account, &row.group, &row.model, &row.state, &row.message, &row.result); err == nil {
			err = consume(row, trace)
		}
		if err != nil {
			rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	rows.Close()
	trace.finish()
	trace = nil
	if err != nil {
		return nil, err
	}
	if len(full) > 0 {
		if err := readManagementPageModelStream(ctx, db, owner, full, consume); err != nil {
			return nil, err
		}
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_selected_models groups=%d full_groups=%d summary_rows=%d directory_rows=%d request_id=%s", len(projected), len(full), len(selected), len(metadata), consoleRequestID(ctx))
	}
	return projected, ctx.Err()
}

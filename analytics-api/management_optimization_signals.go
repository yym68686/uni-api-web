package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"log"
)

// Match the full typed reader's summary source, but only by the primary keys
// selected from the current, owned model directory.
const managementTypedPreviewSQL = `WITH wanted AS MATERIALIZED (
 SELECT w.* FROM jsonb_to_recordset($2::jsonb) AS w(account_id text,group_id bigint,model text)
 JOIN console_sub_accounts a ON a.id=w.account_id WHERE a.owner=$1)
 SELECT w.account_id,w.group_id,w.model,COALESCE(ms.result,console_sub_model_summary_v1(m.result))
 FROM wanted w
 LEFT JOIN LATERAL (SELECT result FROM console_sub_model_summaries
 WHERE account_id=w.account_id AND group_id=w.group_id AND model=w.model OFFSET 0) ms ON true
 LEFT JOIN LATERAL (SELECT result FROM console_sub_models
 WHERE ms.result IS NULL AND account_id=w.account_id AND group_id=w.group_id AND model=w.model OFFSET 0) m ON true`

// Preserve model names/states for the whole directory, but decode evidence
// only for this preview's model, aliases, Astra, and custom-model fallback
// groups. All reads remain in the caller's owned repeatable-read transaction.
func readManagementTypedPreviewSignals(ctx context.Context, db controlQueryDB, owner, model string, names []string) ([]managementModelSignals, string, error) {
	defer managementReadSpan(ctx, "database_typed_preview")()
	models, err := readManagementModelSourceDirectory(ctx, db, owner)
	if err != nil {
		return nil, "", err
	}
	wanted, fallback := map[string]bool{}, map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	for _, group := range managementStreamFallbackGroups(model, models) {
		fallback[qualityGroupID(group.AccountID, group.GroupID)] = true
	}
	const batchSize = 256
	batch := make(map[managementModelSeekKey]*managementStreamModel, batchSize)
	keys := make([]managementModelSeekKey, 0, batchSize)
	read := func() error {
		if len(keys) == 0 {
			return nil
		}
		if err := readManagementTypedPreviewBatch(ctx, db, owner, keys, batch); err != nil {
			return err
		}
		clear(batch)
		keys = keys[:0]
		return nil
	}
	selected := 0
	for i := range models {
		row := &models[i]
		item := &row.item
		if !wanted[item.Model.Model] && (len(fallback) == 0 || !fallback[qualityGroupID(item.Account, item.Group)]) {
			continue
		}
		key := managementModelSeekKey{item.Account, item.Group, item.Model.Model}
		keys = append(keys, key)
		batch[key] = row
		selected++
		if len(keys) == batchSize {
			if err := read(); err != nil {
				return nil, "", err
			}
		}
	}
	if err := read(); err != nil {
		return nil, "", err
	}

	// This revision identifies a read-only preview, not a selection token.
	// Include the transactional receipt version without reading unrelated
	// receipt bodies. The selected receipts themselves are attached by the
	// typed account/summary reader before filtering.
	hash := sha256.New()
	var receiptsVersion int64
	if err := db.QueryRowContext(ctx, `SELECT revision FROM console_management_revision_state WHERE relation_name='console_sub_usage'`).Scan(&receiptsVersion); err != nil {
		return nil, "", err
	}
	if err := json.NewEncoder(hash).Encode([]any{"management-typed-preview-v1", receiptsVersion}); err != nil {
		return nil, "", err
	}
	order := managementDirectoryStreamOrder(ctx, db, owner, models)
	writer := newManagementRevisionWriter(hash)
	out := make([]managementModelSignals, 0, len(models))
	for i := range models {
		if i%1024 == 0 && ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		row := &models[i]
		if order != nil {
			row = order[i]
		}
		item := row.item
		item.Model.State = siteDetectionOutcome(item.Model.State, row.message)
		item.Model.Message = row.message
		writer.write(item.Account, item.Group, item.Model.Model, item.Model.State, row.message, row.evidence, nil, nil)
		out = append(out, item)
	}
	revision, err := finishManagementModelRevision(ctx, db, owner, hash)
	if managementTraceEnabled(ctx) {
		log.Printf("management_typed_preview directory_models=%d evidence_models=%d request_id=%s", len(models), selected, consoleRequestID(ctx))
	}
	return out, revision, err
}

func readManagementTypedPreviewBatch(ctx context.Context, db controlQueryDB, owner string, keys []managementModelSeekKey, batch map[managementModelSeekKey]*managementStreamModel) error {
	encoded, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	trace := newManagementRowsTrace(ctx, "preview_typed_predicates")
	defer trace.finish()
	rows, err := db.QueryContext(ctx, managementTypedPreviewSQL, owner, string(encoded))
	if err != nil {
		return err
	}
	defer rows.Close()
	trace.queryReady()
	for trace.next(rows) {
		start := trace.decodeStart()
		var key managementModelSeekKey
		var raw []byte
		if err := rows.Scan(&key.Account, &key.Group, &key.Model, &raw); err != nil {
			return err
		}
		row := batch[key]
		if row == nil || row.evidence != nil {
			return errManagementRevision
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &row.item.Model.Result); err != nil {
				return err
			}
			compactSubModelResult(row.item.Model.Result)
		}
		digest := sha256.Sum256(raw)
		row.evidence = digest[:]
		trace.decoded(start, len(raw))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, row := range batch {
		if row.evidence == nil {
			return errManagementRevision
		}
	}
	return ctx.Err()
}

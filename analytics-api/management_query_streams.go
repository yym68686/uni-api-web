package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"log"
	"slices"
)

// Keep model and receipt streams narrow. Joining both receipt roles in SQL
// duplicates the large receipt scan and sorts the expanded rows on disk. The
// caller's repeatable-read transaction permits one request-local ID index to
// resolve both roles without changing the snapshot or retaining a cache.
const managementModelSourceStreamSQL = `SELECT m.account_id,m.group_id,m.model,m.state,m.message
 FROM console_sub_models m JOIN console_sub_accounts a ON a.id=m.account_id WHERE a.owner=$1`

const managementModelStreamSQL = `SELECT p.account_id,p.group_id,p.model,p.valid,p.present,p.evidence_digest,
 p.checked_at,p.verdict,p.availability,p.terminal,p.model_match,p.probe_id,p.quality_probe_id,p.has_usage,p.usage_status,
 p.input_price,p.output_price,p.input_tokens,p.output_tokens
 FROM console_management_model_signal p JOIN console_sub_accounts a ON a.id=p.account_id WHERE a.owner=$1`

const managementReceiptSelectedStreamSQL = managementReceiptStreamSQL + ` AND r.id=ANY($2::text[])`

const managementReceiptStreamSQL = `SELECT r.id,r.valid,r.evidence_digest,r.status,r.input_price,r.output_price,r.input_tokens,r.output_tokens
 FROM console_management_receipt_signal r JOIN console_sub_accounts a ON a.id=r.account_id
 WHERE a.owner=$1`

type managementStreamModel struct {
	item                    managementModelSignals
	message, probe, quality string
	evidence                []byte
}

type managementStreamReceipt struct {
	valid    bool
	evidence []byte
	usage    managementStreamUsage
}

type managementStreamUsage struct {
	Status                    string
	InputPrice, OutputPrice   *float64
	InputTokens, OutputTokens *int64
}

func (u managementStreamUsage) expanded() *subUsage {
	return &subUsage{Status: u.Status, InputPrice: u.InputPrice, OutputPrice: u.OutputPrice, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
}

type managementStreamScope struct {
	CatalogOnly     bool
	Model           string
	Names           []string
	CompactRevision bool
	ReceiptReuse    *managementReceiptReuse
}

type managementStreamIdentity struct {
	account string
	group   int64
	model   string
}

type managementStreamPositionIndex struct {
	values map[managementStreamIdentity]int
	only   map[string]bool // nil keeps every model; a non-nil empty set keeps none.
}

func (p managementStreamPositionIndex) add(item managementModelSignals, position int) {
	if p.values != nil && (p.only == nil || p.only[item.Model.Model]) {
		p.values[managementStreamIdentity{item.Account, item.Group, item.Model.Model}] = position
	}
}

func readManagementSignalStreams(ctx context.Context, db controlQueryDB, owner string, strict bool) ([]managementModelSignals, string, error) {
	return readManagementScopedStreams(ctx, db, owner, strict, managementStreamScope{})
}

func readManagementScopedStreams(ctx context.Context, db controlQueryDB, owner string, strict bool, scope managementStreamScope) ([]managementModelSignals, string, error) {
	// Public helpers are also used with a pool in integration tests. Never allow
	// their streams to observe different committed generations either.
	if pool, ok := db.(*sql.DB); ok {
		tx, err := pool.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return nil, "", err
		}
		defer tx.Rollback()
		return readManagementScopedStreams(ctx, tx, owner, strict, scope)
	}
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT complete FROM console_management_projection_state WHERE singleton`).Scan(&complete); err != nil {
		return nil, "", err
	}
	if !complete {
		return nil, "", errManagementPredicateFallback
	}
	compactRevision, compact, err := readManagementCompactRevision(ctx, db, owner)
	if err != nil {
		return nil, "", err
	}
	scope.CompactRevision = compact
	endModels := managementReadSpan(ctx, "database_signal_models")
	models, ids, err := readManagementStreamModels(ctx, db, owner, strict, scope)
	endModels()
	if err != nil {
		return nil, "", err
	}
	if managementTraceEnabled(ctx) {
		log.Printf("management_signal_scope compact_revision=%t catalog_only=%t model_scoped=%t models=%d receipt_ids=%d request_id=%s", compact, scope.CatalogOnly, scope.Model != "", len(models), len(ids), consoleRequestID(ctx))
	}
	endReceipts := managementReadSpan(ctx, "database_signal_receipts")
	receipts, err := readManagementStreamReceipts(ctx, db, owner, ids, strict)
	endReceipts()
	if err != nil {
		return nil, "", err
	}
	stages := newManagementReadTimeline(ctx, "signal_model_order")
	defer stages.finish()
	// PostgreSQL COLLATE "C" orders UTF-8 text by bytes, exactly like Go's
	// string comparison. Preserve the existing revision byte stream verbatim.
	order := managementOrderedStreamModels(models)
	stages.next("signal_model_expand")
	out := make([]managementModelSignals, 0, len(models))
	hash := sha256.New()
	writer := newManagementRevisionWriter(hash)
	for i := range models {
		model := &models[i]
		if order != nil {
			model = order[i]
		}
		var receiptDigest, qualityDigest []byte
		if r, ok := receipts[model.probe]; ok {
			receiptDigest = r.evidence
			if model.item.Model.Result != nil {
				model.item.Model.Result.Availability.Usage = r.usage.expanded()
			}
		}
		if q, ok := receipts[model.quality]; ok {
			qualityDigest = q.evidence
		}
		if !compact {
			writer.write(model.item.Account, model.item.Group, model.item.Model.Model, model.item.Model.State, model.message, model.evidence, receiptDigest, qualityDigest)
		}
		out = append(out, model.item)
	}
	if compact {
		if ctx.Err() == nil {
			scope.ReceiptReuse.capture(db, owner, receipts)
		}
		return out, compactRevision, ctx.Err()
	}
	stages.next("signal_revision_finish")
	revision, err := finishManagementModelRevision(ctx, db, owner, hash)
	if err == nil && ctx.Err() == nil {
		scope.ReceiptReuse.capture(db, owner, receipts)
	}
	if err != nil {
		return nil, "", err
	}
	return out, revision, nil
}

func readManagementStreamModels(ctx context.Context, db controlQueryDB, owner string, strict bool, scope managementStreamScope) ([]managementStreamModel, []string, error) {
	stages := newManagementReadTimeline(ctx, "signal_model_directory")
	defer stages.finish()
	out, err := readManagementModelDirectory(ctx, db, owner, scope.CompactRevision, func() ([]managementStreamModel, error) {
		return readManagementModelSourceDirectory(ctx, db, owner)
	})
	if err != nil {
		return nil, nil, err
	}
	positions := managementStreamPositionIndex{}
	wanted := map[string]bool{}
	if scope.Model != "" {
		for _, name := range scope.Names {
			wanted[name] = true
		}
	}
	// The compact catalog returns before predicates are joined. It does not
	// consume an identity index for any of the directory's model rows.
	if !scope.CompactRevision || !scope.CatalogOnly {
		positions.values = map[managementStreamIdentity]int{}
		// Built-in selections have no missing-model fallback groups. Their
		// compact predicate SQL returns only Names, including every public alias.
		// Retain the complete index for legacy revisions and non-built-in models.
		if scope.CompactRevision && scope.Model != "" && slices.Contains(subModels, scope.Model) {
			positions.only = wanted
		}
	}
	for i, model := range out {
		positions.add(model.item, i)
	}
	stages.next("signal_model_scope")
	if managementTraceEnabled(ctx) {
		log.Printf("management_signal_index models=%d positions=%d scoped=%t request_id=%s", len(out), len(positions.values), positions.only != nil, consoleRequestID(ctx))
	}
	fallback := map[string]bool{}
	fallbackGroups := []subBoundKey{}
	if scope.Model != "" {
		fallbackGroups = managementStreamFallbackGroups(scope.Model, out)
		for _, g := range fallbackGroups {
			fallback[qualityGroupID(g.AccountID, g.GroupID)] = true
		}
	}
	query := managementModelStreamSQL
	args := []any{owner}
	if scope.CompactRevision && scope.CatalogOnly {
		return out, []string{}, ctx.Err()
	}
	if scope.CompactRevision && scope.Model != "" {
		raw, err := json.Marshal(fallbackGroups)
		if err != nil {
			return nil, nil, err
		}
		query += ` AND (p.model=ANY($2::text[]) OR EXISTS(SELECT 1 FROM jsonb_to_recordset($3::jsonb) AS g(account_id text,group_id bigint) WHERE g.account_id=p.account_id AND g.group_id=p.group_id))`
		args = append(args, scope.Names, string(raw))
	}
	stages.next("signal_model_predicates")
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	ids := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var key managementStreamIdentity
		var valid, present, hasUsage bool
		var evidence []byte
		var probe, quality string
		var checkedAt int64
		var verdict, availability, terminal, modelMatch string
		var usage managementStreamUsage
		if err := rows.Scan(&key.account, &key.group, &key.model, &valid, &present, &evidence, &checkedAt, &verdict, &availability, &terminal, &modelMatch, &probe, &quality, &hasUsage, &usage.Status, &usage.InputPrice, &usage.OutputPrice, &usage.InputTokens, &usage.OutputTokens); err != nil {
			return nil, nil, err
		}
		i, exists := positions.values[key]
		// The foreign key excludes orphan projections. Still never publish one if
		// a future storage implementation returns an identity absent from sources.
		if !exists {
			return nil, nil, errManagementPredicateFallback
		}
		if len(evidence) != sha256.Size || strict && !valid {
			return nil, nil, errManagementPredicateFallback
		}
		model := &out[i]
		model.evidence, model.probe, model.quality = evidence, probe, quality
		if present && !scope.CatalogOnly && (scope.Model == "" || wanted[key.model] || fallback[qualityGroupID(key.account, key.group)]) {
			result := &subResult{CheckedAt: checkedAt, Verdict: verdict, Availability: subProbe{Status: availability, TerminalStatus: terminal, ModelMatch: modelMatch}}
			model.item.Model.Result = result
			if hasUsage {
				result.Availability.Usage = usage.expanded()
			}
		}
		for _, id := range []string{probe, quality} {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	stages.next("signal_model_verify")
	for _, model := range out {
		if len(model.evidence) != sha256.Size && (!scope.CompactRevision || scope.Model == "" || wanted[model.item.Model.Model] || fallback[qualityGroupID(model.item.Account, model.item.Group)]) {
			return nil, nil, errManagementPredicateFallback
		}
	}
	return out, ids, ctx.Err()
}

func readManagementModelSourceDirectory(ctx context.Context, db controlQueryDB, owner string) ([]managementStreamModel, error) {
	rows, err := db.QueryContext(ctx, managementModelSourceStreamSQL, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []managementStreamModel{}
	for rows.Next() {
		var model managementStreamModel
		if err := rows.Scan(&model.item.Account, &model.item.Group, &model.item.Model.Model, &model.item.Model.State, &model.message); err != nil {
			return nil, err
		}
		out = append(out, model)
	}
	return out, rows.Err()
}

func readManagementStreamReceipts(ctx context.Context, db controlQueryDB, owner string, ids []string, strict bool) (map[string]managementStreamReceipt, error) {
	out := make(map[string]managementStreamReceipt)
	if len(ids) == 0 {
		return out, ctx.Err()
	}
	// One narrow owner scan has stable cost even when most current models have
	// different receipt IDs. An enormous ANY parameter can instead cause a
	// generic prepared plan to perform one random index lookup per ID. Retain
	// only referenced receipts, and never validate unrelated receipt payloads.
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	query := managementReceiptStreamSQL
	args := []any{owner}
	if len(ids) <= 4096 {
		query = managementReceiptSelectedStreamSQL
		args = append(args, ids)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var r managementStreamReceipt
		if err := rows.Scan(&id, &r.valid, &r.evidence, &r.usage.Status, &r.usage.InputPrice, &r.usage.OutputPrice, &r.usage.InputTokens, &r.usage.OutputTokens); err != nil {
			return nil, err
		}
		if !wanted[id] {
			continue
		}
		if strict && !r.valid {
			return nil, errManagementPredicateFallback
		}
		out[id] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, ctx.Err()
}

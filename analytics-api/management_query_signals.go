package main

import (
	"context"
	"crypto/sha256"
	"errors"
)

// A false support marker means use the full existing typed reader, not a
// successful partial result or a fabricated error for valid legacy JSON.
var errManagementPredicateFallback = errors.New("management predicates require the full typed reader")

// Predicate input only. This is deliberately separate from a page row: absence
// of timing/messages here must never be presented as a missing test result.
// The page must hydrate its selected groups under the same transaction before
// publishing, and complete-selection reads must retain their full action data.
type managementModelSignals struct {
	Account string
	Group   int64
	Model   subModelResult
}

const managementModelSignalsStoredColumns = `m.account_id,m.group_id,m.model,m.state,
 COALESCE(p.valid,false) AND COALESCE(r.valid,true) AND COALESCE(q.valid,true),COALESCE(p.present,false),COALESCE(p.checked_at,0),COALESCE(p.verdict,''),COALESCE(p.availability,''),COALESCE(p.terminal,''),COALESCE(p.model_match,''),
 CASE WHEN r.id IS NOT NULL THEN r.status ELSE COALESCE(p.usage_status,'') END,
 CASE WHEN r.id IS NOT NULL THEN r.input_price ELSE p.input_price END,
 CASE WHEN r.id IS NOT NULL THEN r.output_price ELSE p.output_price END,
 CASE WHEN r.id IS NOT NULL THEN r.input_tokens ELSE p.input_tokens END,
 CASE WHEN r.id IS NOT NULL THEN r.output_tokens ELSE p.output_tokens END,
 r.id IS NOT NULL OR COALESCE(p.has_usage,false)`

const managementModelSignalsStoredFrom = ` FROM console_sub_models m JOIN console_sub_accounts a ON a.id=m.account_id
 LEFT JOIN console_management_model_signal p USING(account_id,group_id,model)
 LEFT JOIN console_management_receipt_signal r ON r.id=NULLIF(p.probe_id,'')
 AND EXISTS(SELECT 1 FROM console_sub_accounts ra WHERE ra.id=r.account_id AND ra.owner=$1)
 LEFT JOIN console_management_receipt_signal q ON q.id=NULLIF(p.quality_probe_id,'')
 AND EXISTS(SELECT 1 FROM console_sub_accounts qa WHERE qa.id=q.account_id AND qa.owner=$1)
 WHERE a.owner=$1`

const managementModelSignalsStoredSQL = `SELECT ` + managementModelSignalsStoredColumns + managementModelSignalsStoredFrom + ` ORDER BY m.account_id,m.group_id,m.model COLLATE "C"`

func readCurrentManagementModelSignals(ctx context.Context, db controlQueryDB, owner string) ([]managementModelSignals, error) {
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT complete FROM console_management_projection_state WHERE singleton`).Scan(&complete); err != nil {
		return nil, err
	}
	if !complete {
		return nil, errManagementPredicateFallback
	}
	return readManagementSignalsColumns(ctx, db, owner, managementModelSignalsStoredSQL)
}

func readManagementSignalsColumns(ctx context.Context, db controlQueryDB, owner, query string) ([]managementModelSignals, error) {
	return readManagementSignalsColumnsArgs(ctx, db, query, owner)
}

func readManagementSignalsColumnsArgs(ctx context.Context, db controlQueryDB, query string, args ...any) ([]managementModelSignals, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []managementModelSignals{}
	for rows.Next() {
		var item managementModelSignals
		var valid, present, hasUsage bool
		result := &subResult{}
		usage := &subUsage{}
		if err := rows.Scan(&item.Account, &item.Group, &item.Model.Model, &item.Model.State, &valid, &present, &result.CheckedAt, &result.Verdict,
			&result.Availability.Status, &result.Availability.TerminalStatus, &result.Availability.ModelMatch,
			&usage.Status, &usage.InputPrice, &usage.OutputPrice, &usage.InputTokens, &usage.OutputTokens, &hasUsage); err != nil {
			return nil, err
		}
		if !valid {
			return nil, errManagementPredicateFallback
		}
		if present {
			item.Model.Result = result
			if hasUsage {
				result.Availability.Usage = usage
			}
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// The paged path needs both predicates and the complete evidence revision.
// Read their shared model/receipt join once, under the caller's transaction;
// full action selections retain the same revision encoding and queue fence.
func readCurrentManagementSignalsAndRevision(ctx context.Context, db controlQueryDB, owner string) ([]managementModelSignals, string, error) {
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT complete FROM console_management_projection_state WHERE singleton`).Scan(&complete); err != nil {
		return nil, "", err
	}
	if !complete {
		return nil, "", errManagementPredicateFallback
	}
	query := `SELECT ` + managementModelSignalsStoredColumns + `,m.message,p.evidence_digest,r.evidence_digest,q.evidence_digest` + managementModelSignalsStoredFrom + ` ORDER BY m.account_id COLLATE "C",m.group_id,m.model COLLATE "C"`
	rows, err := db.QueryContext(ctx, query, owner)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []managementModelSignals{}
	hash := sha256.New()
	writer := newManagementRevisionWriter(hash)
	for rows.Next() {
		var item managementModelSignals
		var valid, present, hasUsage bool
		var message string
		var evidence, receipt, quality []byte
		result, usage := &subResult{}, &subUsage{}
		if err := rows.Scan(&item.Account, &item.Group, &item.Model.Model, &item.Model.State, &valid, &present, &result.CheckedAt, &result.Verdict,
			&result.Availability.Status, &result.Availability.TerminalStatus, &result.Availability.ModelMatch,
			&usage.Status, &usage.InputPrice, &usage.OutputPrice, &usage.InputTokens, &usage.OutputTokens, &hasUsage,
			&message, &evidence, &receipt, &quality); err != nil {
			return nil, "", err
		}
		if !valid || len(evidence) != sha256.Size {
			return nil, "", errManagementPredicateFallback
		}
		writer.write(item.Account, item.Group, item.Model.Model, item.Model.State, message, evidence, receipt, quality)
		if present {
			item.Model.Result = result
			if hasUsage {
				result.Availability.Usage = usage
			}
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	rows.Close()
	revision, err := finishManagementModelRevision(ctx, db, owner, hash)
	if err != nil {
		return nil, "", err
	}
	return out, revision, nil
}

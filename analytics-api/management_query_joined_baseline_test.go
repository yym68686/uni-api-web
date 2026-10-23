package main

import (
	"context"
	"crypto/sha256"
)

func readManagementJoinedSignalsAndRevision(ctx context.Context, db controlQueryDB, owner string) ([]managementModelSignals, string, error) {
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

func readManagementJoinedRevisionCatalogFrom(ctx context.Context, db controlQueryDB, owner string, catalog bool) (string, []managementModelSignals, error) {
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT complete FROM console_management_projection_state WHERE singleton`).Scan(&complete); err != nil {
		return "", nil, err
	}
	if !complete {
		return "", nil, errManagementPredicateFallback
	}
	rows, err := db.QueryContext(ctx, `SELECT m.account_id,m.group_id,m.model,m.state,m.message,p.evidence_digest,r.evidence_digest,q.evidence_digest,COALESCE(p.valid,false) AND COALESCE(r.valid,true) AND COALESCE(q.valid,true)
 FROM console_sub_models m JOIN console_sub_accounts a ON a.id=m.account_id
 LEFT JOIN console_management_model_signal p USING(account_id,group_id,model)
 LEFT JOIN console_management_receipt_signal r ON r.id=NULLIF(p.probe_id,'') AND EXISTS(SELECT 1 FROM console_sub_accounts ra WHERE ra.id=r.account_id AND ra.owner=$1)
 LEFT JOIN console_management_receipt_signal q ON q.id=NULLIF(p.quality_probe_id,'') AND EXISTS(SELECT 1 FROM console_sub_accounts qa WHERE qa.id=q.account_id AND qa.owner=$1)
 WHERE a.owner=$1 ORDER BY m.account_id COLLATE "C",m.group_id,m.model COLLATE "C"`, owner)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var models []managementModelSignals
	if catalog {
		models = []managementModelSignals{}
	}
	hash := sha256.New()
	writer := newManagementRevisionWriter(hash)
	for rows.Next() {
		var account, model, state, message string
		var group int64
		var evidence, receipt, quality []byte
		var supported bool
		if err := rows.Scan(&account, &group, &model, &state, &message, &evidence, &receipt, &quality, &supported); err != nil {
			return "", nil, err
		}
		if len(evidence) != sha256.Size || catalog && !supported {
			return "", nil, errManagementPredicateFallback
		}
		if catalog {
			models = append(models, managementModelSignals{Account: account, Group: group, Model: subModelResult{Model: model, State: state}})
		}
		writer.write(account, group, model, state, message, evidence, receipt, quality)
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	rows.Close()
	revision, err := finishManagementModelRevision(ctx, db, owner, hash)
	return revision, models, err
}

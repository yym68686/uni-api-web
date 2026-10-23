package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
)

// Stream fixed-size evidence digests rather than serializing every model reply
// into a page revision. Both compact page reads and full selections use these
// same committed identities; omitted presentation fields still invalidate them.
func readManagementModelRevisionFrom(ctx context.Context, db controlQueryDB, owner string) (string, error) {
	revision, _, err := readManagementRevisionCatalogFrom(ctx, db, owner, false)
	return revision, err
}

func readManagementRevisionCatalogFrom(ctx context.Context, db controlQueryDB, owner string, catalog bool) (string, []managementModelSignals, error) {
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
	encoder := json.NewEncoder(hash)
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
		if err := encoder.Encode([]any{account, group, model, state, summaryMessage(message), evidence, receipt, quality}); err != nil {
			return "", nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	rows.Close()
	revision, err := finishManagementModelRevision(ctx, db, owner, hash)
	return revision, models, err
}

func finishManagementModelRevision(ctx context.Context, db controlQueryDB, owner string, hash hash.Hash) (string, error) {
	encoder := json.NewEncoder(hash)
	queued, err := db.QueryContext(ctx, `SELECT q.account_id,q.group_id,q.kind,q.model FROM console_sub_check_queue q JOIN console_sub_accounts a ON a.id=q.account_id WHERE a.owner=$1 ORDER BY q.account_id COLLATE "C",q.group_id,q.kind COLLATE "C",q.model COLLATE "C"`, owner)
	if err != nil {
		return "", err
	}
	defer queued.Close()
	for queued.Next() {
		var account, kind, model string
		var group int64
		if err := queued.Scan(&account, &group, &kind, &model); err != nil {
			return "", err
		}
		if err := encoder.Encode([]any{account, group, kind, model}); err != nil {
			return "", err
		}
	}
	if err := queued.Err(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func managementProjectedRevision(in managementQueryInput, page managementQueryPage, evidence managementFilterEvidence) (string, error) {
	// The same header/capability/queue/price/configuration values accompany both
	// reads. Hydrating page model details cannot alter this revision contract.
	compact := in
	compact.Accounts = make([]subAccount, len(in.Accounts))
	for i, account := range in.Accounts {
		compact.Accounts[i] = account
		compact.Accounts[i].Targets = make([]subTarget, len(account.Targets))
		for j, target := range account.Targets {
			compact.Accounts[i].Targets[j] = target
			compact.Accounts[i].Targets[j].Models = nil
		}
	}
	raw, err := json.Marshal(struct {
		Version         string
		ModelsRevision  string
		Input           managementQueryInput
		Sources         map[string]string
		Models          []string
		DetectionModels []string
		Evidence        managementFilterEvidence
	}{"management-projected-v1", in.ModelsRevision, compact, in.SourceRevisions, page.Models, page.DetectionModels, evidence})
	if err != nil {
		return "", err
	}
	return tokenHash(string(raw)), nil
}

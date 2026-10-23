package main

import (
	"context"
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
	models, revision, err := readManagementScopedStreams(ctx, db, owner, catalog, managementStreamScope{CatalogOnly: true})
	if err != nil {
		return "", nil, err
	}
	if !catalog {
		return revision, nil, nil
	}
	for i := range models {
		models[i].Model.Result = nil
	}
	return revision, models, nil
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

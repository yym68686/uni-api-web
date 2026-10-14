package main

import (
	"context"
	"errors"
)

type subSyncScopeKey struct{}

// The durable sync task captures the checkbox selection at submission time.
// An absent scope is retained only for legacy single-account/initial syncs.
func withSubSyncModels(ctx context.Context, models []string) context.Context {
	return context.WithValue(ctx, subSyncScopeKey{}, models)
}

func subSyncScopeModels(ctx context.Context) []string {
	models, _ := ctx.Value(subSyncScopeKey{}).([]string)
	return models
}

func subSyncIncludesModel(ctx context.Context, model string) bool {
	models, scoped := ctx.Value(subSyncScopeKey{}).([]string)
	if !scoped {
		return true
	}
	for _, selected := range models {
		if model == selected {
			return true
		}
	}
	return false
}

func subSyncModelResults(ctx context.Context, models []subModelResult) []subModelResult {
	out := make([]subModelResult, 0, len(models))
	for _, model := range models {
		if subSyncIncludesModel(ctx, model.Model) {
			out = append(out, model)
		}
	}
	return out
}

// Do not use successful history from unchecked models to trigger more paid
// capability probes. Groups without a selected successful GPT/Codex model keep
// their previous capability results untouched.
func (s *Service) subQueueSyncCapabilities(ctx context.Context, id, job, kind string) error {
	if kind != "compaction" && kind != "tool_use" {
		return errors.New("无效能力检测类型")
	}
	_, err := s.control.db.ExecContext(ctx, `UPDATE console_sub_targets t SET `+kind+`_state='queued'
 WHERE account_id=$1 AND active AND state='done'
 AND EXISTS(SELECT 1 FROM console_sub_accounts a WHERE a.id=t.account_id AND a.job_id=$2 AND a.state='running'
 AND (a.sync_models IS NULL OR EXISTS(SELECT 1 FROM console_sub_models m
 WHERE m.account_id=t.account_id AND m.group_id=t.group_id AND a.sync_models ? m.model
 AND m.state='done' AND m.result->'availability'->>'status'='success'
 AND (m.model LIKE 'gpt%' OR m.model LIKE 'codex%'))))`, id, job)
	return err
}

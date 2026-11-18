package main

import (
	"context"
	"errors"
)

type subSyncScopeKey struct{}

func subSyncMetadataOnly(ctx context.Context) bool {
	models, scoped := ctx.Value(subSyncScopeKey{}).([]string)
	return scoped && models != nil && len(models) == 0
}

// The durable sync task captures the checkbox selection at submission time.
// The worker normalizes a missing legacy scope to metadata-only at claim.
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
	// Commit the stage and its queue together. A successor runs only the
	// still-queued groups of that stage, not an already completed stage.
	_, err := s.subProbeWrite(ctx, `WITH advanced AS (
 UPDATE console_sub_accounts SET sync_phase=$3
 WHERE id=$1 AND job_id=$2 AND state='running' AND sync_lease_until>clock_timestamp()
 AND ($3='compaction' AND sync_phase NOT IN ('compaction','tool_use') OR $3='tool_use' AND sync_phase='compaction')
 RETURNING id,owner,sync_models,sync_run_id
 ) UPDATE console_sub_targets t SET `+kind+`_state='queued' FROM advanced a
 WHERE t.account_id=a.id AND t.active AND t.state='done'
 AND NOT EXISTS(SELECT 1 FROM console_channel_detection_settings d WHERE d.owner=a.owner AND d.account_id=t.account_id AND d.group_id=t.group_id)
 AND (a.sync_models IS NULL OR EXISTS(SELECT 1 FROM console_sub_models m
 WHERE m.account_id=t.account_id AND m.group_id=t.group_id AND a.sync_models ? m.model
 AND (a.sync_run_id='' OR m.sync_run_id=a.sync_run_id)
 AND m.state='done' AND m.result->'availability'->>'status'='success'
 AND (m.model LIKE 'gpt%' OR m.model LIKE 'codex%')))`, id, job, kind)
	return err
}

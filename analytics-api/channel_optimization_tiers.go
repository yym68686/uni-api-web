package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type optimizationTierChoice struct {
	serviceTierRoutingChoice
	EvidenceModels []string `json:"evidence_models"`
}

func validOptimizationTierChoices(choices []optimizationTierChoice) bool {
	seen := map[[2]string]bool{}
	for _, choice := range choices {
		key := [2]string{choice.Provider, choice.Tier}
		if seen[key] || !isServiceTier(choice.Tier) || !serviceTierDigest(choice.Scope) ||
			strings.TrimSpace(choice.Provider) == "" || len(choice.Provider) > 256 || strings.ContainsAny(choice.Provider, "\r\n/") ||
			len(choice.EvidenceModels) == 0 {
			return false
		}
		names := map[string]bool{}
		for _, model := range choice.EvidenceModels {
			if model == "" || len(model) > 256 || names[model] || strings.ContainsAny(model, "\r\n") {
				return false
			}
			names[model] = true
		}
		seen[key] = true
	}
	return true
}

func (s *Service) prepareOptimizationTierSettings(ctx context.Context, src controlSource, in channelBatchInput) (map[string]map[string]any, error) {
	if len(in.TierChoices) == 0 {
		return nil, nil
	}
	providers, err := effectiveProviders(ctx, src, true)
	if err != nil {
		return nil, errors.New("无法确认当前档位配置")
	}
	live := map[string]configuredProvider{}
	for _, provider := range providers {
		live[provider.Provider] = provider
	}
	settings := map[string]map[string]any{}
	for _, choice := range in.TierChoices {
		p := live[choice.Provider]
		current := p.ServiceTierRouting[choice.Tier]
		if current == "" || serviceTierScope(src, p.ServiceTierScope) != choice.Scope || (current == "enabled") == choice.Enabled {
			return nil, errors.New("渠道档位配置已变化，请重新预览")
		}
		for _, target := range in.Targets {
			if target.Provider == choice.Provider && p.Temporary {
				remaining := false
				for model := range target.Models {
					remaining = remaining || target.SplitProtocols[model] == ""
				}
				if !remaining {
					return nil, fmt.Errorf("%s：所选模型变更会移除此接入，请取消其档位变更", choice.Provider)
				}
			}
		}
		set := settings[choice.Provider]
		if set == nil {
			set = map[string]any{}
			settings[choice.Provider] = set
		}
		if err := s.validateOptimizationTierEvidence(ctx, src, in.Targets, choice, set); err != nil {
			return nil, fmt.Errorf("%s · %s：%w", choice.Provider, choice.Tier, err)
		}
		set["/preferences/service_tier_disabled/"+choice.Tier] = !choice.Enabled
	}
	return settings, nil
}

func (s *Service) validateOptimizationTierEvidence(ctx context.Context, src controlSource, targets []channelBatchTarget, choice optimizationTierChoice, set map[string]any) error {
	rows, err := s.control.db.QueryContext(ctx, `SELECT model,state,result FROM console_configured_checks
 WHERE source_id=$1 AND source_target=$2 AND provider=$3 AND kind=$4 AND fingerprint=$5`,
		src.ID, controlTarget(src), choice.Provider, choice.Tier, choice.Scope)
	if err != nil {
		return err
	}
	defer rows.Close()
	now := time.Now().Unix()
	found := false
	expected := map[string]bool{}
	for _, model := range choice.EvidenceModels {
		expected[model] = true
	}
	for rows.Next() {
		var model, state string
		var raw []byte
		if err := rows.Scan(&model, &state, &raw); err != nil {
			return err
		}
		kept := true
		for _, target := range targets {
			if target.Provider == choice.Provider && (target.Models[model] == "" || target.Current[model] != target.Models[model]) {
				kept = false
			}
		}
		if !kept && choice.Enabled {
			continue
		}
		var result serviceTierResult
		status := tierUnknown
		if state == "done" && json.Unmarshal(raw, &result) == nil && serviceTierDigest(result.Binding) && serviceTierFresh(result.CheckedAt, now) {
			switch result.Status {
			case "not_honored", "unsupported":
				status = result.Status
			case "supported":
				status = serviceTierFilterStatus(result, now)
			}
		}
		if choice.Enabled && status == "supported" && expected[model] {
			set["/preferences/service_tier_evidence/"+result.VerifiedBinding+"/"+choice.Tier] = result.VerifiedAt
			found = true
		}
		if !choice.Enabled {
			if status == "supported" || expected[model] && status != "unsupported" && status != "not_honored" {
				return errors.New("仍有模型支持此档位或检测尚未确认，请重新预览")
			}
			delete(expected, model)
			found = len(expected) == 0
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !found {
		return errors.New("没有当前接入的有效档位证据，请先检测")
	}
	return nil
}

func applyOptimizationTierSettings(snapshot *retainedSnapshot, settings map[string]map[string]any) error {
	for provider, paths := range settings {
		var change struct {
			Set    map[string]any `json:"set"`
			Remove []string       `json:"remove"`
		}
		if raw := snapshot.Settings[provider]; len(raw) > 0 && json.Unmarshal(raw, &change) != nil {
			return errors.New("渠道覆盖配置无效")
		}
		for _, removed := range change.Remove {
			for path := range paths {
				if removed == path || strings.HasPrefix(path, removed+"/") {
					return errors.New("档位设置与已有覆盖冲突，请检查渠道配置")
				}
			}
		}
		if change.Set == nil {
			change.Set = map[string]any{}
		}
		for path, value := range paths {
			change.Set[path] = value
		}
		if snapshot.Settings == nil {
			snapshot.Settings = map[string]json.RawMessage{}
		}
		snapshot.Settings[provider] = json.RawMessage(mustJSON(change))
	}
	return nil
}

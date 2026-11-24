package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type serviceTierRoutingChoice struct {
	Provider string `json:"provider"`
	Scope    string `json:"scope"`
	Tier     string `json:"tier"`
	Enabled  bool   `json:"enabled"`
}

// This endpoint cannot change membership, order or the other tier. Enabling
// requires an existing receipt for this exact live provider and credential.
func (s *Service) applyServiceTierRouting(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision  string                     `json:"revision"`
		Operation string                     `json:"operation_id"`
		Choices   []serviceTierRoutingChoice `json:"choices"`
	}
	if !decodeConfiguration(w, r, &in) {
		return
	}
	if len(in.Choices) == 0 || len(in.Choices) > 64 || in.Operation == "" || len(in.Operation) > 128 || in.Revision == "" || len(in.Revision) > 256 {
		http.Error(w, "无效档位设置", 400)
		return
	}
	seen := map[[2]string]bool{}
	for _, choice := range in.Choices {
		key := [2]string{choice.Provider, choice.Tier}
		if seen[key] || !isServiceTier(choice.Tier) || !serviceTierDigest(choice.Scope) ||
			strings.TrimSpace(choice.Provider) == "" || len(choice.Provider) > 256 || strings.ContainsAny(choice.Provider, "\r\n/") {
			http.Error(w, "无效档位设置", 400)
			return
		}
		seen[key] = true
	}
	ctx := r.Context()
	src, err := s.control.source(ctx, r.PathValue("id"))
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	providers, err := effectiveProviders(ctx, src, true)
	if err != nil {
		http.Error(w, "无法确认当前档位配置", 409)
		return
	}
	live := map[string]configuredProvider{}
	for _, p := range providers {
		live[p.Provider] = p
	}
	byProvider := map[string]map[string]any{}
	order := []string{}
	for _, choice := range in.Choices {
		p := live[choice.Provider]
		if p.ServiceTierRouting[choice.Tier] == "" || serviceTierScope(src, p.ServiceTierScope) != choice.Scope {
			http.Error(w, "渠道配置已变化或来源尚不支持档位开关", 409)
			return
		}
		set := byProvider[choice.Provider]
		if set == nil {
			set = map[string]any{}
			byProvider[choice.Provider] = set
			order = append(order, choice.Provider)
		}
		if choice.Enabled && p.ServiceTierRouting[choice.Tier] != "disabled_verified" && p.ServiceTierRouting[choice.Tier] != "enabled" {
			if err := s.tierRoutingEvidence(r, src, choice, set); err != nil {
				http.Error(w, "没有当前渠道的有效档位证据，请先检测", 409)
				return
			}
		}
		set["/preferences/service_tier_disabled/"+choice.Tier] = !choice.Enabled
	}
	// The standard operation journal rechecks this revision under the source
	// lock and atomically persists the settings; lost receipts are not retried.
	changes := make([]channelSettingChange, 0, len(order))
	for _, p := range order {
		changes = append(changes, channelSettingChange{Provider: p, Set: byProvider[p]})
	}
	s.applyChannelSettings(w, r, channelSettingMutation{Revision: in.Revision, Operation: in.Operation, Changes: changes})
}

func (s *Service) tierRoutingEvidence(r *http.Request, src controlSource, choice serviceTierRoutingChoice, set map[string]any) error {
	rows, err := s.control.db.QueryContext(r.Context(), `SELECT result FROM console_configured_checks
 WHERE source_id=$1 AND source_target=$2 AND provider=$3 AND kind=$4 AND fingerprint=$5`,
		src.ID, controlTarget(src), choice.Provider, choice.Tier, choice.Scope)
	if err != nil {
		return err
	}
	defer rows.Close()
	now := time.Now().Unix()
	found := false
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var result serviceTierResult
		if json.Unmarshal(raw, &result) == nil && serviceTierFilterStatus(result, now) == "supported" {
			set["/preferences/service_tier_evidence/"+result.VerifiedBinding+"/"+choice.Tier] = result.VerifiedAt
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !found {
		return errors.New("no verified evidence")
	}
	return nil
}

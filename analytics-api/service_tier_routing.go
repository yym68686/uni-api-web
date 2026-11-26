package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
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

// Resolve the exact live provider/model, not a cached management inventory.
// verify_only is local to the gateway and does not start an upstream request.
func (s *Service) serviceTierTarget(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	provider, model := q.Get("provider"), q.Get("model")
	if !validTierRoutingProvider(provider) || !validProbeModel(model) {
		http.Error(w, "无效渠道或模型", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	src, err := s.control.source(ctx, r.PathValue("id"))
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	admin, err := serviceTierAdmin(src)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	target, err := serviceTierGateway(ctx, admin, provider, model, "fast", "", true)
	if err != nil {
		http.Error(w, "无法读取当前渠道档位检测范围", 503)
		return
	}
	out := map[string]any{"applicable": target.Status == "ready", "scope": serviceTierScope(src, target.Scope)}
	if target.Status == "ready" && target.UpstreamModel != "" && generatedSubProvider(provider) {
		owner, _ := s.controlUser(r)
		out["group_results"], err = s.installedGroupTierResults(ctx, src, owner, provider, target.UpstreamModel, target.Scope)
		if err != nil {
			http.Error(w, "无法读取分组档位检测结果", 503)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}

func (s *Service) installedGroupTierResults(ctx context.Context, src controlSource, owner, provider, upstream, scope string) (map[string]subTierProbe, error) {
	providers, err := effectiveProviders(ctx, src, true)
	if err != nil {
		return nil, err
	}
	var base string
	for _, p := range providers {
		if p.Provider == provider && p.Temporary && !p.IdentityChanged && p.ServiceTierScope == scope {
			base = p.Base
			break
		}
	}
	if base == "" {
		return nil, nil
	}
	raw, _, err := subGateway(ctx, src, http.MethodGet, "/v1/channel-controls", nil)
	if err != nil {
		return nil, err
	}
	var state subGatewayControls
	if err := decodeMap(raw, &state); err != nil {
		return nil, err
	}
	refs, err := s.control.subChannelRefs(ctx, owner)
	if err != nil {
		return nil, err
	}
	index := newSubProviderRefIndex(refs)
	for _, channel := range state.Temporary {
		if channel.Provider != provider || channel.IdentityChanged || channel.KeyID == "" {
			continue
		}
		matches := index.matches(provider, channel.KeyID, base)
		if len(matches) != 1 {
			return nil, nil
		}
		ref := matches[0]
		results, err := s.groupTierResults(ctx, ref.Account, ref.Group, []string{upstream})
		return results[upstream], err
	}
	return nil, nil
}

func (s *Service) serviceTierRouting(w http.ResponseWriter, r *http.Request) {
	src, err := s.control.source(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	provider := r.URL.Query().Get("provider")
	if !validTierRoutingProvider(provider) {
		http.Error(w, "无效渠道", 400)
		return
	}
	providers, err := effectiveProviders(r.Context(), src, true)
	if err != nil {
		http.Error(w, "无法读取当前档位配置", 503)
		return
	}
	for _, p := range providers {
		if p.Provider == provider {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, map[string]any{"scope": serviceTierScope(src, p.ServiceTierScope), "routing": p.ServiceTierRouting})
			return
		}
	}
	http.Error(w, "渠道不存在", 404)
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
	s.applyChannelSettings(w, r, channelSettingMutation{Revision: in.Revision, Operation: in.Operation, TierChoices: in.Choices})
}

func validTierRoutingProvider(provider string) bool {
	return strings.TrimSpace(provider) != "" && len(provider) <= 256 && !strings.ContainsAny(provider, "\r\n/")
}

func validateServiceTierChoices(choices []serviceTierRoutingChoice) error {
	if len(choices) > 64 {
		return errors.New("无效档位设置")
	}
	seen := map[[2]string]bool{}
	for _, choice := range choices {
		key := [2]string{choice.Provider, choice.Tier}
		if seen[key] || !isServiceTier(choice.Tier) || !serviceTierDigest(choice.Scope) || !validTierRoutingProvider(choice.Provider) {
			return errors.New("无效档位设置")
		}
		seen[key] = true
	}
	return nil
}

// Resolve against the live provider both at preview and under the apply lock.
// The gateway only receives ordinary settings patches with confirmed evidence.
func (s *Service) resolveServiceTierChoices(ctx context.Context, src controlSource, in channelSettingMutation) (channelSettingMutation, int, error) {
	if len(in.TierChoices) == 0 {
		return in, 0, nil
	}
	if err := validateServiceTierChoices(in.TierChoices); err != nil {
		return in, 400, err
	}
	providers, err := effectiveProviders(ctx, src, true)
	if err != nil {
		return in, 409, errors.New("无法确认当前档位配置")
	}
	live := map[string]configuredProvider{}
	for _, p := range providers {
		live[p.Provider] = p
	}
	changes := append([]channelSettingChange(nil), in.Changes...)
	byProvider := map[string]int{}
	for i, change := range changes {
		if _, duplicate := byProvider[change.Provider]; duplicate {
			return in, 400, errors.New("同一渠道不能重复提交设置")
		}
		byProvider[change.Provider] = i
	}
	for _, choice := range in.TierChoices {
		if choice.Enabled {
			for _, change := range in.Changes {
				// Global preferences can change the proxy/headers for every
				// provider. Keep cross-provider edits in a separate operation.
				if change.Provider != choice.Provider && (change.Reset || change.CopyToKey != "" || change.CreateToKey != "" || change.DeleteCopy || len(change.Set) != 0 || len(change.Remove) != 0) {
					return in, 400, errors.New("请将其他渠道或全局设置与开启档位路由分开保存")
				}
			}
		}
	}
	for _, choice := range in.TierChoices {
		p := live[choice.Provider]
		if p.ServiceTierRouting[choice.Tier] == "" || serviceTierScope(src, p.ServiceTierScope) != choice.Scope {
			return in, 409, errors.New("渠道配置已变化或来源尚不支持档位开关")
		}
		index, ok := byProvider[choice.Provider]
		if !ok {
			index = len(changes)
			byProvider[choice.Provider] = index
			changes = append(changes, channelSettingChange{Provider: choice.Provider})
		}
		// Check only the regular draft, not the paths resolved for another tier.
		if index < len(in.Changes) {
			if err := checkTierRoutingDraft(in.Changes[index], choice.Enabled); err != nil {
				return in, 400, err
			}
		}
		set := maps.Clone(changes[index].Set)
		if set == nil {
			set = map[string]any{}
		}
		if choice.Enabled && p.ServiceTierRouting[choice.Tier] != "disabled_verified" && p.ServiceTierRouting[choice.Tier] != "enabled" {
			if err := s.tierRoutingEvidence(ctx, src, choice, set); err != nil {
				return in, 409, errors.New("没有当前渠道的有效档位证据，请先检测")
			}
		}
		set["/preferences/service_tier_disabled/"+choice.Tier] = !choice.Enabled
		changes[index].Set = set
	}
	in.Changes, in.TierChoices = changes, nil
	return in, 0, nil
}

func checkTierRoutingDraft(change channelSettingChange, enabling bool) error {
	if change.Reset || change.CopyToKey != "" || change.CreateToKey != "" || change.DeleteCopy {
		return errors.New("恢复、复制或创建渠道与档位开关需分开保存")
	}
	keys := append([]string(nil), change.Remove...)
	for path := range change.Set {
		keys = append(keys, path)
	}
	for _, path := range keys {
		if path == "/preferences" || strings.HasPrefix(path, "/preferences/service_tier_") {
			return errors.New("请通过档位开关单独修改档位配置")
		}
		if !enabling {
			continue
		}
		for _, bound := range []string{"/api", "/base_url", "/engine", "/model", "/exclude_endpoints", "/only_request_types", "/exclude_request_types", "/preferences/proxy", "/preferences/headers", "/preferences/passthrough_request_headers", "/preferences/__model_upstream_protocols", "/preferences/upstream_stream_mode", "/preferences/post_body_parameter_overrides"} {
			if path == bound || strings.HasPrefix(path, bound+"/") {
				return errors.New("请先保存影响档位检测的配置，重新检测后再开启路由")
			}
		}
	}
	return nil
}

func (s *Service) tierRoutingEvidence(ctx context.Context, src controlSource, choice serviceTierRoutingChoice, set map[string]any) error {
	rows, err := s.control.db.QueryContext(ctx, `SELECT result FROM console_configured_checks
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

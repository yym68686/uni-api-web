package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

func (s *Service) groupTierResults(ctx context.Context, account string, group int64, upstreams []string) (map[string]map[string]subTierProbe, error) {
	rows, err := s.control.db.QueryContext(ctx, `SELECT model,result->'service_tiers' FROM console_sub_models
 WHERE account_id=$1 AND group_id=$2 AND model=ANY($3) AND result ? 'service_tiers'`, account, group, upstreams)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := map[string]map[string]subTierProbe{}
	for rows.Next() {
		var model string
		var raw []byte
		if err = rows.Scan(&model, &raw); err != nil {
			return nil, err
		}
		var tiers map[string]subTierProbe
		if json.Unmarshal(raw, &tiers) == nil {
			results[model] = tiers
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Service) siteTierImportCounts(ctx context.Context, account string, group int64, models map[string]string) (map[string]int, error) {
	counts := map[string]int{}
	results, err := s.groupTierResults(ctx, account, group, slices.Collect(maps.Values(models)))
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	for _, upstream := range models {
		for tier, result := range results[upstream] {
			if isServiceTier(tier) && result.Status == "supported" && serviceTierFresh(result.CheckedAt, now) {
				counts[tier]++
			}
		}
	}
	return counts, nil
}

type serviceTierImportBinding struct {
	Binding       string `json:"binding"`
	Compatibility string `json:"compatibility"`
}

type serviceTierImportPlan struct {
	set    map[string]any
	counts map[string]int
}

// The site probe covers a group and wire model. Build a fresh binding
// for the installed configuration without contacting the upstream again.
func (s *Service) planGroupTierImport(ctx context.Context, src controlSource, provider, base, key string, models map[string]string, in subImportInput) (serviceTierImportPlan, error) {
	plan := serviceTierImportPlan{set: map[string]any{}, counts: map[string]int{}}
	results, err := s.groupTierResults(ctx, in.AccountID, in.GroupID, slices.Collect(maps.Values(models)))
	if err != nil {
		return plan, err
	}
	admin, err := serviceTierAdmin(src)
	if err != nil {
		return plan, err
	}
	bindings, err := serviceTierImportBindings(ctx, admin, provider, base, key, models)
	if err != nil {
		return plan, err
	}
	now := time.Now().Unix()
	for public, upstream := range models {
		binding := bindings[public].Binding
		if !serviceTierDigest(binding) {
			continue
		}
		for tier, result := range results[upstream] {
			if in.ServiceTierSiteChoices[tier] && result.Status == "supported" && serviceTierFresh(result.CheckedAt, now) {
				plan.set["/preferences/service_tier_evidence/"+binding+"/"+tier] = result.CheckedAt
				plan.counts[tier]++
			}
		}
	}
	return plan, nil
}

func validTierChoices(v map[string]bool) bool {
	for tier := range v {
		if !isServiceTier(tier) {
			return false
		}
	}
	return true
}

// No upstream access: the gateway compiles the prospective channel in memory
// and returns only credential-bound digests. The business key never leaves
// the console/gateway trust boundary. This is not a billable probe.
func serviceTierImportBindings(ctx context.Context, admin controlSource, provider, base, key string, models map[string]string) (map[string]serviceTierImportBinding, error) {
	input := map[string]any{"provider": provider, "model": "candidate", "tier": "fast", "verify_only": true,
		"candidate": map[string]any{"base_url": base, "api_key": key, "models": models}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, admin.Base+serviceTierProbePath, bytes.NewBufferString(mustJSON(input)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+admin.Key)
	req.Header.Set("Content-Type", "application/json")
	req.GetBody = nil
	client := &http.Client{Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("无法验证新接入的档位配置")
	}
	defer res.Body.Close()
	var out struct {
		Models map[string]serviceTierImportBinding `json:"models"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 96*1024)).Decode(&out) != nil || out.Models == nil {
		return nil, errors.New("来源无法验证新接入的档位配置")
	}
	return out.Models, nil
}

func (s *Service) planServiceTierImport(ctx context.Context, src controlSource, provider, base, key string, models map[string]string) (serviceTierImportPlan, error) {
	plan := serviceTierImportPlan{set: map[string]any{}, counts: map[string]int{}}
	if key == "" || len(models) == 0 || len(models) > 128 {
		return plan, nil
	}
	now := time.Now().Unix()
	names := make([]string, 0, 2*len(models))
	for public, upstream := range models {
		names = append(names, public, upstream)
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT kind,result FROM console_configured_checks
 WHERE source_id=$1 AND source_target=$2 AND kind IN ('fast','ultrafast')
 AND (model=ANY($3) OR result->>'upstream_model'=ANY($3))
 AND (COALESCE(result->>'verified_at','') <> '' OR result->>'status'='unsupported')`, src.ID, controlTarget(src), names)
	if err != nil {
		return plan, err
	}
	receipts := map[string]map[string]int64{"fast": {}, "ultrafast": {}}
	denials := map[string]map[string]int64{"fast": {}, "ultrafast": {}}
	for rows.Next() {
		var tier string
		var raw []byte
		if err = rows.Scan(&tier, &raw); err != nil {
			rows.Close()
			return plan, err
		}
		var result serviceTierResult
		if !isServiceTier(tier) || json.Unmarshal(raw, &result) != nil {
			continue
		}
		entries, at := receipts[tier], result.VerifiedAt
		switch serviceTierFilterStatus(result, now) {
		case "supported":
		case "unsupported":
			entries, at = denials[tier], result.CheckedAt
		default:
			continue
		}
		for _, id := range []string{result.Binding, result.Compatibility} {
			if serviceTierDigest(id) && at > entries[id] {
				entries[id] = at
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(receipts["fast"])+len(receipts["ultrafast"]) == 0 {
		return plan, err
	}
	admin, err := serviceTierAdmin(src)
	if err != nil {
		return plan, err
	}
	bindings, err := serviceTierImportBindings(ctx, admin, provider, base, key, models)
	if err != nil {
		return plan, err
	}
	for model := range models {
		binding := bindings[model]
		if !serviceTierDigest(binding.Binding) {
			continue
		}
		for tier, entries := range receipts {
			at := max(entries[binding.Binding], entries[binding.Compatibility])
			if at <= max(denials[tier][binding.Binding], denials[tier][binding.Compatibility]) {
				continue
			}
			plan.counts[tier]++
			plan.set["/preferences/service_tier_evidence/"+binding.Binding+"/"+tier] = at
		}
	}
	return plan, nil
}

func (s *Service) previewServiceTierImport(w http.ResponseWriter, r *http.Request) {
	var in subImportInput
	if !decodeConfiguration(w, r, &in) {
		return
	}
	key, err := subRawKey(in.SourceID, in.KeyID)
	if err != nil || in.GroupID <= 0 || len(in.Models)+len(in.ModelMappings) > 128 {
		http.Error(w, "请选择有效接入", 400)
		return
	}
	if _, err := importPublicModels(in.Models, in.ModelMappings); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	owner, _ := s.controlUser(r)
	var base, secret string
	err = s.control.db.QueryRowContext(ctx, `SELECT a.base,t.encrypted_routing_key FROM console_sub_accounts a JOIN console_sub_targets t
 ON t.account_id=a.id WHERE a.owner=$1 AND a.id=$2 AND t.group_id=$3 AND t.active`, owner, in.AccountID, in.GroupID).Scan(&base, &secret)
	if err != nil {
		http.Error(w, "未能确认此站点分组", 404)
		return
	}
	src, err := s.control.source(ctx, in.SourceID)
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	parts, err := partitionChannelModels(publicChannelModels(in.Models, in.ModelMappings), in.Protocols)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if secret != "" {
		secret, err = s.control.decrypt(secret)
		if err != nil {
			http.Error(w, "无法验证业务密钥", 409)
			return
		}
	}
	plan := serviceTierImportPlan{counts: map[string]int{}}
	siteSupported := map[string]int{}
	verificationUnavailable := false
	total := 0
	for index, part := range parts {
		if part.Protocol != "responses" {
			continue
		}
		total += len(part.Models)
		siteSupported, err = s.siteTierImportCounts(ctx, in.AccountID, in.GroupID, part.Models)
		if err != nil {
			http.Error(w, "未能读取站点档位检测结果", 503)
			return
		}
		name := subProviderName(in.AccountID, in.GroupID, key)
		if index > 0 {
			name = protocolProvider(name, part.Protocol)
		}
		plan, err = s.planServiceTierImport(ctx, src, name, subBindingSite(base)+part.Endpoint, secret, part.Models)
		verificationUnavailable = err != nil
	}
	writeJSON(w, 200, map[string]any{"verified": plan.counts, "site_supported": siteSupported,
		"verification_unavailable": verificationUnavailable, "total": total})
}

func importTierSettings(plan serviceTierImportPlan, in subImportInput) map[string]any {
	set := map[string]any{}
	for path, value := range plan.set {
		tier := path[strings.LastIndex(path, "/")+1:]
		if enabled, chosen := in.ServiceTierEnabled[tier]; chosen {
			set[path] = value
			set["/preferences/service_tier_disabled/"+tier] = !enabled
		}
	}
	return set
}

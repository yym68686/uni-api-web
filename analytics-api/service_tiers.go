package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const serviceTierBatchLimit = 30
const serviceTierTTL = int64(7 * 24 * 60 * 60)
const serviceTierProbePath = "/v1/channel-settings/service-tier-probe"

func isServiceTier(tier string) bool { return tier == "fast" || tier == "ultrafast" }

type serviceTierResult struct {
	Status          string `json:"status"`
	Observed        string `json:"observed,omitempty"`
	CheckedAt       int64  `json:"checked_at"`
	Binding         string `json:"binding,omitempty"`
	Scope           string `json:"scope,omitempty"`
	VerifiedAt      int64  `json:"verified_at,omitempty"`
	VerifiedBinding string `json:"verified_binding,omitempty"`
}

func serviceTierDigest(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func serviceTierFresh(at, now int64) bool { return at > 0 && at <= now && now-at < serviceTierTTL }

func serviceTierScope(src controlSource, scope string) string {
	if !serviceTierDigest(scope) {
		return ""
	}
	return tokenHash(controlTarget(src) + "\n" + scope)
}

func serviceTierAdmin(src controlSource) (controlSource, error) {
	if src.configKeyError != nil {
		return src, errors.New("来源管理员密钥不可用")
	}
	if src.ConfigKey != "" {
		src.Key = src.ConfigKey
	}
	return src, nil
}

func serviceTierGateway(ctx context.Context, src controlSource, provider, model, tier, scope string, verify bool) (serviceTierResult, error) {
	var out serviceTierResult
	payload := map[string]any{"provider": provider, "model": model, "tier": tier, "verify_only": verify, "expected_scope": scope}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, src.Base+serviceTierProbePath, bytes.NewBufferString(mustJSON(payload)))
	if err != nil {
		return out, errors.New("来源地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+src.Key)
	req.Header.Set("Content-Type", "application/json")
	// Never repeat a POST after a lost response or redirect.
	req.GetBody = nil
	client := &http.Client{Timeout: 48 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return out, errors.New("检测结果未确认")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 401, 403:
		return out, errors.New("来源密钥没有档位检测权限")
	case 409:
		return out, errors.New("渠道配置已变化，请重新检测")
	default:
		return out, errors.New("来源无法完成档位检测")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(body) > 4096 || json.Unmarshal(body, &out) != nil || !serviceTierDigest(out.Scope) ||
		(scope != "" && out.Scope != scope) {
		return serviceTierResult{}, errors.New("来源返回了无效的档位检测结果")
	}
	if verify && out.Status == "ready" && serviceTierDigest(out.Binding) {
		return out, nil
	}
	switch out.Status {
	case "supported", "not_honored", "unsupported", "inconclusive":
		if !serviceTierDigest(out.Binding) || !serviceTierFresh(out.CheckedAt, time.Now().Unix()) {
			return serviceTierResult{}, errors.New("来源返回了无效的档位检测结果")
		}
	case "not_applicable":
		return serviceTierResult{Status: out.Status, Scope: out.Scope, CheckedAt: time.Now().Unix()}, nil
	default:
		return serviceTierResult{}, errors.New("来源返回了无效的档位检测结果")
	}
	if out.Observed != "" && out.Observed != "fast" && out.Observed != "priority" && out.Observed != "ultrafast" &&
		out.Observed != "default" && out.Observed != "flex" && out.Observed != "scale" {
		return serviceTierResult{}, errors.New("来源返回了无效的实际档位")
	}
	if out.Status == "supported" && !(out.Observed == tier || tier == "fast" && out.Observed == "priority") {
		return serviceTierResult{}, errors.New("来源返回的实际档位与检测档位不符")
	}
	// Never import verification fields supplied by an upstream or compatibility proxy.
	out.VerifiedAt, out.VerifiedBinding = 0, ""
	if out.Status == "supported" {
		out.VerifiedAt, out.VerifiedBinding = out.CheckedAt, out.Binding
	}
	return out, nil
}

func (s *Service) runServiceTierCheck(ctx context.Context, src controlSource, c *configuredCheck, result *any) error {
	admin, err := serviceTierAdmin(src)
	if err != nil {
		return err
	}
	preflight, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	runtime, _, err := subGateway(preflight, admin, http.MethodGet, "/v1/observability/runtime", nil)
	if err != nil || runtime["service_tier_probe_version"] != float64(1) {
		return errors.New("此来源尚不支持档位检测，未发起上游请求")
	}
	// The captured scope fences the POST against a concurrent credential change.
	target, err := serviceTierGateway(preflight, admin, c.Provider, c.Model, c.Kind, "", true)
	if err != nil {
		return err
	}
	c.Fingerprint = serviceTierScope(src, target.Scope)
	if target.Status == "not_applicable" {
		target.Scope = ""
		*result = target
		return nil
	}
	if target.Status != "ready" {
		return errors.New("无法确认待检测渠道")
	}
	out, err := serviceTierGateway(ctx, admin, c.Provider, c.Model, c.Kind, target.Scope, false)
	if err != nil {
		return err
	}
	if out.Binding != target.Binding {
		return errors.New("渠道凭据已变化，请重新检测")
	}
	out.Scope = ""
	*result = out
	return nil
}

func (s *Service) saveServiceTierResult(ctx context.Context, c configuredCheck, run, state, message string, raw any) {
	// Do not replace or extend past evidence on transport errors, missing
	// fields or a Fast ramp returning default. A definitive denial removes it.
	_, err := s.control.db.ExecContext(ctx, `UPDATE console_configured_checks SET state=$6,message=$7,
 result=CASE
   WHEN $8::jsonb IS NULL AND ($9='' OR fingerprint=$9) THEN result
   WHEN $8::jsonb->>'status' IN ('inconclusive','not_honored') AND fingerprint=$9
        AND result->>'verified_binding'=$8::jsonb->>'binding'
   THEN $8::jsonb || jsonb_build_object('verified_at',result->'verified_at','verified_binding',result->'verified_binding')
   ELSE $8::jsonb END,
 fingerprint=CASE WHEN $9='' THEN fingerprint ELSE $9 END,updated_at=now(),deadline=NULL
 WHERE source_id=$1 AND provider=$2 AND kind=$3 AND model=$4 AND run_id=$5 AND state='running'`,
		c.Source, c.Provider, c.Kind, c.Model, run, state, message, raw, c.Fingerprint)
	if err != nil {
		log.Printf("service_tier_check save failed class=%s", databaseErrorClass(err))
	}
}

func (s *Service) publishServiceTier(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider  string `json:"provider"`
		Model     string `json:"model"`
		Tier      string `json:"tier"`
		Operation string `json:"operation_id"`
		Remove    bool   `json:"remove"`
	}
	if !decodeConfiguration(w, r, &in) {
		return
	}
	if !isServiceTier(in.Tier) || in.Provider == "" || in.Model == "" || in.Operation == "" ||
		len(in.Operation) > 128 || len(in.Model) > 256 || len(in.Provider) > 256 {
		http.Error(w, "无效档位发布请求", 400)
		return
	}
	ctx := r.Context()
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
	var raw []byte
	var fingerprint, state string
	err = s.control.db.QueryRowContext(ctx, `SELECT result,fingerprint,state FROM console_configured_checks
 WHERE source_id=$1 AND provider=$2 AND model=$3 AND kind=$4 AND source_target=$5`, src.ID, in.Provider, in.Model, in.Tier, controlTarget(src)).Scan(&raw, &fingerprint, &state)
	var result serviceTierResult
	if err != nil || state == "queued" || state == "running" || json.Unmarshal(raw, &result) != nil ||
		(!in.Remove && (!serviceTierFresh(result.VerifiedAt, time.Now().Unix()) || !serviceTierDigest(result.VerifiedBinding))) {
		http.Error(w, "没有可发布的有效档位证据，请先检测", 409)
		return
	}
	settings, code, err := s.settingsGateway(ctx, admin, "GET", "/v1/channel-settings?"+url.Values{"provider": {in.Provider}}.Encode(), nil)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	target, err := serviceTierGateway(ctx, admin, in.Provider, in.Model, in.Tier, "", true)
	if err != nil || target.Status != "ready" || serviceTierScope(src, target.Scope) != fingerprint ||
		(!in.Remove && target.Binding != result.VerifiedBinding) {
		http.Error(w, "渠道配置或凭据已变化，请重新检测", 409)
		return
	}
	revision, _ := settings["revision"].(string)
	if revision == "" {
		http.Error(w, "未能确认当前配置版本", 409)
		return
	}
	path := "/preferences/service_tier_evidence/" + target.Binding + "/" + in.Tier
	change := channelSettingChange{Provider: in.Provider}
	if in.Remove {
		change.Remove = []string{path}
	} else {
		change.Set = map[string]any{path: result.VerifiedAt}
	}
	s.applyChannelSettings(w, r, channelSettingMutation{Operation: in.Operation, Revision: revision, Changes: []channelSettingChange{change}})
}

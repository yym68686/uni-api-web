package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const channelSettingsSchema = `CREATE TABLE IF NOT EXISTS console_channel_settings_operations(
 id TEXT PRIMARY KEY,source_id TEXT NOT NULL REFERENCES console_sources(id),owner TEXT NOT NULL,
 request_hash TEXT NOT NULL,request_revision TEXT NOT NULL,status TEXT NOT NULL,
 encrypted_before TEXT NOT NULL,encrypted_intent TEXT NOT NULL,public_result JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE INDEX IF NOT EXISTS console_channel_settings_audit ON console_channel_settings_operations(source_id,created_at DESC);
 CREATE TABLE IF NOT EXISTS console_channel_setting_templates(id TEXT PRIMARY KEY,owner TEXT NOT NULL,name TEXT NOT NULL,patch JSONB NOT NULL,updated_at TIMESTAMPTZ NOT NULL DEFAULT now());`

type channelSettingChange struct {
	DeleteCopy  bool           `json:"delete_copy,omitempty"`
	CopyToKey   string         `json:"copy_to_key,omitempty"`
	CreateToKey string         `json:"create_to_key,omitempty"`
	Provider    string         `json:"provider"`
	Set         map[string]any `json:"set,omitempty"`
	Remove      []string       `json:"remove,omitempty"`
	Reset       bool           `json:"reset"`
}
type channelSettingMutation struct {
	Revision  string                 `json:"revision"`
	Operation string                 `json:"operation_id"`
	Changes   []channelSettingChange `json:"changes"`
	Sample    map[string]any         `json:"sample,omitempty"`
}

func settingsPublic(value map[string]any) map[string]any { delete(value, "intent"); return value }
func (s *Service) settingsSource(r *http.Request) (controlSource, error) {
	src, err := s.control.source(r.Context(), r.PathValue("id"))
	if err == nil && src.configKeyError != nil {
		return src, src.configKeyError
	}
	if err == nil && src.ConfigKey != "" {
		src.Key = src.ConfigKey
	}
	return src, err
}
func (s *Service) settingsGateway(ctx context.Context, src controlSource, method, path string, body any) (map[string]any, int, error) {
	var input []byte
	if body != nil {
		input, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, src.Base+path, bytes.NewReader(input))
	if err != nil {
		return nil, 502, errors.New("来源地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+src.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := sourceHTTP.Do(req)
	if err != nil {
		return nil, 502, errors.New("应用结果待确认，请通过操作记录核对")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, 502, errors.New("来源设置响应读取失败")
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil && resp.StatusCode == 200 {
		return nil, 502, errors.New("来源设置响应无效")
	}
	if resp.StatusCode != 200 {
		message := "来源拒绝设置请求"
		switch resp.StatusCode {
		case 404:
			message = "来源尚不支持渠道设置，请更新 uni-api"
		case 403, 401:
			message = "来源管理员密钥没有配置权限"
		case 409:
			message = "配置版本已变化，请刷新并核对草稿后重试"
		case 400:
			if e, ok := value["error"].(map[string]any); ok {
				if m, ok := e["message"].(string); ok && strings.HasPrefix(m, "Invalid channel setting:") {
					message = m
				}
			}
		}
		return nil, resp.StatusCode, errors.New(message)
	}
	return value, 200, nil
}
func (s *Service) channelSettings(w http.ResponseWriter, r *http.Request) {
	src, err := s.settingsSource(r)
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	path := "/v1/channel-settings?" + url.Values{"provider": {r.URL.Query().Get("provider")}}.Encode()
	value, status, err := s.settingsGateway(r.Context(), src, "GET", path, nil)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) channelSettingsSchema(w http.ResponseWriter, r *http.Request) {
	src, err := s.settingsSource(r)
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	value, status, err := s.settingsGateway(r.Context(), src, "GET", "/v1/channel-settings/schema", nil)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Service) channelSettingsSecrets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	provider, revision := r.URL.Query().Get("provider"), r.URL.Query().Get("revision")
	if provider == "" || revision == "" {
		http.Error(w, "请选择渠道并刷新设置", 400)
		return
	}
	src, err := s.settingsSource(r)
	if err != nil {
		http.Error(w, "来源管理员配置不可用", 403)
		return
	}
	path := "/v1/channel-settings/secrets?" + url.Values{"provider": {provider}, "revision": {revision}}.Encode()
	value, status, err := s.settingsGateway(r.Context(), src, "GET", path, nil)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	// Explicit projection keeps the reveal response out of mutation/audit paths.
	if value["provider"] != provider || value["revision"] != revision {
		http.Error(w, "密钥版本不一致，请刷新设置", 409)
		return
	}
	writeJSON(w, 200, map[string]any{"keys": value["keys"]})
}
func settingsInput(w http.ResponseWriter, r *http.Request) (channelSettingMutation, bool) {
	var in channelSettingMutation
	if !decodeControlLimit(w, r, &in, 2<<20) {
		return in, false
	}
	if in.Operation == "" || len(in.Operation) > 128 || len(in.Revision) > 256 || len(in.Changes) == 0 || len(in.Changes) > 100 {
		http.Error(w, "无效设置请求", 400)
		return in, false
	}
	return in, true
}
func (s *Service) channelSettingsValidate(w http.ResponseWriter, r *http.Request) {
	in, ok := settingsInput(w, r)
	if !ok {
		return
	}
	src, err := s.settingsSource(r)
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	v, status, err := s.settingsGateway(r.Context(), src, "POST", "/v1/channel-settings/validate", in)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, 200, settingsPublic(v))
}
func (s *Service) channelSettingsDiscover(w http.ResponseWriter, r *http.Request) {
	in, ok := settingsInput(w, r)
	if !ok {
		return
	}
	src, err := s.settingsSource(r)
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	result, status, err := s.settingsGateway(r.Context(), src, "POST", "/v1/channel-settings/discover", in)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Service) channelSettingsApply(w http.ResponseWriter, r *http.Request) {
	in, ok := settingsInput(w, r)
	if !ok {
		return
	}
	s.applyChannelSettings(w, r, in)
}
func (s *Service) applyChannelSettings(w http.ResponseWriter, r *http.Request, in channelSettingMutation) {
	src, err := s.settingsSource(r)
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	owner, _ := s.controlUser(r)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	unlock, err := s.control.lockControls(ctx, src.ID)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	defer unlock()
	hash := tokenHash(mustJSON(in))
	var oldHash, status string
	var oldResult []byte
	err = s.control.db.QueryRowContext(ctx, `SELECT request_hash,status,public_result FROM console_channel_settings_operations WHERE id=$1 AND source_id=$2 AND owner=$3`, in.Operation, src.ID, owner).Scan(&oldHash, &status, &oldResult)
	if err == nil {
		if oldHash != hash {
			http.Error(w, "操作编号已用于其他修改", 409)
			return
		}
		writeJSON(w, 200, map[string]any{"operation_id": in.Operation, "status": status, "result": json.RawMessage(oldResult)})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "操作状态读取失败", 503)
		return
	}
	record, err := s.control.retainedRecord(ctx, src.ID)
	if err != nil || !record.Enabled {
		http.Error(w, "请在来源设置启用保留临时配置，确保渠道设置可以重启恢复", 409)
		return
	}
	original, err := s.control.source(ctx, src.ID)
	if err != nil {
		http.Error(w, "来源读取失败", 503)
		return
	}
	state, err := s.reconcileControls(ctx, original)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if state["revision"] != in.Revision {
		http.Error(w, "配置已更新或已完成重启恢复，请刷新后重新编辑；本次修改尚未发送", 409)
		return
	}
	// Prepare and persist recoverable intent before touching the gateway.
	preview, code, err := s.settingsGateway(ctx, src, "POST", "/v1/channel-settings/validate", in)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	before, code, err := s.settingsGateway(ctx, src, "GET", "/v1/channel-settings/export", nil)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	if before["revision"] != in.Revision {
		http.Error(w, "配置版本已变化", 409)
		return
	}
	encBefore, err := s.control.encrypt(mustJSON(map[string]any{"settings": before["channel_settings"], "temporary_definitions": before["temporary_definitions"]}))
	if err != nil {
		http.Error(w, "配置加密失败", 503)
		return
	}
	encIntent, err := s.control.encrypt(mustJSON(preview["intent"]))
	if err != nil {
		http.Error(w, "配置加密失败", 503)
		return
	}
	_, err = s.control.db.ExecContext(ctx, `INSERT INTO console_channel_settings_operations(id,source_id,owner,request_hash,request_revision,status,encrypted_before,encrypted_intent,public_result) VALUES($1,$2,$3,$4,$5,'pending',$6,$7,$8)`, in.Operation, src.ID, owner, hash, in.Revision, encBefore, encIntent, mustJSON(settingsPublic(preview)))
	if err != nil {
		http.Error(w, "操作保存失败，未应用", 503)
		return
	}
	applied, code, err := s.settingsGateway(ctx, src, "PATCH", "/v1/channel-settings", in)
	if err != nil {
		if code == 400 || code == 401 || code == 403 || code == 409 {
			_, _ = s.control.db.ExecContext(ctx, `UPDATE console_channel_settings_operations SET status='rejected',updated_at=now() WHERE id=$1`, in.Operation)
		}
		writeJSON(w, 200, map[string]any{"operation_id": in.Operation, "status": map[bool]string{true: "rejected", false: "pending"}[code == 400 || code == 401 || code == 403 || code == 409], "message": err.Error()})
		return
	}
	s.finishSettingsOperation(w, r, src, in.Operation, applied)
}
func (s *Service) finishSettingsOperation(w http.ResponseWriter, r *http.Request, admin controlSource, id string, applied map[string]any) {
	// A successful settings write changes the catalog. Never show the saved
	// pre-write inventory while the periodic reader catches up.
	_, _ = s.control.db.ExecContext(r.Context(), `DELETE FROM console_channel_management_snapshots WHERE source_id=$1`, admin.ID)
	original, err := s.control.source(r.Context(), admin.ID)
	if err == nil {
		var live map[string]any
		live, _, err = s.settingsGateway(r.Context(), admin, "GET", "/v1/channel-controls", nil)
		if err == nil {
			err = s.saveLiveControls(r.Context(), original, live)
		}
	}
	if err == nil {
		if providers, e := configuredProviders(r.Context(), original); e == nil {
			_ = s.saveConfiguredInventory(r.Context(), original, providers)
		}
	}
	result := settingsPublic(applied)
	status := "applied"
	if err != nil {
		status = "applied_unretained"
		result["message"] = "运行时已应用，恢复快照未确认；请重试核对"
	}
	result["status"] = status
	_, saveErr := s.control.db.ExecContext(r.Context(), `UPDATE console_channel_settings_operations SET status=$2,public_result=$3,updated_at=now() WHERE id=$1`, id, status, mustJSON(result))
	if saveErr != nil {
		result["status"] = "pending"
		result["message"] = "运行时已应用，操作记录待确认"
	}
	writeJSON(w, 200, result)
}
func (s *Service) channelSettingsOperation(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	id := r.PathValue("operation")
	var status string
	var raw []byte
	err := s.control.db.QueryRowContext(r.Context(), `SELECT status,public_result FROM console_channel_settings_operations WHERE id=$1 AND source_id=$2 AND owner=$3`, id, r.PathValue("id"), owner).Scan(&status, &raw)
	if err != nil {
		http.Error(w, "操作不存在", 404)
		return
	}
	if status == "pending" || status == "applied_unretained" {
		src, e := s.settingsSource(r)
		if e == nil {
			unlock, e := s.control.lockControls(r.Context(), src.ID)
			if e == nil {
				defer unlock()
				remote, _, e := s.settingsGateway(r.Context(), src, "GET", "/v1/channel-settings/operations/"+url.PathEscape(id), nil)
				if e == nil {
					s.finishSettingsOperation(w, r, src, id, remote)
					return
				}
				var encrypted string
				if s.control.db.QueryRowContext(r.Context(), `SELECT encrypted_intent FROM console_channel_settings_operations WHERE id=$1`, id).Scan(&encrypted) == nil {
					if decoded, e := s.control.decrypt(encrypted); e == nil {
						var intended map[string]any
						exported, _, e := s.settingsGateway(r.Context(), src, "GET", "/v1/channel-settings/export", nil)
						if e == nil && json.Unmarshal([]byte(decoded), &intended) == nil && canonicalSettings(intended["settings"]) == canonicalSettings(exported["channel_settings"]) && canonicalSettings(intended["temporary_definitions"]) == canonicalSettings(exported["temporary_definitions"]) {
							var result map[string]any
							_ = json.Unmarshal(raw, &result)
							result["revision"] = exported["revision"]
							s.finishSettingsOperation(w, r, src, id, result)
							return
						}
					}
				}
			}
		}
	}
	writeJSON(w, 200, map[string]any{"operation_id": id, "status": status, "result": json.RawMessage(raw)})
}
func (s *Service) channelSettingsOperations(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	rows, err := s.control.db.QueryContext(r.Context(), `SELECT id,status,public_result,extract(epoch FROM created_at)::bigint FROM console_channel_settings_operations WHERE source_id=$1 AND owner=$2 ORDER BY created_at DESC LIMIT 50`, r.PathValue("id"), owner)
	if err != nil {
		http.Error(w, "审计读取失败", 503)
		return
	}
	defer rows.Close()
	data := []any{}
	for rows.Next() {
		var id, status string
		var raw []byte
		var at int64
		if err = rows.Scan(&id, &status, &raw, &at); err != nil {
			http.Error(w, "审计读取失败", 503)
			return
		}
		data = append(data, map[string]any{"id": id, "status": status, "created_at": at, "result": json.RawMessage(raw)})
	}
	writeJSON(w, 200, map[string]any{"data": data})
}
func (s *Service) channelSettingsRollback(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Operation string `json:"operation_id"`
		Revision  string `json:"revision"`
		Rollback  string `json:"rollback_id"`
	}
	if !decodeControl(w, r, &in) {
		return
	}
	owner, _ := s.controlUser(r)
	var encrypted string
	var result []byte
	err := s.control.db.QueryRowContext(r.Context(), `SELECT encrypted_before,public_result FROM console_channel_settings_operations WHERE id=$1 AND source_id=$2 AND owner=$3 AND status IN ('applied','applied_unretained')`, in.Rollback, r.PathValue("id"), owner).Scan(&encrypted, &result)
	if err != nil {
		http.Error(w, "可回滚版本不存在", 404)
		return
	}
	raw, err := s.control.decrypt(encrypted)
	if err != nil {
		http.Error(w, "版本解密失败", 503)
		return
	}
	var previous struct {
		Settings map[string]struct {
			Set    map[string]any `json:"set,omitempty"`
			Remove []string       `json:"remove,omitempty"`
		} `json:"settings"`
	}
	if json.Unmarshal([]byte(raw), &previous) != nil {
		http.Error(w, "版本无效", 503)
		return
	}
	var public struct {
		Previews []struct {
			Provider string `json:"provider"`
			Created  bool   `json:"created"`
			Deleted  bool   `json:"deleted"`
		} `json:"previews"`
	}
	if json.Unmarshal(result, &public) != nil {
		http.Error(w, "审计无效", 503)
		return
	}
	mutation := channelSettingMutation{Operation: in.Operation, Revision: in.Revision}
	for _, p := range public.Previews {
		if p.Deleted {
			http.Error(w, "删除副本不可再次回滚，请从原渠道重新复制", 409)
			return
		}
		v := previous.Settings[p.Provider]
		mutation.Changes = append(mutation.Changes, channelSettingChange{Provider: p.Provider, Reset: true, Set: v.Set, Remove: v.Remove, DeleteCopy: p.Created})
	}
	if mutation.Operation == "" || len(mutation.Changes) == 0 {
		http.Error(w, "无效回滚", 400)
		return
	}
	s.applyChannelSettings(w, r, mutation)
}
func (s *Service) channelSettingsTemplates(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	if r.Method == "POST" {
		var in struct {
			Name   string         `json:"name"`
			Set    map[string]any `json:"set,omitempty"`
			Remove []string       `json:"remove,omitempty"`
		}
		if !decodeControl(w, r, &in) {
			return
		}
		if strings.TrimSpace(in.Name) == "" || len(in.Name) > 128 {
			http.Error(w, "模板名称无效", 400)
			return
		}
		safe := func(path string) bool {
			for _, root := range []string{"/model", "/engine", "/tools", "/image", "/AUTO_RETRY", "/exclude_endpoints", "/only_request_types", "/exclude_request_types", "/exclude_request_rules", "/api_key_schedule_algorithm", "/api_key_rate_limit", "/preferences/model_timeout", "/preferences/timeout_policy", "/preferences/keepalive_interval", "/preferences/cooldown_period", "/preferences/api_key_cooldown_period", "/preferences/api_key_rate_limit_cooldown_period", "/preferences/api_key_quota_cooldown_period", "/preferences/api_key_rate_limit", "/preferences/api_key_schedule_algorithm", "/preferences/max_request_body_bytes", "/preferences/normalize_responses_custom_tool_call_ids", "/preferences/balance_query"} {
				if path == root {
					return true
				}
			}
			return false
		}
		for path, v := range in.Set {
			if !safe(path) || strings.Contains(mustJSON(v), "$secret") {
				delete(in.Set, path)
			}
		}
		removed := []string{}
		for _, path := range in.Remove {
			if safe(path) {
				removed = append(removed, path)
			}
		}
		in.Remove = removed

		id := randomID()
		_, err := s.control.db.ExecContext(r.Context(), `INSERT INTO console_channel_setting_templates(id,owner,name,patch) VALUES($1,$2,$3,$4)`, id, owner, in.Name, mustJSON(in))
		if err != nil {
			http.Error(w, "模板保存失败", 503)
			return
		}
		writeJSON(w, 200, map[string]any{"id": id})
		return
	}
	rows, err := s.control.db.QueryContext(r.Context(), `SELECT id,name,patch FROM console_channel_setting_templates WHERE owner=$1 ORDER BY updated_at DESC`, owner)
	if err != nil {
		http.Error(w, "模板读取失败", 503)
		return
	}
	defer rows.Close()
	data := []any{}
	for rows.Next() {
		var id, name string
		var raw []byte
		if rows.Scan(&id, &name, &raw) != nil {
			http.Error(w, "模板读取失败", 503)
			return
		}
		data = append(data, map[string]any{"id": id, "name": name, "patch": json.RawMessage(raw)})
	}
	writeJSON(w, 200, map[string]any{"data": data})
}

// Canonical JSON agrees with serde_json while retaining numeric lexemes and
// characters such as < in routing rules; encoding/json's HTML escaping does not.
func canonicalSettings(value any) string {
	raw, _ := json.Marshal(value)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var normalized any
	if dec.Decode(&normalized) != nil {
		return ""
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(normalized)
	return strings.TrimSuffix(out.String(), "\n")
}

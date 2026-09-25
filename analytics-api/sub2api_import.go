package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type subImportInput struct {
	AllowUnverifiedModels bool              `json:"allow_unverified_models,omitempty"`
	Positions             map[string]int    `json:"positions,omitempty"`
	ModelMappings         map[string]string `json:"model_mappings,omitempty"`
	CompactionEnabled     *bool             `json:"compaction_enabled,omitempty"`
	Action                string            `json:"action"`
	AccountID             string            `json:"account_id"`
	GroupID               int64             `json:"group_id"`
	SourceID              string            `json:"source_id"`
	KeyID                 string            `json:"api_key_id"`
	Revision              string            `json:"revision"`
	Models                []string          `json:"models"`
	Position              int               `json:"position"`
}

func subProviderName(account string, group int64, key string) string {
	return "sub2api-" + tokenHash(account + ":" + strconv.FormatInt(group, 10) + ":" + key)[:24]
}
func subRawKey(source, key string) (string, error) {
	if parts := strings.SplitN(key, "::", 2); len(parts) == 2 {
		if parts[0] != source {
			return "", errors.New("API key 不属于所选来源")
		}
		key = parts[1]
	}
	if !strings.HasPrefix(key, "key-") || len(key) != 68 {
		return "", errors.New("请选择有效 API key")
	}
	return key, nil
}
func subGateway(ctx context.Context, src controlSource, method, path string, body any) (map[string]any, int, error) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, src.Base+path, bytes.NewReader(raw))
	if err != nil {
		return nil, 502, errors.New("来源地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+src.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := sourceHTTP.Do(req)
	if err != nil {
		return nil, 502, errors.New("无法确认来源结果，请刷新后核对，勿重复提交")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		switch resp.StatusCode {
		case 404:
			return nil, 404, errors.New("来源尚不支持临时添加渠道，请更新 uni-api")
		case 409:
			return nil, 409, errors.New("渠道顺序已变化或来源已重启，请刷新弹窗后重试")
		case 401, 403:
			return nil, 403, errors.New("来源密钥无管理权限")
		case 400:
			// Gateway diagnostics are allowlisted; never echo an upstream body
			// that might contain credentials or provider configuration.
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			var problem struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(raw, &problem)
			switch problem.Error.Message {
			case "Invalid retained configuration", "Too many temporary scopes", "Too many temporary channels":
				return nil, 400, errors.New("来源拒绝配置快照，可能已达到路由规则或临时渠道数量上限；未提交本来源修改，请核对配置容量")
			case "Retained rule references an unavailable channel":
				return nil, 400, errors.New("来源路由引用的渠道或模型已变化；未提交本来源修改，请重新核对")
			}
			return nil, 400, errors.New("所选 key、模型或位置已失效，请刷新弹窗后重试")
		}
		return nil, 502, errors.New("来源未完成添加，请刷新后核对")
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	var data map[string]any
	if err != nil || len(raw) > 2<<20 || json.Unmarshal(raw, &data) != nil {
		return nil, 502, errors.New("来源返回无效状态，请刷新核对")
	}
	return data, 200, nil
}
func (s *Service) subChannelOptions(w http.ResponseWriter, r *http.Request) {
	src, err := s.control.source(r.Context(), r.URL.Query().Get("source_id"))
	if err != nil {
		http.Error(w, "请选择 uni-api 来源", 404)
		return
	}
	// The destination selector needs only opaque caller IDs and masked labels.
	// Never make it wait for channel-controls or a key's model catalog; those
	// live reads still fence every subsequent route edit with a revision.
	if r.URL.Query().Get("keys_only") == "true" {
		keys, status, err := s.channelImportKeys(r.Context(), src)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, 200, keys)
		return
	}
	state, status, err := subGateway(r.Context(), src, "GET", "/v1/channel-controls", nil)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	keys, status, err := fetchSource(r.Context(), src, "/v1/api-keys", nil)
	if err != nil {
		http.Error(w, "无法读取来源 API key", status)
		return
	}
	channels := []any{}
	if key := r.URL.Query().Get("api_key_id"); key != "" {
		key, err = subRawKey(src.ID, key)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		catalog, code, e := fetchSource(r.Context(), src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
		if e != nil {
			http.Error(w, "无法读取当前渠道顺序", code)
			return
		}
		if data, ok := catalog["data"].([]any); ok {
			replaced := configuredExcluded(state, key)
			for _, row := range data {
				item, _ := row.(map[string]any)
				provider, _ := item["provider"].(string)
				if !replaced[provider] {
					channels = append(channels, row)
				}
			}
		}
	}
	writeJSON(w, 200, map[string]any{"batch_revisions": true, "revision": state["revision"], "supported": state["temporary_channel_import"] == true, "manageable": state["temporary_channel_management"] == true, "keys": keys["data"], "channels": channels, "provider": subProviderName(r.URL.Query().Get("account_id"), int64Param(r.URL.Query().Get("group_id")), strings.TrimPrefix(r.URL.Query().Get("api_key_id"), src.ID+"::"))})
}
func (s *Service) subImportChannel(w http.ResponseWriter, r *http.Request) {
	var in subImportInput
	if !decodeControl(w, r, &in) {
		return
	}
	owner, _ := s.controlUser(r)
	publicModels, modelErr := importPublicModels(in.Models, in.ModelMappings)
	if modelErr != nil || validateModelPositions(publicModels, in.Positions) != nil || in.Position < 1 || in.Position > 1025 || len(in.Revision) > 256 {
		http.Error(w, "请选择模型和有效位置", 400)
		return
	}
	seen := map[string]bool{}
	for _, model := range importUpstreamModels(in.Models, in.ModelMappings) {
		if !validProbeModel(model) || seen[model] {
			http.Error(w, "模型无效或重复", 400)
			return
		}
		seen[model] = true
	}
	key, err := subRawKey(in.SourceID, in.KeyID)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(50 * time.Second))
	src, err := s.control.source(ctx, in.SourceID)
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	unlock, err := s.control.lockControls(ctx, src.ID)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	defer unlock()
	state, err := s.reconcileControls(ctx, src)
	status := 409
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if state["temporary_channel_import"] != true {
		http.Error(w, "该来源尚不支持临时添加渠道", 400)
		return
	}
	if state["revision"] != in.Revision {
		http.Error(w, "渠道顺序已变化，请刷新弹窗后重试", 409)
		return
	}
	// Claim account to serialize refresh-token rotation, key creation and stop.
	job := randomID()
	var base, auth string
	err = s.control.db.QueryRowContext(ctx, `UPDATE console_sub_accounts SET state='running',job_id=$3,job_kind='import',lease_until=now()+interval '90 seconds',message='' WHERE id=$1 AND owner=$2 AND state NOT IN ('running','queued') RETURNING base,encrypted_auth`, in.AccountID, owner, job).Scan(&base, &auth)
	if err != nil {
		http.Error(w, "账号不存在或有任务进行中", 409)
		return
	}
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_, _ = s.control.db.ExecContext(cleanup, `UPDATE console_sub_accounts SET state='idle',job_id='',job_kind='',lease_until=NULL WHERE id=$1 AND job_id=$2`, in.AccountID, job)
	}()
	var active bool
	var storedKey string
	err = s.control.db.QueryRowContext(ctx, `SELECT active,encrypted_routing_key FROM console_sub_targets WHERE account_id=$1 AND group_id=$2`, in.AccountID, in.GroupID).Scan(&active, &storedKey)
	if err != nil || !active {
		http.Error(w, "分组不可用，请同步后重试", 400)
		return
	}
	for _, model := range importUpstreamModels(in.Models, in.ModelMappings) {
		var success bool
		err = s.control.db.QueryRowContext(ctx, `SELECT state='done' AND result->'availability'->>'status'='success' FROM console_sub_models WHERE account_id=$1 AND group_id=$2 AND model=$3`, in.AccountID, in.GroupID, model).Scan(&success)
		if err != nil || !success {
			http.Error(w, "只能添加最近检测可用的模型，请重新检测", 400)
			return
		}
	}
	// A separate business key keeps production traffic and detection usage isolated.
	call, groups, err := s.subPanel(ctx, in.AccountID, base, job, auth)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	allowed := false
	for _, g := range groups {
		if g.ID == in.GroupID {
			allowed = true
		}
	}
	if !allowed {
		http.Error(w, "该账号已无分组权限", 400)
		return
	}
	routeName := "uni-console-route-" + in.AccountID + "-" + strconv.FormatInt(in.GroupID, 10)
	var routeKey subRemoteKey
	if storedKey != "" {
		// Read the dedicated upstream record, so revoked/disabled keys cannot be silently imported.
		var remoteID int64
		s.control.db.QueryRowContext(ctx, `SELECT routing_key_id FROM console_sub_targets WHERE account_id=$1 AND group_id=$2`, in.AccountID, in.GroupID).Scan(&remoteID)
		err = call("GET", "/api/v1/keys/"+strconv.FormatInt(remoteID, 10), nil, &routeKey, "")
	} else {
		for page := 1; page <= 100; page++ {
			var listing struct {
				Items []subRemoteKey `json:"items"`
				Pages int            `json:"pages"`
			}
			err = call("GET", "/api/v1/keys?search="+url.QueryEscape(routeName)+"&page_size=100&page="+strconv.Itoa(page), nil, &listing, "")
			if err != nil {
				break
			}
			for _, k := range listing.Items {
				if k.Name == routeName && k.GroupID == in.GroupID {
					routeKey = k
					break
				}
			}
			if routeKey.ID > 0 || len(listing.Items) < 100 || (listing.Pages > 0 && page >= listing.Pages) {
				break
			}
			if page == 100 {
				err = errors.New("业务 key 列表未读取完整")
			}
		}
		if err == nil && routeKey.ID == 0 {
			err = call("POST", "/api/v1/keys", map[string]any{"name": routeName, "group_id": in.GroupID}, &routeKey, "route-"+in.AccountID+"-"+strconv.FormatInt(in.GroupID, 10))
		}
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if routeKey.Key == "" || routeKey.GroupID != in.GroupID || routeKey.Name != routeName || routeKey.Status != "active" || (routeKey.ExpiresAt != nil && routeKey.ExpiresAt.Before(time.Now())) {
		http.Error(w, "专用业务 key 已失效，请在上游恢复后重试", 400)
		return
	}
	encrypted, err := s.control.encrypt(routeKey.Key)
	if err != nil {
		http.Error(w, "业务 key 保存失败", 503)
		return
	}
	result, err := s.control.db.ExecContext(ctx, `UPDATE console_sub_targets SET encrypted_routing_key=$3,routing_key_id=$4 WHERE account_id=$1 AND group_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$5)`, in.AccountID, in.GroupID, encrypted, routeKey.ID, job)
	if err != nil {
		http.Error(w, "业务 key 保存失败", 503)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		http.Error(w, "操作已停止，请刷新核对", 409)
		return
	}
	provider := subProviderName(in.AccountID, in.GroupID, key)
	var applied map[string]any
	if in.CompactionEnabled != nil || len(in.ModelMappings) > 0 || len(in.Positions) > 0 {
		applied, status, err = s.subImportCompactionChannel(ctx, src, state, in, key, provider, base, routeKey.Key)
	} else {
		applied, status, err = subGateway(ctx, src, "POST", "/v1/temporary-channels", map[string]any{"revision": in.Revision, "api_key_id": key, "provider": provider, "base_url": base + "/v1/responses", "api_key": routeKey.Key, "models": in.Models, "position": in.Position})
	}
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if e := s.saveLiveControls(ctx, src, applied); e != nil {
		retentionFailure(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"provider": provider, "models": publicModels, "position": in.Position, "message": fmt.Sprintf("已临时添加至第 %d 位", in.Position)})
}

func int64Param(v string) int64 { n, _ := strconv.ParseInt(v, 10, 64); return n }

// The existing atomic snapshot API installs the new channel, request-type
// policy and priority together. Never briefly expose a channel before its
// compaction exclusion is applied. The caller holds the source lock; both
// export and final apply must match the same revision.
func (s *Service) subImportCompactionChannel(ctx context.Context, src controlSource, state map[string]any, in subImportInput, key, provider, base, upstreamKey string) (map[string]any, int, error) {
	public, err := importPublicModels(in.Models, in.ModelMappings)
	if err != nil {
		return nil, 400, err
	}
	snapshot, code, err := s.importSnapshot(ctx, src, state, in.Revision)
	if err != nil {
		return nil, code, err
	}
	for _, p := range snapshot.Channels {
		if p.Provider == provider {
			return nil, 409, errors.New("渠道已存在，请使用编辑")
		}
	}
	excluded := []string{}
	if in.CompactionEnabled != nil && !*in.CompactionEnabled {
		excluded = append(excluded, "compaction")
	}
	definition, _ := json.Marshal(map[string]any{"provider": provider, "base_url": base + "/v1/responses", "engine": "gpt", "api": []string{upstreamKey}, "model": importModelDefinition(in.Models, in.ModelMappings), "exclude_request_types": excluded})
	return s.applyImportSnapshot(ctx, src, snapshot, in.Revision, retainedChannel{Provider: provider, KeyID: key, Base: base + "/v1/responses", Key: upstreamKey, Models: public, Definition: definition}, in.Position, in.Positions)
}

func (s *Service) importSnapshot(ctx context.Context, src controlSource, state map[string]any, revision string) (retainedSnapshot, int, error) {
	if state["temporary_channel_restore"] != true || state["channel_settings"] != true {
		return retainedSnapshot{}, 400, errors.New("来源尚不支持带请求规则的渠道添加，请更新 uni-api")
	}
	admin := src
	if src.ConfigKey != "" {
		admin.Key = src.ConfigKey
	}
	exported, code, err := s.settingsGateway(ctx, admin, "GET", "/v1/channel-settings/export", nil)
	if err != nil {
		return retainedSnapshot{}, code, err
	}
	if exported["revision"] != revision {
		return retainedSnapshot{}, 409, errors.New("渠道配置已变化，请刷新后重试")
	}
	var live retainedLive
	var definitions map[string]json.RawMessage
	snapshot := retainedSnapshot{Version: 2, Channels: []retainedChannel{}}
	if decodeMap(state, &live) != nil || decodeMap(exported["temporary_definitions"], &definitions) != nil || definitions == nil || decodeMap(exported["channel_settings"], &snapshot.Settings) != nil || snapshot.Settings == nil {
		return retainedSnapshot{}, 502, errors.New("来源配置快照无效")
	}
	snapshot.Rules = append([]retainedRule{}, live.Rules...)
	// Legacy temporary channels without advanced settings are intentionally
	// absent from the gateway's definition export. Reuse only a retained snapshot
	// proven to describe this exact live revision and source credential.
	var retained map[string]retainedChannel
	for _, p := range live.Channels {
		raw, ok := definitions[p.Provider]
		if !ok {
			if retained == nil {
				record, e := s.control.retainedRecord(ctx, src.ID)
				if e != nil || record.Revision != revision || record.Target != controlTarget(src) {
					return retainedSnapshot{}, 409, errors.New("请先启用并同步保留临时配置，再添加带压缩规则的渠道")
				}
				saved, e := s.control.retainedSnapshot(record)
				if e != nil {
					return retainedSnapshot{}, 503, errors.New("已保存的渠道配置暂不可用")
				}
				retained = map[string]retainedChannel{}
				for _, c := range saved.Channels {
					retained[c.Provider] = c
				}
			}
			c, exists := retained[p.Provider]
			if !exists || c.KeyID != p.KeyID || c.Key == "" {
				return retainedSnapshot{}, 502, errors.New("来源未提供完整渠道定义，未添加")
			}
			c.Models = p.Models
			snapshot.Channels = append(snapshot.Channels, c)
			continue
		}
		var doc struct {
			Base string `json:"base_url"`
			API  any    `json:"api"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			return retainedSnapshot{}, 502, errors.New("来源渠道定义无效")
		}
		keys := providerKeys(doc.API)
		secret := "__full_definition__"
		if len(keys) > 0 {
			secret = keys[0]
		}
		snapshot.Channels = append(snapshot.Channels, retainedChannel{Provider: p.Provider, KeyID: p.KeyID, Base: doc.Base, Key: secret, Models: p.Models, Definition: raw})
	}
	return snapshot, 200, nil
}

func (s *Service) applyImportSnapshot(ctx context.Context, src controlSource, snapshot retainedSnapshot, revision string, channel retainedChannel, position int, positions map[string]int, suppress ...string) (map[string]any, int, error) {
	if err := validateModelPositions(channel.Models, positions); err != nil {
		return nil, 400, err
	}
	replaced := snapshotExcluded(snapshot.Rules, snapshot.Channels, channel.KeyID)
	for _, provider := range suppress {
		if provider != "" {
			replaced[provider] = true
		}
	}
	// Prune only models no longer offered by this copy. Keep disabled policies
	// and broader ordering scopes for models that remain in service.
	kept := map[string]bool{}
	for _, model := range channel.Models {
		kept[model] = true
	}
	for i := range snapshot.Rules {
		r := &snapshot.Rules[i]
		if r.Model != "" && !kept[r.Model] {
			r.Order = withoutProvider(r.Order, channel.Provider)
			r.Disabled = withoutProvider(r.Disabled, channel.Provider)
		}
	}
	next := snapshot.Channels[:0]
	for _, old := range snapshot.Channels {
		if old.Provider != channel.Provider {
			next = append(next, old)
		}
	}
	snapshot.Channels = next
	delete(snapshot.Settings, channel.Provider)
	catalog, code, err := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {channel.KeyID}, "endpoint": {"all"}, "stream": {"all"}})
	if err != nil {
		return nil, code, err
	}
	var channels []struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if decodeMap(catalog["data"], &channels) != nil {
		return nil, 502, errors.New("渠道顺序无效")
	}
	for _, model := range channel.Models {
		modelPosition := position
		if p, ok := positions[model]; ok {
			modelPosition = p
		}
		order := []string{}
		seen := map[string]bool{}
		for _, c := range channels {
			if c.Model == model && c.Provider != channel.Provider && !replaced[c.Provider] && !seen[c.Provider] {
				order = append(order, c.Provider)
				seen[c.Provider] = true
			}
		}
		if modelPosition < 1 || modelPosition > len(order)+1 {
			return nil, 400, errors.New("添加位置已失效，请刷新后重试")
		}
		order = append(order, "")
		copy(order[modelPosition:], order[modelPosition-1:])
		order[modelPosition-1] = channel.Provider
		found := false
		for i := range snapshot.Rules {
			r := &snapshot.Rules[i]
			if r.KeyID == channel.KeyID && r.Model == model {
				r.Order = order
				found = true
				break
			}
		}
		if !found {
			snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: channel.KeyID, Model: model, Order: order, Disabled: []string{}})
		}
	}
	snapshot.Channels = append(snapshot.Channels, channel)
	admin := src
	if src.ConfigKey != "" {
		admin.Key = src.ConfigKey
	}
	return subGateway(ctx, admin, "POST", "/v1/channel-controls/restore", map[string]any{"revision": revision, "snapshot": snapshot})
}

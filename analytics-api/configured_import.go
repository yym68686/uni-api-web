package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func configuredImportName(provider, key string) string {
	return "sub2api-copy-" + tokenHash("channel-import\n" + provider + "\n" + key)[:20]
}

// A native channel edited for one caller is replaced by a key-owned copy.
// The native definition remains available to all other callers. Both changes
// live in the same atomic gateway snapshot and survive retention/restore.
func configuredReplacements(state map[string]any, key string) map[string]bool {
	var live retainedLive
	_ = decodeMap(state, &live)
	return snapshotReplacements(live.Rules, live.Channels, key)
}
func snapshotReplacements(rules []retainedRule, channels []retainedChannel, key string) map[string]bool {
	copies := map[string]bool{}
	for _, c := range channels {
		if c.KeyID == key {
			copies[c.Provider] = true
		}
	}
	replaced := map[string]bool{}
	for _, r := range rules {
		if r.KeyID == key && r.Model == "" {
			for _, provider := range r.Disabled {
				if copies[configuredImportName(provider, key)] {
					replaced[provider] = true
				}
			}
		}
	}
	return replaced
}

// Reconstruct the effective provider on the server. Full credentials are never
// sent to the browser; aliases preserve engine, headers, limits and other rules.
func (s *Service) importProviderDocument(ctx context.Context, src controlSource, snapshot retainedSnapshot, provider, revision string, baseDocuments ...map[string]map[string]any) (map[string]any, int, error) {
	admin := src
	if src.ConfigKey != "" {
		admin.Key = src.ConfigKey
	}
	var document map[string]any
	for _, channel := range snapshot.Channels {
		if channel.Provider == provider {
			if len(channel.Definition) > 0 {
				if json.Unmarshal(channel.Definition, &document) != nil {
					return nil, 502, errors.New("渠道定义无效")
				}
			} else {
				document = map[string]any{"provider": provider, "base_url": channel.Base, "api": channel.Key, "engine": "gpt", "model": channel.Models}
			}
			break
		}
	}
	if document == nil && len(baseDocuments) > 0 {
		// Clone before applying key-specific settings/model changes.
		raw, _ := json.Marshal(baseDocuments[0][provider])
		_ = json.Unmarshal(raw, &document)
		if document == nil {
			return nil, 404, errors.New("渠道已不存在")
		}
	}
	if document == nil {
		raw, code, err := subGateway(ctx, admin, "GET", "/v1/api_config", nil)
		if err != nil {
			return nil, code, err
		}
		var cfg struct {
			Providers []map[string]any `json:"providers"`
		}
		if decodeMap(raw["api_config"], &cfg) != nil {
			return nil, 502, errors.New("来源配置无效")
		}
		for _, p := range cfg.Providers {
			if p["provider"] == provider {
				document = p
				break
			}
		}
	}
	if document == nil {
		return nil, 404, errors.New("渠道已不存在")
	}
	if raw := snapshot.Settings[provider]; len(raw) > 0 {
		var change struct {
			Set    map[string]any `json:"set"`
			Remove []string       `json:"remove"`
		}
		if json.Unmarshal(raw, &change) != nil {
			return nil, 502, errors.New("渠道覆盖配置无效")
		}
		for path, value := range change.Set {
			if err := setImportPath(document, path, value, false); err != nil {
				return nil, 502, err
			}
		}
		for _, path := range change.Remove {
			if err := setImportPath(document, path, nil, true); err != nil {
				return nil, 502, err
			}
		}
	}
	// Final write checks the same revision, so a concurrent edit cannot silently
	// pair an old definition with a new route order.
	return document, 200, nil
}
func setImportPath(document map[string]any, path string, value any, remove bool) error {
	if !strings.HasPrefix(path, "/") {
		return errors.New("配置路径无效")
	}
	parts := strings.Split(path[1:], "/")
	var current any = document
	for i, raw := range parts {
		key := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch node := current.(type) {
		case map[string]any:
			if i == len(parts)-1 {
				if remove {
					delete(node, key)
				} else {
					node[key] = value
				}
				return nil
			}
			child, ok := node[key]
			if !ok {
				if remove {
					return nil
				}
				child = map[string]any{}
				node[key] = child
			}
			current = child
		case []any:
			n, err := strconv.Atoi(key)
			if err != nil || n < 0 || n >= len(node) {
				return errors.New("配置数组路径无效")
			}
			if i == len(parts)-1 {
				if remove {
					return errors.New("不支持删除配置数组子项")
				}
				node[n] = value
				return nil
			}
			current = node[n]
		default:
			return errors.New("配置路径类型无效")
		}
	}
	return nil
}

func (s *Service) configuredImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		subImportInput
		Provider     string `json:"provider"`
		EditProvider string `json:"edit_provider"`
	}
	if !decodeControlLimit(w, r, &in, 512<<10) {
		return
	}
	public, err := importPublicModels(in.Models, in.ModelMappings)
	if err != nil || validateModelPositions(public, in.Positions) != nil || in.Provider == "" || in.Position < 1 || in.Position > 1025 {
		http.Error(w, "请选择模型、对外名称及有效位置", 400)
		return
	}
	key, err := subRawKey(in.SourceID, in.KeyID)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
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
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	snapshot, code, err := s.importSnapshot(ctx, src, state, in.Revision)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	provider := configuredImportName(in.Provider, key)
	documentProvider := in.Provider
	editingNative := false
	if in.EditProvider != "" {
		if in.EditProvider != provider && in.EditProvider != in.Provider {
			http.Error(w, "编辑渠道不属于所选来源", 400)
			return
		}
		found := false
		for _, c := range snapshot.Channels {
			if c.Provider == in.EditProvider && c.KeyID == key {
				found = true
			}
		}
		if !found && in.EditProvider == in.Provider {
			catalog, _, e := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
			var rows []struct {
				Provider string `json:"provider"`
			}
			if e != nil || decodeMap(catalog["data"], &rows) != nil {
				http.Error(w, "当前 API key 路由读取失败", 503)
				return
			}
			for _, row := range rows {
				if row.Provider == in.Provider {
					editingNative = true
				}
			}
			for _, c := range snapshot.Channels {
				if c.Provider == provider {
					http.Error(w, "此 API key 已有独立模型配置，请刷新后编辑该配置", 409)
					return
				}
			}
		}
		if !found && !editingNative {
			http.Error(w, "该 API key 的专用渠道已变化，请刷新后编辑", 409)
			return
		}
		if found {
			provider = in.EditProvider
			documentProvider = provider
		}
	}
	document, code, err := s.importProviderDocument(ctx, src, snapshot, documentProvider, in.Revision)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	catalog, _, err := fetchSource(ctx, src, "/v1/model-channels", url.Values{"endpoint": {"all"}, "stream": {"all"}})
	var rows []struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Upstream string `json:"upstream_model"`
	}
	if err != nil || decodeMap(catalog["data"], &rows) != nil {
		http.Error(w, "来源模型读取失败", 503)
		return
	}
	available := map[string]string{}
	for _, row := range rows {
		if row.Provider == in.Provider {
			up := row.Upstream
			if up == "" {
				up = row.Model
			}
			available[row.Model] = up
		}
	}
	if in.EditProvider != "" {
		for _, row := range rows {
			if row.Provider == in.EditProvider {
				up := row.Upstream
				if up == "" {
					up = row.Model
				}
				if _, ok := available[up]; !ok {
					available[up] = up
				}
			}
		}
	}
	for _, model := range importUpstreamModels(in.Models, in.ModelMappings) {
		if _, ok := available[model]; !ok {
			// A successful probe may cover a model absent from the initial
			// configuration. Validate its evidence below before extending the copy.
			if validPublicModel(model) && validProbeModel(model) {
				available[model] = model
				continue
			}
			http.Error(w, "所选上游模型已不存在，请刷新重试", 400)
			return
		}
	}
	// Resolve through the source's existing mapping, so aliasing an alias still
	// sends the real upstream model rather than the previous public name.
	resolved := map[string]string{}
	for _, m := range in.Models {
		resolved[m] = available[m]
	}
	for alias, m := range in.ModelMappings {
		resolved[alias] = available[m]
	}
	current := map[string]string{}
	if in.EditProvider != "" {
		keyCatalog, _, e := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
		var existing []batchCatalogRow
		if e != nil || decodeMap(keyCatalog["data"], &existing) != nil {
			http.Error(w, "当前路由读取失败", 503)
			return
		}
		for _, row := range existing {
			if row.Provider == in.EditProvider {
				up := row.Upstream
				if up == "" {
					up = row.Model
				}
				current[row.Model] = up
			}
		}
	}
	owner, _ := s.controlUser(r)
	if err = s.validateConfiguredModelChanges(ctx, src, in.Provider, owner, current, resolved); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	document["provider"] = provider
	document["model"] = importModelDefinition(nil, resolved)
	raw, _ := json.Marshal(document)
	base, _ := document["base_url"].(string)
	secret := "__full_definition__"
	if keys := providerKeys(document["api"]); len(keys) > 0 {
		secret = keys[0]
	}
	if editingNative {
		found := false
		for i := range snapshot.Rules {
			rule := &snapshot.Rules[i]
			if rule.KeyID == key && rule.Model == "" {
				rule.Disabled = append(withoutProvider(rule.Disabled, in.Provider), in.Provider)
				found = true
			}
		}
		if !found {
			snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: key, Order: []string{}, Disabled: []string{in.Provider}})
		}
	}
	suppressed := ""
	if editingNative {
		suppressed = in.Provider
	}
	applied, code, err := s.applyImportSnapshot(ctx, src, snapshot, in.Revision, retainedChannel{Provider: provider, KeyID: key, Base: base, Key: secret, Models: public, Definition: raw}, in.Position, in.Positions, suppressed)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	if err = s.saveLiveControls(ctx, src, applied); err != nil {
		retentionFailure(w, err)
		return
	}
	message := "模型映射已添加到所选 API key"
	if in.EditProvider != "" {
		message = "模型与各自路由位置已更新"
	}
	writeJSON(w, 200, map[string]any{"provider": provider, "message": message, "revision": applied["revision"]})
}

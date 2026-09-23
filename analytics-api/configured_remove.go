package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// Key-wide exclusions remove native membership without editing the shared
// provider definition. Temporary copies are owned by exactly one caller key.
func removeConfiguredFromSnapshot(snapshot *retainedSnapshot, key, provider string, native bool) error {
	for i, channel := range snapshot.Channels {
		if channel.Provider != provider {
			continue
		}
		if channel.KeyID != key {
			return errors.New("渠道不属于所选 API key")
		}
		snapshot.Channels = append(snapshot.Channels[:i], snapshot.Channels[i+1:]...)
		delete(snapshot.Settings, provider)
		for i := range snapshot.Rules {
			rule := &snapshot.Rules[i]
			rule.Order = withoutProvider(rule.Order, provider)
			rule.Disabled = withoutProvider(rule.Disabled, provider)
		}
		return nil
	}
	if !native {
		return errors.New("渠道已变化，请刷新后重试")
	}
	for i := range snapshot.Rules {
		rule := &snapshot.Rules[i]
		if rule.KeyID == key && rule.Model == "" {
			rule.Disabled = append(withoutProvider(rule.Disabled, provider), provider)
			return nil
		}
	}
	snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: key, Order: []string{}, Disabled: []string{provider}})
	return nil
}

func configuredExcluded(state map[string]any, key string) map[string]bool {
	var live retainedLive
	_ = decodeMap(state, &live)
	return snapshotExcluded(live.Rules, live.Channels, key)
}
func snapshotExcluded(rules []retainedRule, channels []retainedChannel, key string) map[string]bool {
	excluded := snapshotReplacements(rules, channels, key)
	for _, rule := range rules {
		if rule.KeyID == key && rule.Model == "" {
			for _, provider := range rule.Disabled {
				excluded[provider] = true
			}
		}
	}
	return excluded
}

func (s *Service) removeConfiguredBinding(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source   string `json:"source_id"`
		Key      string `json:"api_key_id"`
		Provider string `json:"provider"`
		Revision string `json:"revision"`
	}
	if !decodeControl(w, r, &in) {
		return
	}
	if in.Provider == "" || len(in.Provider) > 256 || in.Revision == "" {
		http.Error(w, "请选择渠道和有效配置版本", 400)
		return
	}
	key, err := subRawKey(in.Source, in.Key)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	src, err := s.control.source(ctx, in.Source)
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
	catalog, _, err := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
	var rows []routeMove
	if err != nil || decodeMap(catalog["data"], &rows) != nil {
		http.Error(w, "当前 API key 路由读取失败", 503)
		return
	}
	found := false
	for _, row := range rows {
		if row.Provider == in.Provider {
			found = true
		}
	}
	if !found || configuredExcluded(state, key)[in.Provider] {
		http.Error(w, "渠道已不在所选 API key，请刷新后重试", 409)
		return
	}
	if err = removeConfiguredFromSnapshot(&snapshot, key, in.Provider, true); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	admin := src
	if src.ConfigKey != "" {
		admin.Key = src.ConfigKey
	}
	applied, code, err := subGateway(ctx, admin, "POST", "/v1/channel-controls/restore", map[string]any{"revision": in.Revision, "snapshot": snapshot})
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	if err = s.saveLiveControls(ctx, src, applied); err != nil {
		retentionFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"message": "已从当前 API key 移除渠道"})
}

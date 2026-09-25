package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type configuredProvider struct {
	Temporary       bool   `json:"temporary,omitempty"`
	IdentityChanged bool   `json:"identity_changed,omitempty"`
	Provider        string `json:"provider"`
	Base            string `json:"base_url"`
	API             any    `json:"api"`
}

func configuredProviders(ctx context.Context, src controlSource) ([]configuredProvider, error) {
	return effectiveProviders(ctx, src, false)
}

// Diagnostics must target the actual installed channel, including temporary
// key-owned copies. Inventory/binding discovery keeps its original filtering.
func effectiveProviders(ctx context.Context, src controlSource, includeTemporary bool) ([]configuredProvider, error) {
	if src.configKeyError != nil {
		return nil, src.configKeyError
	}
	// Configuration access needs an explicit administrator on gateways where
	// the first key only grants catalog inspection and temporary controls.
	if src.ConfigKey != "" {
		src.Key = src.ConfigKey
	}
	effective, code, e := subGateway(ctx, src, "GET", "/v1/channel-settings/providers", nil)
	if e == nil {
		if _, ok := effective["providers"]; ok {
			var providers []configuredProvider
			if e = decodeMap(effective["providers"], &providers); e == nil {
				filtered := providers[:0]
				for _, p := range providers {
					if includeTemporary || !p.Temporary || p.IdentityChanged {
						filtered = append(filtered, p)
					}
				}
				return filtered, nil
			}
			return nil, e
		}
	}
	if e != nil && code != 404 {
		return nil, e
	}
	raw, _, err := subGateway(ctx, src, "GET", "/v1/api_config", nil)
	if err != nil {
		return nil, err
	}
	var config struct {
		Providers []configuredProvider `json:"providers"`
	}
	if err = decodeMap(raw["api_config"], &config); err != nil {
		return nil, err
	}
	return config.Providers, nil
}
func dashboardURL(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return ""
	}
	u.Path = "/dashboard"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func providerKeys(value any) []string {
	keys := []string{}
	if key, ok := value.(string); ok && strings.TrimSpace(key) != "" {
		keys = append(keys, key)
	}
	if values, ok := value.([]any); ok {
		for _, item := range values {
			if key, ok := item.(string); ok && strings.TrimSpace(key) != "" {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// Only public site links leave this endpoint. The upstream admin configuration
// (including unrelated providers and keys) is never forwarded to the browser.
func (s *Service) channelSites(w http.ResponseWriter, r *http.Request) {
	sources, err := s.control.listSources(r.Context())
	if err != nil {
		http.Error(w, "站点地址暂不可用", 503)
		return
	}
	type site struct {
		Source   string `json:"source_id"`
		Provider string `json:"provider"`
		URL      string `json:"dashboard_url"`
	}
	data := []site{}
	unavailable := []string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, source := range sources {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-r.Context().Done():
				return
			}
			defer func() { <-slots }()
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			src, e := s.control.source(ctx, id)
			var providers []configuredProvider
			if e == nil {
				providers, e = configuredProviders(ctx, src)
			}
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				unavailable = append(unavailable, id)
				return
			}
			for _, p := range providers {
				if link := dashboardURL(p.Base); link != "" {
					data = append(data, site{id, p.Provider, link})
				}
			}
		}(source.ID)
	}
	wg.Wait()
	writeJSON(w, 200, map[string]any{"data": data, "unavailable_sources": unavailable})
}

func (s *Service) channelInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		http.Error(w, "请选择渠道", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	src, err := s.control.source(ctx, r.PathValue("id"))
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	reveal := r.URL.Query().Get("reveal") == "true"
	respond := func(base string, keys []string) {
		out := map[string]any{"dashboard_url": dashboardURL(base)}
		if reveal {
			out["api_keys"] = keys
		}
		writeJSON(w, 200, out)
	}
	if strings.HasPrefix(provider, "sub2api-") {
		admin := src
		if admin.ConfigKey != "" {
			admin.Key = admin.ConfigKey
		}
		if raw, _, e := subGateway(ctx, admin, "GET", "/v1/channel-settings/providers", nil); e == nil {
			var providers []configuredProvider
			if decodeMap(raw["providers"], &providers) == nil {
				for _, p := range providers {
					if p.Provider == provider {
						respond(p.Base, providerKeys(p.API))
						return
					}
				}
			}
		}
		owner, _ := s.controlUser(r)
		refs, e := s.control.subChannelRefs(ctx, owner)
		if e != nil {
			http.Error(w, "渠道信息暂不可用", 503)
			return
		}
		raw, _, e := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
		var state subGatewayControls
		if e != nil || decodeMap(raw, &state) != nil {
			http.Error(w, "无法确认当前渠道绑定", 503)
			return
		}
		for _, p := range state.Temporary {
			if p.Provider != provider {
				continue
			}
			for _, ref := range refs {
				if subProviderName(ref.Account, ref.Group, p.KeyID) != provider {
					continue
				}
				if !reveal {
					respond(ref.Base, nil)
					return
				}
				// Prefer the exact retained credential of this installed channel.
				record, e := s.control.retainedRecord(ctx, src.ID)
				if e == nil && record.Encrypted != "" && record.Target == controlTarget(src) {
					snapshot, e := s.control.retainedSnapshot(record)
					if e == nil {
						for _, saved := range snapshot.Channels {
							if saved.Provider == provider && saved.KeyID == p.KeyID && saved.Key != "" {
								respond(saved.Base, []string{saved.Key})
								return
							}
						}
					}
				}
				var encrypted string
				e = s.control.db.QueryRowContext(ctx, `SELECT encrypted_routing_key FROM console_sub_targets WHERE account_id=$1 AND group_id=$2`, ref.Account, ref.Group).Scan(&encrypted)
				if e != nil || encrypted == "" {
					http.Error(w, "该渠道的业务 key 暂不可用", 404)
					return
				}
				key, e := s.control.decrypt(encrypted)
				if e != nil {
					http.Error(w, "渠道 key 读取失败", 503)
					return
				}
				respond(ref.Base, []string{key})
				return
			}
		}
		http.Error(w, "渠道不存在或不属于当前账号", 404)
		return
	}
	providers, err := configuredProviders(ctx, src)
	if err != nil {
		http.Error(w, "无法读取渠道配置，请在来源设置中配置有效的配置读取管理员密钥", 503)
		return
	}
	for _, p := range providers {
		if p.Provider == provider {
			respond(p.Base, providerKeys(p.API))
			return
		}
	}
	http.Error(w, "渠道不存在", 404)
}

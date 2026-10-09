package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
)

type subChannelRef struct {
	Account string
	Group   int64
	Name    string
	Base    string
	Rate    *float64
}
type subInstalledChannel struct {
	Protocol         string            `json:"protocol,omitempty"`
	Engine           string            `json:"engine,omitempty"`
	ModelMappings    map[string]string `json:"model_mappings,omitempty"`
	Fingerprint      string            `json:"-"`
	Kind             string            `json:"kind,omitempty"`
	BindingStatus    string            `json:"binding_status,omitempty"`
	BindingCheckedAt int64             `json:"binding_checked_at,omitempty"`
	BoundKeys        []subBoundKey     `json:"bound_keys,omitempty"`
	Base             string            `json:"base"`
	AccountID        string            `json:"account_id"`
	GroupID          int64             `json:"group_id"`
	SourceID         string            `json:"source_id"`
	SourceName       string            `json:"source_name"`
	KeyID            string            `json:"api_key_id"`
	KeyPosition      int               `json:"key_position"`
	KeyPrefix        string            `json:"key_prefix"`
	Provider         string            `json:"provider"`
	Name             string            `json:"name"`
	Models           []string          `json:"models"`
	Positions        map[string]int    `json:"positions"`
	Revision         string            `json:"revision"`
	Manageable       bool              `json:"manageable"`
}
type subGatewayControls struct {
	Revision   string `json:"revision"`
	Manageable bool   `json:"temporary_channel_management"`
	Temporary  []struct {
		Provider        string   `json:"provider"`
		IdentityChanged bool     `json:"identity_changed"`
		KeyID           string   `json:"api_key_id"`
		Models          []string `json:"models"`
	} `json:"temporary_channels"`
	Rules []struct {
		KeyID string   `json:"api_key_id"`
		Model string   `json:"model"`
		Order []string `json:"order"`
	} `json:"rules"`
}

func decodeMap(value any, out any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	return json.Unmarshal(raw, out)
}

func subChannelDisplayName(account string, billing []byte) string {
	var b subBilling
	rate := "未知倍率"
	if json.Unmarshal(billing, &b) == nil && b.Rate != nil {
		rate = strconv.FormatFloat(*b.Rate, 'f', -1, 64)
	}
	return account + "-" + rate
}

func (s *controlStore) subChannelRefs(ctx context.Context, owner string) ([]subChannelRef, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT a.id,t.group_id,a.name,t.billing,a.base FROM console_sub_accounts a JOIN console_sub_targets t ON t.account_id=a.id WHERE a.owner=$1 ORDER BY a.created_at,t.group_id`, owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	refs := []subChannelRef{}
	for rows.Next() {
		var ref subChannelRef
		var billing []byte
		if e = rows.Scan(&ref.Account, &ref.Group, &ref.Name, &billing, &ref.Base); e != nil {
			return nil, e
		}
		ref.Name = subChannelDisplayName(ref.Name, billing)
		var b subBilling
		if json.Unmarshal(billing, &b) == nil && b.Rate != nil && *b.Rate >= 0 && !math.IsNaN(*b.Rate) && !math.IsInf(*b.Rate, 0) {
			ref.Rate = b.Rate
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
func (s *Service) subInstalledChannels(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	refs, e := s.control.subChannelRefs(r.Context(), owner)
	if e != nil {
		http.Error(w, "渠道关联读取失败", 503)
		return
	}
	sources, e := s.control.listSources(r.Context())
	if e != nil {
		http.Error(w, "来源列表读取失败", 503)
		return
	}
	type sourceResult struct {
		Data   []subInstalledChannel
		Labels map[string]string
		Error  string
	}
	results := make([]sourceResult, len(sources))
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, source := range sources {
		wg.Add(1)
		go func(i int, source sourceView) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-r.Context().Done():
				return
			}
			defer func() { <-slots }()
			src, err := s.control.source(r.Context(), source.ID)
			if err != nil {
				results[i].Error = source.Name
				return
			}
			if s.skipUnavailableObservation(src) {
				results[i].Error = source.Name
				return
			}
			var stateMap, keysMap, catalogMap map[string]any
			var stateErr, keysErr error
			var readers sync.WaitGroup
			readers.Add(3)
			go func() {
				defer readers.Done()
				stateMap, _, stateErr = subGateway(r.Context(), src, "GET", "/v1/channel-controls", nil)
			}()
			go func() { defer readers.Done(); keysMap, _, keysErr = fetchSource(r.Context(), src, "/v1/api-keys", nil) }()
			go func() {
				defer readers.Done()
				catalogMap, _, _ = fetchSource(r.Context(), src, "/v1/model-channels", url.Values{"endpoint": {"all"}, "stream": {"all"}})
			}()
			readers.Wait()
			s.observeSourceFailure(r.Context(), src, stateErr)
			s.observeSourceFailure(r.Context(), src, keysErr)
			if stateErr != nil || keysErr != nil {
				results[i].Error = source.Name
				return
			}
			var state subGatewayControls
			var keys []struct {
				ID       string `json:"key_id"`
				Prefix   string `json:"prefix"`
				Position int    `json:"position"`
			}
			if decodeMap(stateMap, &state) != nil || decodeMap(keysMap["data"], &keys) != nil {
				results[i].Error = source.Name
				return
			}
			var modelRows []struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Upstream string `json:"upstream_model"`
				Engine   string `json:"engine"`
			}
			_ = decodeMap(catalogMap["data"], &modelRows)
			mappings := map[string]map[string]string{}
			engines := map[string]string{}
			for _, row := range modelRows {
				engines[row.Provider] = row.Engine
				if row.Upstream != "" && row.Upstream != row.Model {
					if mappings[row.Provider] == nil {
						mappings[row.Provider] = map[string]string{}
					}
					mappings[row.Provider][row.Model] = row.Upstream
				}
			}
			labels := map[string]string{}
			lookup := map[string]subChannelRef{}
			keyLabels := map[string]struct {
				Prefix   string
				Position int
			}{}
			for _, key := range keys {
				keyLabels[key.ID] = struct {
					Prefix   string
					Position int
				}{key.Prefix, key.Position}
				for _, ref := range refs {
					for _, provider := range subProviderNames(ref.Account, ref.Group, key.ID) {
						lookup[provider] = ref
					}
				}
			}
			results[i].Labels = labels
			for _, p := range state.Temporary {
				if p.IdentityChanged {
					continue
				}
				ref, ok := lookup[p.Provider]
				if !ok {
					continue
				}
				labels[p.Provider] = ref.Name
				positions := map[string]int{}
				for _, model := range p.Models {
					for _, scope := range [][2]string{{p.KeyID, model}, {p.KeyID, ""}, {"", model}, {"", ""}} {
						found := false
						for _, rule := range state.Rules {
							if rule.KeyID == scope[0] && rule.Model == scope[1] && len(rule.Order) > 0 {
								for n, name := range rule.Order {
									if name == p.Provider {
										positions[model] = n + 1
									}
								}
								found = true
								break
							}
						}
						if found {
							break
						}
					}
				}
				kl := keyLabels[p.KeyID]
				protocol := providerProtocol(p.Provider)
				if protocol == "" {
					for _, spec := range channelProtocols {
						if spec.Engine == engines[p.Provider] {
							protocol = spec.Protocol
						}
					}
				}
				results[i].Data = append(results[i].Data, subInstalledChannel{Protocol: protocol, Engine: engines[p.Provider], ModelMappings: mappings[p.Provider], Base: ref.Base, AccountID: ref.Account, GroupID: ref.Group, SourceID: src.ID, SourceName: src.Name, KeyID: p.KeyID, KeyPosition: kl.Position, KeyPrefix: kl.Prefix, Provider: p.Provider, Name: ref.Name, Models: p.Models, Positions: positions, Revision: state.Revision, Manageable: state.Manageable})
			}
		}(i, source)
	}
	wg.Wait()
	data := []subInstalledChannel{}
	labels := map[string]map[string]string{}
	unavailable := []string{}
	for i, result := range results {
		if result.Error != "" {
			unavailable = append(unavailable, result.Error)
		}
		data = append(data, result.Data...)
		if result.Labels != nil {
			labels[sources[i].ID] = result.Labels
		}
	}
	configured, bindingErr := s.configuredBindings(r.Context(), owner)
	if bindingErr != nil {
		http.Error(w, "配置渠道关联读取失败", 503)
		return
	}
	seen := map[string]bool{}
	for _, c := range data {
		seen[c.SourceID+"\n"+c.Provider] = true
	}
	for _, c := range configured {
		if !seen[c.SourceID+"\n"+c.Provider] {
			data = append(data, c)
			if c.Name != c.Provider {
				if labels[c.SourceID] == nil {
					labels[c.SourceID] = map[string]string{}
				}
				labels[c.SourceID][c.Provider] = c.Name
			}
		}
	}
	sort.Slice(data, func(i, j int) bool {
		if data[i].SourceID != data[j].SourceID {
			return data[i].SourceID < data[j].SourceID
		}
		return data[i].KeyID+data[i].Provider < data[j].KeyID+data[j].Provider
	})
	writeJSON(w, 200, map[string]any{"data": data, "labels": labels, "multipliers": subChannelMultipliers(data, refs), "unavailable_sources": unavailable})
}

// Reuse saved billing metadata; sorting must not query upstreams or infer a
// multiplier from a user-editable channel name. Mixed or unbound keys are unknown.
func subChannelMultipliers(channels []subInstalledChannel, refs []subChannelRef) map[string]map[string]*float64 {
	type group struct {
		account string
		id      int64
	}
	rates := map[group]*float64{}
	for _, ref := range refs {
		rates[group{ref.Account, ref.Group}] = ref.Rate
	}
	out := map[string]map[string]*float64{}
	for _, channel := range channels {
		var rate *float64
		if channel.Kind != "configured" {
			rate = rates[group{channel.AccountID, channel.GroupID}]
		} else if channel.BindingStatus == "matched" {
			for i, key := range channel.BoundKeys {
				value := rates[group{key.AccountID, key.GroupID}]
				if value == nil || (i > 0 && rate != nil && *value != *rate) {
					rate = nil
					break
				}
				rate = value
			}
		}
		if out[channel.SourceID] == nil {
			out[channel.SourceID] = map[string]*float64{}
		}
		if previous, ok := out[channel.SourceID][channel.Provider]; ok && (previous == nil || rate == nil || *previous != *rate) {
			rate = nil
		}
		out[channel.SourceID][channel.Provider] = rate
	}
	return out
}
func (s *Service) subManageChannel(w http.ResponseWriter, r *http.Request) {
	var in subImportInput
	if !decodeConfiguration(w, r, &in) {
		return
	}
	owner, _ := s.controlUser(r)
	if in.Action != "delete" && in.Action != "replace" {
		http.Error(w, "无效操作", 400)
		return
	}
	var owned bool
	if s.control.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM console_sub_accounts a JOIN console_sub_targets t ON t.account_id=a.id WHERE a.id=$1 AND t.group_id=$2 AND a.owner=$3)`, in.AccountID, in.GroupID, owner).Scan(&owned) != nil || !owned {
		http.Error(w, "分组不存在", 404)
		return
	}
	key, e := subRawKey(in.SourceID, in.KeyID)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	src, e := s.control.source(r.Context(), in.SourceID)
	if e != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	unlock, e := s.control.lockControls(r.Context(), src.ID)
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	defer unlock()
	stateMap, e := s.reconcileControls(r.Context(), src)
	status := 409
	if e != nil {
		http.Error(w, e.Error(), status)
		return
	}
	var state subGatewayControls
	if decodeMap(stateMap, &state) != nil {
		http.Error(w, "来源状态无效", 502)
		return
	}
	if !state.Manageable {
		http.Error(w, "来源尚未支持编辑和删除，请更新 uni-api", 400)
		return
	}
	if state.Revision != in.Revision {
		http.Error(w, "临时渠道状态已变化，请刷新后重试", 409)
		return
	}
	provider := subProviderName(in.AccountID, in.GroupID, key)
	if in.Provider != "" {
		if !matchesSubProvider(in.Provider, in.AccountID, in.GroupID, key) {
			http.Error(w, "接入不属于所选分组", 400)
			return
		}
		provider = in.Provider
	}
	var existing []string
	for _, p := range state.Temporary {
		if p.Provider == provider && p.KeyID == key {
			existing = p.Models
			break
		}
	}
	if existing == nil {
		http.Error(w, "临时渠道已删除或来源已重启", 404)
		return
	}
	if in.Action == "replace" {
		if _, err := importPublicModels(in.Models, in.ModelMappings); err != nil || in.Position < 1 || in.Position > 1025 {
			http.Error(w, "请选择模型和有效位置", 400)
			return
		}
		var actual []batchCatalogRow
		catalog, _, readErr := fetchSource(r.Context(), src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
		if readErr != nil || decodeMap(catalog["data"], &actual) != nil {
			http.Error(w, "当前路由读取失败", 503)
			return
		}
		current := map[string]string{}
		retainedProtocols := map[string]string{}
		for _, row := range actual {
			if row.Provider == provider {
				up := row.Upstream
				if up == "" {
					up = row.Model
				}
				current[row.Model] = up
				for _, spec := range channelProtocols {
					if spec.Engine == row.Engine {
						retainedProtocols[up] = spec.Protocol
					}
				}
			}
		}
		if e = s.validateSiteModelChanges(r.Context(), in.AccountID, in.GroupID, current, publicChannelModels(in.Models, in.ModelMappings)); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		if in.Protocols == nil {
			in.Protocols = map[string]string{}
		}
		for model, protocol := range retainedProtocols {
			if in.Protocols[model] == "" {
				in.Protocols[model] = protocol
			}
		}
		protocols, err := s.importProtocols(r.Context(), in.AccountID, in.GroupID, importUpstreamModels(in.Models, in.ModelMappings), in.Protocols)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		in.Protocols = protocols
	}
	mutation := map[string]any{"action": in.Action, "revision": in.Revision, "api_key_id": key, "provider": provider}
	// Delete has no model/position payload. A nil Go slice becomes JSON null,
	// which the gateway rejects when decoding its Vec<String> before dispatch.
	if in.Action == "replace" {
		mutation["models"] = in.Models
		mutation["position"] = in.Position
	}
	var applied map[string]any
	if in.Action == "replace" {
		var snapshot retainedSnapshot
		if stateMap["automatic_engine"] != true {
			http.Error(w, "来源尚不支持按地址自动识别引擎，请更新 uni-api", 400)
			return
		}
		snapshot, status, e = s.importSnapshot(r.Context(), src, stateMap, in.Revision)
		if e == nil {
			var document map[string]any
			document, status, e = s.importProviderDocument(r.Context(), src, snapshot, provider, in.Revision)
			if e == nil {
				e = errors.New("渠道定义已变化，请刷新重试")
				status = 409
				for _, old := range snapshot.Channels {
					if old.Provider == provider {
						var parts []protocolPartition
						parts, e = partitionChannelModels(publicChannelModels(in.Models, in.ModelMappings), in.Protocols)
						if e != nil {
							status = 400
							break
						}
						var channels []retainedChannel
						channels, e = splitProtocolChannel(&snapshot, old, document, parts)
						if e != nil {
							break
						}
						for _, channel := range channels {
							snapshot, status, e = s.prepareImportSnapshot(r.Context(), src, snapshot, channel, in.Position, protocolPositions(channel.Models, in.Positions), provider)
							if e != nil {
								break
							}
						}
						if e == nil {
							admin := src
							if src.ConfigKey != "" {
								admin.Key = src.ConfigKey
							}
							applied, status, e = subGateway(r.Context(), admin, "POST", "/v1/channel-controls/restore", map[string]any{"revision": in.Revision, "snapshot": snapshot})
						}
						break
					}
				}
			}
		}
	} else {
		applied, status, e = subGateway(r.Context(), src, "POST", "/v1/temporary-channels", mutation)
	}
	if e != nil {
		http.Error(w, e.Error(), status)
		return
	}
	if err := s.saveLiveControls(r.Context(), src, applied); err != nil {
		retentionFailure(w, err)
		return
	}
	message := "临时渠道已删除"
	if in.Action == "replace" {
		message = "临时渠道已更新"
	}
	writeJSON(w, 200, map[string]any{"message": message, "revision": applied["revision"]})
}

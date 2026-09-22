package main

import (
	"context"
	"encoding/json"
	"errors"
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
}
type subInstalledChannel struct {
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
		var b subBilling
		rate := "未知倍率"
		if json.Unmarshal(billing, &b) == nil && b.Rate != nil {
			rate = strconv.FormatFloat(*b.Rate, 'f', -1, 64)
		}
		ref.Name += "-" + rate
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
			}
			_ = decodeMap(catalogMap["data"], &modelRows)
			mappings := map[string]map[string]string{}
			for _, row := range modelRows {
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
					provider := subProviderName(ref.Account, ref.Group, key.ID)
					lookup[provider] = ref
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
				results[i].Data = append(results[i].Data, subInstalledChannel{ModelMappings: mappings[p.Provider], Base: ref.Base, AccountID: ref.Account, GroupID: ref.Group, SourceID: src.ID, SourceName: src.Name, KeyID: p.KeyID, KeyPosition: kl.Position, KeyPrefix: kl.Prefix, Provider: p.Provider, Name: ref.Name, Models: p.Models, Positions: positions, Revision: state.Revision, Manageable: state.Manageable})
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
		}
	}
	sort.Slice(data, func(i, j int) bool {
		if data[i].SourceID != data[j].SourceID {
			return data[i].SourceID < data[j].SourceID
		}
		return data[i].KeyID+data[i].Provider < data[j].KeyID+data[j].Provider
	})
	writeJSON(w, 200, map[string]any{"data": data, "labels": labels, "unavailable_sources": unavailable})
}
func (s *Service) subManageChannel(w http.ResponseWriter, r *http.Request) {
	var in subImportInput
	if !decodeControl(w, r, &in) {
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
		seen := map[string]bool{}
		for _, m := range importUpstreamModels(in.Models, in.ModelMappings) {
			if !subModelAllowed(m) || seen[m] {
				http.Error(w, "模型无效或重复", 400)
				return
			}
			seen[m] = true
			old := false
			for _, v := range existing {
				if v == m {
					old = true
				}
			}
			if old {
				continue
			}
			var success bool
			e = s.control.db.QueryRowContext(r.Context(), `SELECT state='done' AND result->'availability'->>'status'='success' FROM console_sub_models WHERE account_id=$1 AND group_id=$2 AND model=$3`, in.AccountID, in.GroupID, m).Scan(&success)
			if e != nil || !success {
				http.Error(w, "新增模型必须检测可用", 400)
				return
			}
		}
	}
	mutation := map[string]any{"action": in.Action, "revision": in.Revision, "api_key_id": key, "provider": provider}
	// Delete has no model/position payload. A nil Go slice becomes JSON null,
	// which the gateway rejects when decoding its Vec<String> before dispatch.
	if in.Action == "replace" {
		mutation["models"] = in.Models
		mutation["position"] = in.Position
	}
	var applied map[string]any
	if in.Action == "replace" && in.ModelMappings != nil {
		var snapshot retainedSnapshot
		snapshot, status, e = s.importSnapshot(r.Context(), src, stateMap, in.Revision)
		if e == nil {
			var document map[string]any
			document, status, e = s.importProviderDocument(r.Context(), src, snapshot, provider, in.Revision)
			if e == nil {
				document["model"] = importModelDefinition(in.Models, in.ModelMappings)
				raw, _ := json.Marshal(document)
				public, _ := importPublicModels(in.Models, in.ModelMappings)
				e = errors.New("渠道定义已变化，请刷新重试")
				status = 409
				for _, old := range snapshot.Channels {
					if old.Provider == provider {
						old.Definition = raw
						old.Models = public
						applied, status, e = s.applyImportSnapshot(r.Context(), src, snapshot, in.Revision, old, in.Position)
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
	writeJSON(w, 200, map[string]string{"message": message})
}

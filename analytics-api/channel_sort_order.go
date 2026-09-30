package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"time"
)

type channelSortSelection struct {
	Model     string   `json:"model"`
	Providers []string `json:"providers"`
}
type channelSortChange struct {
	Key         string            `json:"api_key_id"`
	KeyPosition int               `json:"key_position"`
	Model       string            `json:"model"`
	Before      []string          `json:"before"`
	After       []string          `json:"after"`
	Upstreams   map[string]string `json:"upstreams"`
}

// The receipt is authenticated with the control store's stable encryption key.
// It contains orders only, never provider credentials. Returning it BEFORE a
// write lets the client recover and undo even if the apply response is lost.
type channelSortReceipt struct {
	Version        int                 `json:"version"`
	Owner          string              `json:"owner"`
	Source         string              `json:"source"`
	Target         string              `json:"target"`
	Revision       string              `json:"revision"`
	ConfigRevision string              `json:"config_revision"`
	BeforeDigest   string              `json:"before_digest"`
	AfterDigest    string              `json:"after_digest"`
	BeforeRules    []retainedRule      `json:"before_rules"`
	AfterRules     []retainedRule      `json:"after_rules"`
	Changes        []channelSortChange `json:"changes"`
}

func sortSnapshotDigest(snapshot retainedSnapshot) string {
	snapshot.Rules = append([]retainedRule{}, snapshot.Rules...)
	sort.Slice(snapshot.Rules, func(i, j int) bool {
		return snapshot.Rules[i].KeyID+"\n"+snapshot.Rules[i].Model < snapshot.Rules[j].KeyID+"\n"+snapshot.Rules[j].Model
	})
	for i := range snapshot.Rules {
		snapshot.Rules[i].Disabled = append([]string{}, snapshot.Rules[i].Disabled...)
		sort.Strings(snapshot.Rules[i].Disabled)
		snapshot.Rules[i].Order = append([]string{}, snapshot.Rules[i].Order...)
	}
	snapshot.Channels = append([]retainedChannel{}, snapshot.Channels...)
	sort.Slice(snapshot.Channels, func(i, j int) bool { return snapshot.Channels[i].Provider < snapshot.Channels[j].Provider })
	for i := range snapshot.Channels {
		c := &snapshot.Channels[i]
		c.Models = append([]string{}, c.Models...)
		sort.Strings(c.Models)
		var definition any
		if json.Unmarshal(c.Definition, &definition) == nil {
			c.Definition, _ = json.Marshal(definition)
		}
	}
	raw, _ := json.Marshal(snapshot)
	return tokenHash(string(raw))
}
func channelSortOrders(rows []batchCatalogRow, selections []channelSortSelection, key string, keyPosition int) ([]channelSortChange, error) {
	orders := map[string][]string{}
	upstreams := map[string]map[string]string{}
	for _, row := range rows {
		if upstreams[row.Model] == nil {
			upstreams[row.Model] = map[string]string{}
		}
		if _, ok := upstreams[row.Model][row.Provider]; ok {
			continue
		}
		up := row.Upstream
		if up == "" {
			up = row.Model
		}
		upstreams[row.Model][row.Provider] = up
		orders[row.Model] = append(orders[row.Model], row.Provider)
	}
	changes := []channelSortChange{}
	seen := map[string]bool{}
	for _, selection := range selections {
		if selection.Model == "" || seen[selection.Model] {
			return nil, errors.New("排序模型无效或重复")
		}
		seen[selection.Model] = true
		providers := map[string]bool{}
		wanted := []string{}
		for _, p := range selection.Providers {
			if p == "" || providers[p] {
				return nil, errors.New("排序渠道无效或重复")
			}
			providers[p] = true
			if _, ok := upstreams[selection.Model][p]; ok {
				wanted = append(wanted, p)
			}
		}
		before := orders[selection.Model]
		after := append([]string{}, before...)
		n := 0
		for i, p := range before {
			if providers[p] {
				after[i] = wanted[n]
				n++
			}
		}
		if reflect.DeepEqual(before, after) || len(wanted) < 2 {
			continue
		}
		changes = append(changes, channelSortChange{Key: key, KeyPosition: keyPosition, Model: selection.Model, Before: before, After: after, Upstreams: upstreams[selection.Model]})
	}
	return changes, nil
}

func setChannelSortRules(snapshot *retainedSnapshot, changes []channelSortChange) {
	for _, change := range changes {
		found := false
		for i := range snapshot.Rules {
			if snapshot.Rules[i].KeyID == change.Key && snapshot.Rules[i].Model == change.Model {
				snapshot.Rules[i].Order = append([]string{}, change.After...)
				found = true
				break
			}
		}
		if !found {
			snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: change.Key, Model: change.Model, Order: append([]string{}, change.After...), Disabled: []string{}})
		}
	}
}
func (s *controlStore) sealChannelSort(receipt channelSortReceipt) (string, error) {
	raw, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	return s.encrypt(string(raw))
}
func (s *controlStore) openChannelSort(token, owner string, src controlSource) (channelSortReceipt, error) {
	var receipt channelSortReceipt
	raw, err := s.decrypt(token)
	if err != nil || json.Unmarshal([]byte(raw), &receipt) != nil || receipt.Version != 1 || receipt.Owner != owner || receipt.Source != src.ID || receipt.Target != controlTarget(src) {
		return receipt, errors.New("排序记录无效或来源身份已变化，请重新预览")
	}
	return receipt, nil
}
func (s *Service) sortCatalog(ctx context.Context, src controlSource, state map[string]any, key string) ([]batchCatalogRow, error) {
	raw, _, err := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
	var rows []batchCatalogRow
	if err != nil || decodeMap(raw["data"], &rows) != nil {
		return nil, errors.New("实际请求路由读取失败")
	}
	excluded := configuredExcluded(state, key)
	visible := []batchCatalogRow{}
	for _, row := range rows {
		if !excluded[row.Provider] {
			visible = append(visible, row)
		}
	}
	return visible, nil
}
func verifySortCatalog(rows []batchCatalogRow, change channelSortChange, after bool) bool {
	order := []string{}
	ups := map[string]string{}
	for _, row := range rows {
		if row.Model == change.Model {
			if _, ok := ups[row.Provider]; ok {
				continue
			}
			up := row.Upstream
			if up == "" {
				up = row.Model
			}
			ups[row.Provider] = up
			order = append(order, row.Provider)
		}
	}
	expected := change.Before
	if after {
		expected = change.After
	}
	return reflect.DeepEqual(order, expected) && reflect.DeepEqual(ups, change.Upstreams)
}

func (s *Service) channelSortOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action  string                 `json:"action"`
		Key     string                 `json:"api_key_id"`
		Models  []channelSortSelection `json:"models"`
		Receipt string                 `json:"receipt"`
	}
	if !decodeConfiguration(w, r, &in) {
		return
	}
	if in.Action != "prepare" && in.Action != "apply" && in.Action != "undo" && in.Action != "status" {
		http.Error(w, "无效排序操作", 400)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(115 * time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), 110*time.Second)
	defer cancel()
	src, err := s.control.source(ctx, r.PathValue("id"))
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	owner, _ := s.controlUser(r)
	unlock, err := s.control.waitControls(ctx, src.ID)
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
	revision, _ := state["revision"].(string)
	configRevision, _ := state["config_revision"].(string)
	snapshot, code, err := s.importSnapshot(ctx, src, state, revision)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	if in.Action == "prepare" {
		if len(in.Models) == 0 {
			http.Error(w, "当前筛选没有可排序渠道", 400)
			return
		}
		key := ""
		if in.Key != "" {
			key, err = subRawKey(src.ID, in.Key)
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		raw, _, err := fetchSource(ctx, src, "/v1/api-keys", nil)
		var keys []struct {
			ID       string `json:"key_id"`
			Position int    `json:"position"`
		}
		if err != nil || decodeMap(raw["data"], &keys) != nil {
			http.Error(w, "API key 列表读取失败", 503)
			return
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].Position < keys[j].Position })
		if configRevision == "" {
			http.Error(w, "来源未提供基础配置版本，请更新 uni-api 后重试", 409)
			return
		}
		receipt := channelSortReceipt{Version: 1, Owner: owner, Source: src.ID, Target: controlTarget(src), Revision: revision, ConfigRevision: configRevision, BeforeDigest: sortSnapshotDigest(snapshot), BeforeRules: append([]retainedRule(nil), snapshot.Rules...), Changes: []channelSortChange{}}
		matched := false
		for _, k := range keys {
			if key != "" && k.ID != key {
				continue
			}
			matched = true
			rows, err := s.sortCatalog(ctx, src, state, k.ID)
			if err != nil {
				http.Error(w, err.Error(), 503)
				return
			}
			changes, err := channelSortOrders(rows, in.Models, k.ID, k.Position)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			receipt.Changes = append(receipt.Changes, changes...)
		}
		if !matched {
			http.Error(w, "所选 API key 已不存在", 409)
			return
		}
		setChannelSortRules(&snapshot, receipt.Changes)
		receipt.AfterRules = snapshot.Rules
		receipt.AfterDigest = sortSnapshotDigest(snapshot)
		token, err := s.control.sealChannelSort(receipt)
		if err != nil {
			http.Error(w, "无法生成撤回记录", 500)
			return
		}
		writeJSON(w, 200, map[string]any{"receipt": token, "changes": receipt.Changes, "revision": revision})
		return
	}
	receipt, err := s.control.openChannelSort(in.Receipt, owner, src)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	digest := sortSnapshotDigest(snapshot)
	status := "conflict"
	if configRevision != receipt.ConfigRevision {
		status = "conflict"
	} else if digest == receipt.BeforeDigest {
		status = "before"
	} else if digest == receipt.AfterDigest {
		status = "applied"
	}
	if in.Action == "status" {
		writeJSON(w, 200, map[string]any{"status": status, "revision": revision})
		return
	}
	if in.Action == "apply" && status == "applied" || in.Action == "undo" && status == "before" {
		writeJSON(w, 200, map[string]any{"status": status, "revision": revision})
		return
	}
	if (in.Action == "apply" && (status != "before" || revision != receipt.Revision)) || (in.Action == "undo" && status != "applied") {
		http.Error(w, "路由配置在预览或应用后已变化，未执行覆盖；请刷新核对", 409)
		return
	}
	catalogs := map[string][]batchCatalogRow{}
	for _, change := range receipt.Changes {
		rows, ok := catalogs[change.Key]
		if !ok {
			rows, err = s.sortCatalog(ctx, src, state, change.Key)
			if err != nil {
				http.Error(w, err.Error(), 503)
				return
			}
			catalogs[change.Key] = rows
		}
		if !verifySortCatalog(rows, change, in.Action == "undo") {
			http.Error(w, "渠道成员、模型映射或实际顺序已变化，未执行覆盖；请重新预览", 409)
			return
		}
	}
	if in.Action == "apply" {
		snapshot.Rules = append([]retainedRule{}, receipt.AfterRules...)
		status = "applied"
	} else {
		snapshot.Rules = append([]retainedRule{}, receipt.BeforeRules...)
		status = "before"
	}
	admin := src
	if src.ConfigKey != "" {
		admin.Key = src.ConfigKey
	}
	applied, code, err := subGateway(ctx, admin, "POST", "/v1/channel-controls/restore", map[string]any{"revision": revision, "snapshot": snapshot})
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	if err = s.saveLiveControls(ctx, src, applied); err != nil {
		retentionFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"status": status, "revision": applied["revision"]})
}

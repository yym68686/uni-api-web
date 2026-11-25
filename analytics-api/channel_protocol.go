package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type channelProtocol struct {
	Protocol string `json:"protocol"`
	Engine   string `json:"engine"`
	Endpoint string `json:"endpoint"`
}

var channelProtocols = []channelProtocol{
	{"responses", "codex", "/v1/responses"},
	{"messages", "claude", "/v1/messages"},
	{"gemini", "gemini", "/v1beta"},
	{"chat", "gpt", "/v1/chat/completions"},
}

func protocolSpec(protocol string) (channelProtocol, bool) {
	for _, spec := range channelProtocols {
		if spec.Protocol == protocol {
			return spec, true
		}
	}
	return channelProtocol{}, false
}

func protocolProvider(root, protocol string) string { return root + "--" + protocol }

func subProviderNames(account string, group int64, key string) []string {
	root := subProviderName(account, group, key)
	names := []string{root}
	for _, spec := range channelProtocols {
		names = append(names, protocolProvider(root, spec.Protocol))
	}
	return names
}

func matchesSubProvider(provider, account string, group int64, key string) bool {
	for _, name := range subProviderNames(account, group, key) {
		if provider == name {
			return true
		}
	}
	return false
}

func providerProtocol(provider string) string {
	for _, spec := range channelProtocols {
		if strings.HasSuffix(provider, "--"+spec.Protocol) {
			return spec.Protocol
		}
	}
	return ""
}

func catalogChannelProtocol(rows []batchCatalogRow, provider string) string {
	for _, row := range rows {
		if row.Provider != provider {
			continue
		}
		for _, spec := range channelProtocols {
			if spec.Engine == row.Engine {
				return spec.Protocol
			}
		}
	}
	return ""
}

func channelProtocolLabel(protocol string) string {
	labels := map[string]string{"responses": "Responses / Codex", "messages": "Claude Messages", "gemini": "Gemini", "chat": "Chat Completions"}
	if label := labels[protocol]; label != "" {
		return label
	}
	return "协议未确认"
}

// Report every incompatible target before the single atomic restore. A rejected
// source batch must identify the offending channel and model, not accuse all
// channels or silently change the protocol of their existing serving models.
func (s *Service) validateBatchProtocols(ctx context.Context, in channelBatchInput, catalogs map[string][]batchCatalogRow, documents map[string]map[string]any) error {
	problems := []string{}
	for _, t := range in.Targets {
		if t.Account == "" || in.Part == "positions" || in.removes(t) {
			continue
		}
		protocol := catalogChannelProtocol(catalogs[t.Key], t.Provider)
		if t.MigrateLegacyEngine {
			// prepareLegacyEngineMigrations has already proved the legacy origin
			// and removed gpt from the unchanged /v1/responses document.
			protocol = "responses"
		}
		if protocol == "" {
			for _, spec := range channelProtocols {
				if spec.Engine == documents[t.Key+"\n"+t.Provider]["engine"] {
					protocol = spec.Protocol
				}
			}
		}
		added := []string{}
		for model, upstream := range t.Models {
			if t.Current[model] != upstream {
				added = append(added, model)
			}
		}
		sort.Strings(added)
		for _, model := range added {
			upstream := t.Models[model]
			resolved, err := s.importProtocols(ctx, t.Account, t.Group, []string{upstream}, nil)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s · %s：%s", t.Provider, model, err))
				continue
			}
			expected := protocol
			if split := t.SplitProtocols[model]; split != "" {
				expected = split
			}
			if expected == "" || resolved[upstream] != expected {
				label := "当前"
				if t.SplitProtocols[model] != "" {
					label = "拟接入"
				}
				problems = append(problems, fmt.Sprintf("%s · %s：%s %s，检测 %s", t.Provider, model, label, channelProtocolLabel(expected), channelProtocolLabel(resolved[upstream])))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("渠道协议冲突，未提交任何变更：\n%s\n请取消这些新增项，或在添加到渠道中按协议拆分后重新分析", strings.Join(problems, "\n"))
	}
	return nil
}

func validBatchProtocolSplit(t channelBatchTarget) bool {
	for model, protocol := range t.SplitProtocols {
		if _, ok := protocolSpec(protocol); !ok || t.Models[model] == "" || t.Current[model] != "" {
			return false
		}
	}
	return !t.MigrateLegacyEngine
}

func (s *Service) validateBatchProtocolSplits(ctx context.Context, src controlSource, snapshot retainedSnapshot, state map[string]any, owner string, in channelBatchInput, catalogs map[string][]batchCatalogRow, documents map[string]map[string]any) error {
	var overrides map[string]bool
	bases := map[string]string{}
	for _, t := range in.Targets {
		if len(t.SplitProtocols) == 0 {
			continue
		}
		if state["automatic_engine"] != true {
			return errors.New("来源尚不支持检测协议自动接入，请更新 uni-api")
		}
		if overrides == nil {
			var err error
			overrides, err = s.engineOverrideHistory(ctx, src.ID)
			if err != nil {
				return errors.New("渠道协议配置读取失败")
			}
		}
		doc := documents[t.Key+"\n"+t.Provider]
		base, _ := doc["base_url"].(string)
		engine, _ := doc["engine"].(string)
		spec, hasProtocol := protocolSpec(catalogChannelProtocol(catalogs[t.Key], t.Provider))
		if _, ok := bases[t.Account]; !ok {
			var site string
			if err := s.control.db.QueryRowContext(ctx, `SELECT base FROM console_sub_accounts WHERE id=$1 AND owner=$2`, t.Account, owner).Scan(&site); err != nil {
				return errors.New("渠道所属站点已变化")
			}
			bases[t.Account] = subBindingSite(site)
		}
		if !hasProtocol || bases[t.Account] == "" || strings.TrimRight(base, "/") != bases[t.Account]+spec.Endpoint ||
			(engine != "" && engine != "auto") || overrides[t.Provider] || protocolRepairCustomized(snapshot, t.Provider) {
			return fmt.Errorf("%s：无法确认原渠道的自动协议及上游地址，未提交修改", t.Provider)
		}
		for _, protocol := range t.SplitProtocols {
			if protocol == spec.Protocol {
				return errors.New("渠道协议已变化，请重新预览")
			}
		}
	}
	return nil
}

// Only newly selected models go to the new protocol. Existing names, rules
// and settings stay attached to their original provider unless separately edited.
func addBatchProtocolSiblings(snapshot *retainedSnapshot, t channelBatchTarget, doc map[string]any, catalogs map[string][]batchCatalogRow, moves map[string][]routeMove) (channelBatchTarget, error) {
	if doc == nil {
		return t, errors.New("原渠道定义不完整")
	}
	var old retainedChannel
	for _, p := range snapshot.Channels {
		if p.Provider == t.Provider && p.KeyID == t.Key {
			old = p
			break
		}
	}
	if old.Provider == "" {
		return t, errors.New("原渠道已变化，请重新预览")
	}
	root := subProviderName(t.Account, t.Group, t.Key)
	parts := map[string]map[string]string{}
	models, positions := map[string]string{}, map[string]int{}
	for model, upstream := range t.Models {
		if split := t.SplitProtocols[model]; split != "" {
			if parts[split] == nil {
				parts[split] = map[string]string{}
			}
			parts[split][model] = upstream
		} else {
			models[model], positions[model] = upstream, t.Positions[model]
		}
	}
	// Do not carry receipts to a different provider/endpoint/credential scope.
	siblingDoc := map[string]any{}
	for key, value := range doc {
		siblingDoc[key] = value
	}
	if prefs, ok := doc["preferences"].(map[string]any); ok {
		copy := map[string]any{}
		for key, value := range prefs {
			if key != "service_tier_evidence" && key != "__model_upstream_protocols" {
				copy[key] = value
			}
		}
		siblingDoc["preferences"] = copy
	}
	for _, spec := range channelProtocols {
		models := parts[spec.Protocol]
		if len(models) == 0 {
			continue
		}
		provider := protocolProvider(root, spec.Protocol)
		for _, existing := range snapshot.Channels {
			if existing.Provider == provider {
				return t, errors.New("目标协议接入已存在，请重新预览")
			}
		}
		for _, existing := range catalogs[t.Key] {
			if existing.Provider == provider {
				return t, errors.New("目标协议接入已存在，请重新预览")
			}
		}
		channel, err := protocolChannel(old, siblingDoc, provider, protocolPartition{spec, models})
		if err != nil {
			return t, err
		}
		snapshot.Channels = append(snapshot.Channels, channel)
		for model, up := range models {
			catalogs[t.Key] = append(catalogs[t.Key], batchCatalogRow{Provider: provider, Model: model, Upstream: up, Engine: spec.Engine})
			moves[t.Key] = append(moves[t.Key], routeMove{Provider: provider, Model: model, Position: t.Positions[model]})
		}
	}
	t.Models, t.Positions = models, positions
	return t, nil
}

func protocolPositions(models []string, positions map[string]int) map[string]int {
	filtered := map[string]int{}
	for _, model := range models {
		if value, ok := positions[model]; ok {
			filtered[model] = value
		}
	}
	return filtered
}

func (s *Service) importProtocols(ctx context.Context, account string, group int64, models []string, choices map[string]string) (map[string]string, error) {
	resolved := map[string]string{}
	for _, model := range models {
		protocol := choices[model]
		if protocol == "" && s.control != nil {
			var raw []byte
			if err := s.control.db.QueryRowContext(ctx, `SELECT result FROM console_sub_models WHERE account_id=$1 AND group_id=$2 AND model=$3`, account, group, model).Scan(&raw); err == nil {
				var result subResult
				if json.Unmarshal(raw, &result) == nil && result.Availability.Status == "success" {
					protocol = result.Availability.Protocol
				}
			}
		}
		if _, ok := protocolSpec(protocol); !ok {
			return nil, fmt.Errorf("模型 %s 的协议不明确，请选择上游协议", model)
		}
		resolved[model] = protocol
	}
	return resolved, nil
}

type protocolPartition struct {
	channelProtocol
	Models map[string]string `json:"models"`
}

func partitionChannelModels(models map[string]string, protocols map[string]string) ([]protocolPartition, error) {
	groups := map[string]map[string]string{}
	for name, upstream := range models {
		protocol := protocols[upstream]
		if _, ok := protocolSpec(protocol); !ok {
			return nil, fmt.Errorf("模型 %s 的协议不明确，请选择上游协议", upstream)
		}
		if groups[protocol] == nil {
			groups[protocol] = map[string]string{}
		}
		groups[protocol][name] = upstream
	}
	parts := []protocolPartition{}
	for _, spec := range channelProtocols {
		if len(groups[spec.Protocol]) > 0 {
			parts = append(parts, protocolPartition{spec, groups[spec.Protocol]})
		}
	}
	if len(parts) == 0 {
		return nil, errors.New("请至少选择一个模型")
	}
	return parts, nil
}

func protocolChannel(old retainedChannel, document map[string]any, provider string, part protocolPartition) (retainedChannel, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return retainedChannel{}, err
	}
	var copyDocument map[string]any
	if err = json.Unmarshal(raw, &copyDocument); err != nil {
		return retainedChannel{}, err
	}
	base, _ := copyDocument["base_url"].(string)
	base = subBindingSite(base)
	if base == "" {
		return retainedChannel{}, errors.New("渠道上游地址无效")
	}
	copyDocument["provider"], copyDocument["base_url"] = provider, base+part.Endpoint
	delete(copyDocument, "engine")
	copyDocument["model"] = importModelDefinition(nil, part.Models)
	raw, err = json.Marshal(copyDocument)
	if err != nil {
		return retainedChannel{}, err
	}
	models := make([]string, 0, len(part.Models))
	for model := range part.Models {
		models = append(models, model)
	}
	sort.Strings(models)
	old.Provider, old.Base, old.Models, old.Definition = provider, base+part.Endpoint, models, raw
	return old, nil
}

func splitProtocolChannel(snapshot *retainedSnapshot, old retainedChannel, document map[string]any, parts []protocolPartition) ([]retainedChannel, error) {
	channels := []retainedChannel{}
	preferred := providerProtocol(old.Provider)
	if preferred == "" {
		for _, spec := range channelProtocols {
			if spec.Engine == document["engine"] {
				preferred = spec.Protocol
			}
		}
	}
	for index := range parts {
		if parts[index].Protocol == preferred {
			parts[0], parts[index] = parts[index], parts[0]
			break
		}
	}
	if suffix := providerProtocol(old.Provider); suffix != "" && parts[0].Protocol != suffix {
		return nil, errors.New("派生渠道的协议不能改变；请新建对应协议的渠道")
	}
	root := strings.TrimSuffix(old.Provider, "--"+providerProtocol(old.Provider))
	for index, part := range parts {
		provider := old.Provider
		if index > 0 {
			provider = protocolProvider(root, part.Protocol)
		}
		for _, existing := range snapshot.Channels {
			if existing.Provider == provider && provider != old.Provider {
				return nil, errors.New("该协议的子渠道已存在，请分别编辑，未提交修改")
			}
		}
		channel, err := protocolChannel(old, document, provider, part)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	replace := func(names []string, model string) []string {
		out := []string{}
		seen := map[string]bool{}
		for _, name := range names {
			values := []string{name}
			if name == old.Provider {
				values = nil
				for _, channel := range channels {
					for _, offered := range channel.Models {
						if model == "" || model == offered {
							values = append(values, channel.Provider)
							break
						}
					}
				}
			}
			for _, value := range values {
				if !seen[value] {
					out = append(out, value)
					seen[value] = true
				}
			}
		}
		return out
	}
	for index := range snapshot.Rules {
		rule := &snapshot.Rules[index]
		rule.Order, rule.Disabled = replace(rule.Order, rule.Model), replace(rule.Disabled, rule.Model)
	}
	kept := []retainedChannel{}
	for _, channel := range snapshot.Channels {
		if channel.Provider != old.Provider {
			kept = append(kept, channel)
		}
	}
	snapshot.Channels = append(kept, channels...)
	delete(snapshot.Settings, old.Provider)
	return channels, nil
}

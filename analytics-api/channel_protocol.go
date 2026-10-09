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

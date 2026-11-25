package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type optimizationProtocolMove struct {
	Key      string `json:"api_key_id"`
	From     string `json:"from_provider"`
	To       string `json:"to_provider"`
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
}

type optimizationMoveRoute struct{ key, provider, model string }

// A switch is a paired operation, even if the destination already serves the
// model. Never treat the old half as an independent removal.
func validateOptimizationProtocolMoveTargets(source string, in *channelBatchInput) (map[string]bool, error) {
	if len(in.ProtocolMoves) != 0 && in.Part != "optimize" {
		return nil, errors.New("无效的协议切换操作")
	}
	targets := map[string]channelBatchTarget{}
	for _, t := range in.Targets {
		targets[t.Key+"\n"+t.Provider] = t
	}
	sources := map[string]bool{}
	movedFrom, movedTo := map[optimizationMoveRoute]bool{}, map[optimizationMoveRoute]bool{}
	for i := range in.ProtocolMoves {
		m := &in.ProtocolMoves[i]
		key, err := subRawKey(source, m.Key)
		_, validProtocol := protocolSpec(m.Protocol)
		if err != nil || !validProtocol || !validPublicModel(m.Model) || m.From == m.To {
			return nil, errors.New("模型协议切换参数无效")
		}
		m.Key = key
		fromID := key + "\n" + m.From
		from := targets[fromID]
		to := targets[key+"\n"+m.To]
		up := from.Current[m.Model]
		fr, tr := optimizationMoveRoute{key, m.From, m.Model}, optimizationMoveRoute{key, m.To, m.Model}
		if from.Account == "" || up == "" || movedFrom[fr] || movedTo[tr] ||
			!matchesSubProvider(m.To, from.Account, from.Group, key) {
			return nil, errors.New("协议切换必须使用同一分组、同一 Key 的原接入和目标接入")
		}
		if split := from.SplitProtocols[m.Model]; split != "" {
			if split != m.Protocol || m.To != protocolProvider(subProviderName(from.Account, from.Group, key), split) || to.Provider != "" ||
				from.Models[m.Model] != up {
				return nil, errors.New("新协议接入与选中的模型不一致")
			}
		} else if from.Models[m.Model] != "" || to.Account != from.Account || to.Group != from.Group ||
			to.Models[m.Model] != up || to.SplitProtocols[m.Model] != "" ||
			(to.Current[m.Model] != "" && to.Current[m.Model] != up) {
			return nil, errors.New("原路由与目标路由不完整，未切换协议")
		}
		sources[fromID], movedFrom[fr], movedTo[tr] = true, true, true
	}
	for _, t := range in.Targets {
		for model, protocol := range t.SplitProtocols {
			if _, ok := protocolSpec(protocol); !ok || t.Models[model] == "" ||
				(t.Current[model] != "" && !movedFrom[optimizationMoveRoute{t.Key, t.Provider, model}]) {
				return nil, errors.New("无效的协议变更")
			}
		}
	}
	return sources, nil
}

// Read live model evidence, actual endpoint and settings under the same source
// lock as the restore. A changed probe or a disabled destination must not
// strand a model. The caller's own disabled source policy is preserved.
func (s *Service) prepareOptimizationProtocolMoves(ctx context.Context, src controlSource, snapshot retainedSnapshot, state map[string]any, owner string, in channelBatchInput, catalogs map[string][]batchCatalogRow, documents map[string]map[string]any) ([]optimizationProtocolMove, error) {
	if len(in.ProtocolMoves) == 0 {
		return nil, nil
	}
	if state["automatic_engine"] != true || state["channel_settings"] != true {
		return nil, errors.New("来源尚不支持协议切换，未提交修改")
	}
	overrides, err := s.engineOverrideHistory(ctx, src.ID)
	if err != nil {
		return nil, errors.New("渠道协议配置读取失败")
	}
	targets := map[string]channelBatchTarget{}
	owned := map[string]bool{}
	for _, t := range in.Targets {
		targets[t.Key+"\n"+t.Provider] = t
	}
	for _, c := range snapshot.Channels {
		owned[c.KeyID+"\n"+c.Provider] = true
	}
	type proofScope struct {
		account string
		group   int64
		model   string
	}
	sites := map[string]string{}
	proofs := map[proofScope]string{}
	type ruleScope struct{ key, model string }
	disabledAt := make(map[ruleScope][]string, len(snapshot.Rules))
	for _, rule := range snapshot.Rules {
		disabledAt[ruleScope{rule.KeyID, rule.Model}] = rule.Disabled
	}
	isDisabled := func(key, model, provider string) bool {
		for _, id := range []ruleScope{{key, model}, {key, ""}, {"", model}, {"", ""}} {
			if slices.Contains(disabledAt[id], provider) {
				return true
			}
		}
		return false
	}
	disabled := []optimizationProtocolMove{}
	for _, m := range in.ProtocolMoves {
		fromID, toID := m.Key+"\n"+m.From, m.Key+"\n"+m.To
		from, to := targets[fromID], targets[toID]
		current, ok := protocolSpec(optimizationMoveCatalogProtocol(catalogs[m.Key], m.From))
		if !ok || current.Protocol == m.Protocol || !owned[fromID] ||
			from.MigrateLegacyEngine || overrides[m.From] || hasChannelProtocolOverride(snapshot, m.From) {
			return nil, fmt.Errorf("%s · %s：原协议已变化或由人工指定，未提交修改", m.From, m.Model)
		}
		site, ok := sites[from.Account]
		if !ok {
			var base string
			if err := s.control.db.QueryRowContext(ctx, `SELECT base FROM console_sub_accounts WHERE id=$1 AND owner=$2`, from.Account, owner).Scan(&base); err != nil {
				return nil, errors.New("渠道所属站点已变化")
			}
			site = subBindingSite(base)
			sites[from.Account] = site
		}
		fromDoc := documents[fromID]
		fromBase, _ := fromDoc["base_url"].(string)
		engine, _ := fromDoc["engine"].(string)
		if site == "" || strings.TrimRight(fromBase, "/") != site+current.Endpoint || (engine != "" && engine != "auto") {
			return nil, fmt.Errorf("%s：无法确认原渠道的自动协议和上游地址", m.From)
		}
		if to.Provider != "" {
			spec, _ := protocolSpec(m.Protocol)
			toBase, _ := documents[toID]["base_url"].(string)
			actual := optimizationMoveCatalogProtocol(catalogs[m.Key], m.To)
			if to.MigrateLegacyEngine {
				actual = "responses" // prepareLegacyEngineMigrations already validated it.
			}
			if !owned[toID] || actual != m.Protocol || strings.TrimRight(toBase, "/") != site+spec.Endpoint {
				return nil, fmt.Errorf("%s · %s：目标接入协议或地址已变化，未提交修改", m.To, m.Model)
			}
		}
		upstream := from.Current[m.Model]
		scope := proofScope{from.Account, from.Group, upstream}
		if _, ok := proofs[scope]; !ok {
			var raw []byte
			var result subResult
			var checkState string
			if err := s.control.db.QueryRowContext(ctx, `SELECT m.state,m.result
 FROM console_sub_models m JOIN console_sub_targets t USING(account_id,group_id)
 WHERE m.account_id=$1 AND m.group_id=$2 AND m.model=$3 AND t.active`, from.Account, from.Group, upstream).Scan(&checkState, &raw); err != nil ||
				checkState != "done" || json.Unmarshal(raw, &result) != nil || !usableProtocolMoveEvidence(result.Availability) {
				return nil, fmt.Errorf("%s · %s：最新检测未确认可用的目标协议，未提交修改", m.From, m.Model)
			}
			proofs[scope] = result.Availability.Protocol
		}
		if proofs[scope] != m.Protocol {
			return nil, fmt.Errorf("%s · %s：检测协议已变化，请重新预览", m.From, m.Model)
		}
		fromDisabled := isDisabled(m.Key, m.Model, m.From)
		toDisabled := isDisabled(m.Key, m.Model, m.To)
		if toDisabled && !fromDisabled {
			return nil, fmt.Errorf("%s · %s：目标路由已被停用，旧路由未移除", m.To, m.Model)
		}
		if fromDisabled && (to.Provider == "" || to.Current[m.Model] == "") {
			disabled = append(disabled, m)
		}
	}
	return disabled, nil
}

func usableProtocolMoveEvidence(probe subProbe) bool {
	return probe.Status == "success" && (probe.TerminalStatus == "" || probe.TerminalStatus == "complete") &&
		probe.ModelMatch != "mismatch" && probe.ModelMatch != "invalid"
}

// Do not infer a channel's protocol from an arbitrary model when the catalog
// reports conflicting engines for the same binding.
func optimizationMoveCatalogProtocol(rows []batchCatalogRow, provider string) string {
	var engine string
	for _, row := range rows {
		if row.Provider != provider || row.Engine == "" {
			continue
		}
		if engine != "" && engine != row.Engine {
			return ""
		}
		engine = row.Engine
	}
	for _, spec := range channelProtocols {
		if spec.Engine == engine {
			return spec.Protocol
		}
	}
	return ""
}

func retainProtocolMoveDisables(snapshot *retainedSnapshot, moves []optimizationProtocolMove) {
	type scope struct{ key, model string }
	rules := make(map[scope]int, len(snapshot.Rules))
	for i, r := range snapshot.Rules {
		rules[scope{r.KeyID, r.Model}] = i
	}
	for _, move := range moves {
		id := scope{move.Key, move.Model}
		i, exists := rules[id]
		if !exists {
			i = len(snapshot.Rules)
			snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: move.Key, Model: move.Model, Order: []string{}})
			rules[id] = i
		}
		if !containsProvider(snapshot.Rules[i].Disabled, move.To) {
			snapshot.Rules[i].Disabled = append(snapshot.Rules[i].Disabled, move.To)
		}
	}
}

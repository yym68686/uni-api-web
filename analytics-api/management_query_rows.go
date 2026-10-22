package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Request-local management view. Inputs must already be owner-authorized and
// read from one current snapshot. This builder does not fetch remote state,
// mutate configuration, or retain a cross-request cache.
type managementQueryInput struct {
	Accounts           []subAccount                     `json:"accounts"`
	Channels           []managementChannel              `json:"channels"`
	Imports            []subInstalledChannel            `json:"imports"`
	Checks             []configuredCheck                `json:"checks"`
	Fingerprints       []managementFingerprintRecord    `json:"fingerprints"`
	FingerprintReady   bool                             `json:"fingerprint_ready"`
	UnavailableSources []string                         `json:"unavailable_sources"`
	UnavailableImports []string                         `json:"unavailable_imports"`
	Routes             map[string]managementQueryRoutes `json:"routes"`
}

type managementQueryChannel struct {
	managementChannel
	Members []managementChannel `json:"members"`
}

type managementQueryCheck struct {
	subModelResult
	SourceName string `json:"source_name,omitempty"`
}

type managementQueryRow struct {
	ID         string                  `json:"id"`
	Account    subAccount              `json:"account"`
	Target     subTarget               `json:"target"`
	AccountIDs []string                `json:"accountIds"`
	Configured *managementQueryChannel `json:"configured,omitempty"`
	Checks     []managementQueryCheck  `json:"checks"`
	Selected   *managementQueryCheck   `json:"selected,omitempty"`
}

func managementPending(state string) bool { return state == "queued" || state == "running" }

func managementUnique(values []string) []string {
	out, seen := []string{}, map[string]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func managementModelChecks(target *subTarget) []managementQueryCheck {
	byModel := make(map[string]subModelResult, len(target.Models))
	names := append([]string{}, subModels...)
	for _, check := range target.Models {
		byModel[check.Model] = check
		names = append(names, check.Model)
	}
	checks := make([]managementQueryCheck, 0, len(names))
	for _, name := range managementUnique(names) {
		check, exists := byModel[name]
		if !exists {
			check = subModelResult{Model: name, State: "idle"}
			if name == "gpt-6-astra" && target.Result != nil {
				check.State, check.Result = "done", target.Result
			}
		}
		checks = append(checks, managementQueryCheck{subModelResult: check})
	}
	return checks
}

func managementSelect(row *managementQueryRow, model string) {
	row.Selected = nil
	for i := range row.Checks {
		if row.Checks[i].Model == model {
			row.Selected = &row.Checks[i]
			return
		}
	}
}

func managementGroupChannels(channels []managementChannel) []managementQueryChannel {
	groups, index := []managementQueryChannel{}, map[string]int{}
	for _, channel := range channels {
		key := mustJSON([]string{channel.Provider, channel.Engine})
		i, exists := index[key]
		if !exists {
			i, index[key] = len(groups), len(groups)
			groups = append(groups, managementQueryChannel{managementChannel: channel})
		}
		groups[i].Members = append(groups[i].Members, channel)
	}
	for i := range groups {
		group := &groups[i]
		models, accounts, sources := []string{}, []string{}, []string{}
		for _, member := range group.Members {
			models = append(models, member.Models...)
			accounts = append(accounts, member.AccountIDs...)
			sources = append(sources, member.SourceName)
		}
		group.Models, group.AccountIDs = managementUnique(models), managementUnique(accounts)
		group.SourceName = strings.Join(managementUnique(sources), " / ")
	}
	return groups
}

func managementToolModels(result *subCapabilityResult) []subToolUseModel {
	if result == nil {
		return nil
	}
	if result.Models != nil {
		return result.Models
	}
	if result.Model != "" {
		return []subToolUseModel{{Model: result.Model, State: "done", Result: result}}
	}
	return nil
}

func managementUpstream(channel managementChannel, model string) string {
	if upstream := channel.ModelMappings[model]; upstream != "" {
		return upstream
	}
	return model
}

func managementCheckedAt(check managementQueryCheck) int64 {
	if check.Result != nil {
		return check.Result.CheckedAt
	}
	return 0
}

func managementRecent(checks []managementQueryCheck) *managementQueryCheck {
	if len(checks) == 0 {
		return nil
	}
	best := 0
	for i := 1; i < len(checks); i++ {
		a, b := checks[i], checks[best]
		if managementPending(a.State) && !managementPending(b.State) ||
			managementPending(a.State) == managementPending(b.State) && managementCheckedAt(a) > managementCheckedAt(b) {
			best = i
		}
	}
	return &checks[best]
}

// Match the existing managementRows presentation contract, including stable
// source order and public aliases. It must not reuse evidence after a member's
// credential fingerprint changed, or infer a group binding from a site name.
func buildManagementQueryRows(in managementQueryInput, model, accountFilter string) ([]managementQueryRow, error) {
	accounts := make(map[string]*subAccount, len(in.Accounts))
	targets := map[string]*subTarget{}
	checks := map[string][]managementQueryCheck{}
	rows := []managementQueryRow{}
	for i := range in.Accounts {
		account := &in.Accounts[i]
		accounts[account.ID] = account
		for j := range account.Targets {
			target := &account.Targets[j]
			key := qualityGroupID(account.ID, target.GroupID)
			targets[key], checks[key] = target, managementModelChecks(target)
			row := managementQueryRow{ID: key, Account: *account, Target: *target, AccountIDs: []string{account.ID}, Checks: checks[key]}
			// A row carries its account header, never the other groups' results.
			row.Account.Targets = []subTarget{}
			managementSelect(&row, model)
			rows = append(rows, row)
		}
	}
	byChannel := map[string][]configuredCheck{}
	for _, check := range in.Checks {
		key := qualityChannelID(check.Source, check.Provider)
		byChannel[key] = append(byChannel[key], check)
	}
	imported := map[string]bool{}
	for _, channel := range in.Imports {
		if channel.Kind != "configured" {
			imported[qualityChannelID(channel.SourceID, channel.Provider)] = true
		}
	}
	nativeChannels := []managementChannel{}
	for _, channel := range in.Channels {
		if channel.Provider != "" && !imported[qualityChannelID(channel.SourceID, channel.Provider)] {
			nativeChannels = append(nativeChannels, channel)
		}
	}
	for _, group := range managementGroupChannels(nativeChannels) {
		group := group
		type binding struct {
			member  managementChannel
			account *subAccount
			target  *subTarget
		}
		visible := []binding{}
		native := []configuredCheck{}
		sourceNames := map[string]string{}
		for _, member := range group.Members {
			account := accounts[member.AccountID]
			if accountFilter == "" || accountFilter == "__unassigned__" || account != nil && account.ID == accountFilter {
				visible = append(visible, binding{member, account, targets[qualityGroupID(member.AccountID, member.GroupID)]})
			}
			if _, exists := sourceNames[member.SourceID]; !exists {
				sourceNames[member.SourceID] = member.SourceName
			}
			for _, check := range byChannel[qualityChannelID(member.SourceID, member.Provider)] {
				if check.Fingerprint == "" || member.ProbeFingerprint == "" || check.Fingerprint == member.ProbeFingerprint {
					native = append(native, check)
				}
			}
		}
		var preferred *binding
		for i := range visible {
			b := &visible[i]
			if b.target != nil && (model == "" || slices.Contains(b.member.Models, model)) {
				preferred = b
				break
			}
		}
		if preferred == nil {
			for i := range visible {
				if visible[i].target != nil {
					preferred = &visible[i]
					break
				}
			}
		}
		names := []string{}
		for _, id := range group.AccountIDs {
			if a := accounts[id]; a != nil && a.Name != "" {
				names = append(names, a.Name)
			}
		}
		name := strings.Join(names, "、")
		if name == "" {
			name = "未归属渠道"
		}
		account := subAccount{Name: name, Base: group.Base, State: "idle", Targets: []subTarget{}}
		target := subTarget{Platform: group.Engine, State: "idle", Models: []subModelResult{}}
		if group.Engine == "gpt" || group.Engine == "codex" {
			target.Platform = "openai"
		}
		if preferred != nil {
			account, target = *preferred.account, *preferred.target
			account.Targets = []subTarget{}
		}
		target.Name, target.Channel = group.Name, group.Provider
		if target.Name == "" {
			target.Name = group.Provider
		}
		var latestCompaction *configuredCheck
		var latestCompactionResult *subCapabilityResult
		for i := range native {
			c := &native[i]
			if c.Kind != "compaction" {
				continue
			}
			var result *subCapabilityResult
			if len(c.Result) != 0 {
				if err := json.Unmarshal(c.Result, &result); err != nil {
					return nil, fmt.Errorf("management compaction decode: %w", err)
				}
			}
			at, prior := int64(0), int64(0)
			if result != nil {
				at = result.CheckedAt
			}
			if latestCompactionResult != nil {
				prior = latestCompactionResult.CheckedAt
			}
			if latestCompaction == nil || managementPending(c.State) && !managementPending(latestCompaction.State) ||
				managementPending(c.State) == managementPending(latestCompaction.State) && at > prior {
				latestCompaction, latestCompactionResult = c, result
			}
		}
		if latestCompaction != nil {
			at, prior := int64(0), int64(0)
			if latestCompactionResult != nil {
				at = latestCompactionResult.CheckedAt
			}
			if target.Compaction != nil {
				prior = target.Compaction.CheckedAt
			}
			if preferred == nil || at >= prior {
				target.Compaction, target.CompactionState = latestCompactionResult, latestCompaction.State
				if target.Compaction == nil && latestCompaction.Message != "" {
					target.Compaction = &subCapabilityResult{Status: "error", Message: latestCompaction.Message, Attempts: []subProbe{}}
				}
			}
		}
		toolEntries, toolOrder := map[string]subToolUseModel{}, []string{}
		for _, member := range group.Members {
			entries, err := managementMemberTools(member, targets[qualityGroupID(member.AccountID, member.GroupID)], byChannel[qualityChannelID(member.SourceID, member.Provider)])
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				prior, exists := toolEntries[entry.Model]
				if !exists {
					toolOrder = append(toolOrder, entry.Model)
				}
				if !exists || capabilityAt(entry.Result) > capabilityAt(prior.Result) {
					toolEntries[entry.Model] = entry
				}
			}
		}
		target.ToolUse = &subCapabilityResult{Status: "error", Attempts: []subProbe{}, Models: []subToolUseModel{}}
		for _, name := range toolOrder {
			entry := toolEntries[name]
			target.ToolUse.Models = append(target.ToolUse.Models, entry)
			target.ToolUse.CheckedAt = max(target.ToolUse.CheckedAt, capabilityAt(entry.Result))
		}
		toolPending := false
		for _, c := range native {
			toolPending = toolPending || c.Kind == "tool-use" && managementPending(c.State)
		}
		if toolPending {
			target.ToolUseState = "running"
		} else if preferred == nil {
			target.ToolUseState = "done"
		}
		row := managementQueryRow{ID: "configured:" + group.SourceID + ":" + group.Provider, Account: account, Target: target, AccountIDs: group.AccountIDs, Configured: &group, Checks: []managementQueryCheck{}}
		models := append([]string{}, group.Models...)
		if preferred == nil {
			models = append(models, subModels...)
		}
		nativeModels := []managementQueryCheck{}
		for _, c := range native {
			if c.Kind != "model" && c.Kind != "availability" {
				continue
			}
			models = append(models, c.Model)
			check := managementQueryCheck{subModelResult: subModelResult{Model: c.Model, State: c.State, Message: c.Message}, SourceName: sourceNames[c.Source]}
			if len(c.Result) != 0 {
				if err := json.Unmarshal(c.Result, &check.Result); err != nil {
					return nil, fmt.Errorf("management model decode: %w", err)
				}
			}
			nativeModels = append(nativeModels, check)
		}
		for _, name := range managementUnique(models) {
			candidates := []managementQueryCheck{}
			for _, b := range visible {
				if b.target != nil && slices.Contains(b.member.Models, name) {
					for _, c := range checks[qualityGroupID(b.account.ID, b.target.GroupID)] {
						if c.Model == managementUpstream(b.member, name) {
							candidates = append(candidates, c)
						}
					}
				}
			}
			for _, c := range nativeModels {
				if c.Model == name {
					candidates = append(candidates, c)
				}
			}
			check := managementQueryCheck{subModelResult: subModelResult{State: "idle"}}
			if recent := managementRecent(candidates); recent != nil {
				check = *recent
			}
			check.Model = name
			row.Checks = append(row.Checks, check)
		}
		if preferred == nil {
			row.Target.Models = []subModelResult{}
			for _, check := range row.Checks {
				row.Target.Models = append(row.Target.Models, check.subModelResult)
			}
			quality := []configuredCheck{}
			for _, c := range native {
				if c.Kind == "model" && c.Model == "gpt-6-astra" {
					quality = append(quality, c)
				}
			}
			sort.SliceStable(quality, func(i, j int) bool { return managementRawAt(quality[i].Result) > managementRawAt(quality[j].Result) })
			if len(quality) > 0 {
				c := quality[0]
				row.Target.History = c.History
				row.Target.CheckSourceID = c.Source
				var result *subResult
				if len(c.Result) > 0 {
					if err := json.Unmarshal(c.Result, &result); err != nil {
						return nil, err
					}
				}
				if result != nil {
					row.Target.QualityCheck = &ChannelCheck{SourceID: c.Source, Provider: c.Provider, Model: "gpt-6-astra", CheckedAt: result.CheckedAt, Verdict: result.Verdict, Text: result.Quality.Text, Message: result.Quality.Message, DurationMS: result.Quality.Duration, QualityProbe: &result.Quality}
				}
			}
		}
		managementSelect(&row, model)
		rows = append(rows, row)
	}
	return rows, nil
}

func capabilityAt(result *subCapabilityResult) int64 {
	if result == nil {
		return 0
	}
	return result.CheckedAt
}

func managementRawAt(raw json.RawMessage) int64 {
	var result struct {
		CheckedAt int64 `json:"checked_at"`
	}
	_ = json.Unmarshal(raw, &result)
	return result.CheckedAt
}

func managementMemberTools(member managementChannel, bound *subTarget, checks []configuredCheck) ([]subToolUseModel, error) {
	entries, order := map[string]subToolUseModel{}, []string{}
	put := func(entry subToolUseModel) {
		if _, exists := entries[entry.Model]; !exists {
			order = append(order, entry.Model)
		}
		entries[entry.Model] = entry
	}
	if bound != nil {
		for _, model := range member.Models {
			for _, entry := range managementToolModels(bound.ToolUse) {
				if entry.Model == managementUpstream(member, model) {
					entry.Model = model
					put(entry)
					break
				}
			}
		}
	}
	native := []configuredCheck{}
	for _, c := range checks {
		if c.Kind == "tool-use" && (c.Fingerprint == "" || member.ProbeFingerprint == "" || c.Fingerprint == member.ProbeFingerprint) {
			native = append(native, c)
		}
	}
	for _, c := range native {
		if c.Model != "" {
			continue
		}
		var result *subCapabilityResult
		if len(c.Result) > 0 {
			if err := json.Unmarshal(c.Result, &result); err != nil {
				return nil, err
			}
		}
		for _, entry := range managementToolModels(result) {
			put(entry)
		}
		break
	}
	for _, c := range native {
		if c.Model == "" {
			continue
		}
		var result *subCapabilityResult
		if len(c.Result) > 0 {
			if err := json.Unmarshal(c.Result, &result); err != nil {
				return nil, err
			}
		}
		current := entries[c.Model]
		if current.Result == nil || capabilityAt(result) >= capabilityAt(current.Result) {
			if result == nil && c.Message != "" {
				result = &subCapabilityResult{Status: "error", Model: c.Model, Message: c.Message, Attempts: []subProbe{}}
			}
			put(subToolUseModel{Model: c.Model, State: c.State, Result: result})
		}
	}
	out := make([]subToolUseModel, 0, len(order))
	for _, name := range order {
		out = append(out, entries[name])
	}
	return out, nil
}

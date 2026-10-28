package main

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type managementQueryFilters struct {
	Search       string `json:"search"`
	AccountID    string `json:"accountId"`
	Model        string `json:"model"`
	MaxRate      string `json:"maxRate"`
	Sort         string `json:"sort"`
	Availability string `json:"availability"`
	GroupStatus  string `json:"groupStatus"`
	PriceStatus  string `json:"priceStatus"`
	Compaction   string `json:"compaction"`
	ToolUse      string `json:"toolUse"`
	Quality      string `json:"quality"`
	MinQuality   string `json:"minQuality"`
	Platform     string `json:"platform"`
	Joined       string `json:"joined"`
	ModelMatch   string `json:"modelMatch"`
	Fingerprint  string `json:"fingerprint"`
}

type managementFilterEvidence struct {
	Prices           []Price             `json:"prices"`
	FingerprintReady bool                `json:"fingerprint_ready"`
	Fingerprints     map[string][]string `json:"fingerprints"`
	Joined           map[string]string   `json:"joined"`
}

type managementFilteredRows struct {
	Rows      []managementQueryRow `json:"rows"`
	Rates     []float64            `json:"rates"`
	Platforms []string             `json:"platforms"`
}

type managementRate struct {
	Rate     *float64
	Mismatch bool
}

func managementBoundGroups(channel managementChannel) []subBoundKey {
	if channel.Kind != "configured" {
		if channel.AccountID != "" && channel.GroupID > 0 {
			return []subBoundKey{{AccountID: channel.AccountID, GroupID: channel.GroupID}}
		}
		return nil
	}
	if channel.BindingStatus != "matched" {
		return nil
	}
	return channel.BoundKeys
}

func managementBillingRate(row managementQueryRow, model string) managementRate {
	groups := map[string]bool{}
	if row.Configured != nil {
		for _, member := range row.Configured.Members {
			for _, binding := range managementBoundGroups(member) {
				if binding.AccountID != "" && binding.GroupID > 0 {
					groups[qualityGroupID(binding.AccountID, binding.GroupID)] = true
				}
			}
		}
	}
	if len(groups) > 1 {
		return managementRate{}
	}
	var advertised *float64
	if row.Target.Billing != nil {
		advertised = subValidCost(row.Target.Billing.Rate)
	}
	upstream := model
	allowed := map[string]bool{}
	if row.Configured != nil {
		upstream = managementUpstream(row.Configured.managementChannel, model)
		for _, name := range row.Configured.Models {
			allowed[managementUpstream(row.Configured.managementChannel, name)] = true
		}
	}
	out := managementRate{Rate: advertised}
	observed := false
	for name, rate := range row.Target.ObservedRates {
		if subValidCost(&rate.Rate) == nil || upstream != "" && name != upstream || upstream == "" && row.Configured != nil && !allowed[name] {
			continue
		}
		value := rate.Rate
		if !observed || value > *out.Rate {
			out.Rate = &value
		}
		observed = true
		out.Mismatch = out.Mismatch || advertised != nil && math.Abs(value-*advertised) > math.Max(1e-9, math.Abs(*advertised)*1e-9)
	}
	return out
}

func managementModelMatch(check managementQueryCheck) string {
	if check.Result == nil {
		return "untested"
	}
	probe := check.Result.Availability
	if probe.ModelMatch == "" {
		return "legacy"
	}
	if probe.Status != "success" {
		return "unavailable"
	}
	return probe.ModelMatch
}

func managementQuality(target *subTarget) string {
	var saved *subResult
	for _, check := range managementModelChecks(target) {
		if check.Model == "gpt-6-astra" {
			saved = check.Result
			break
		}
	}
	check := target.QualityCheck
	if check != nil && !(check.QualityProbe != nil && saved != nil && saved.CheckedAt == check.CheckedAt && saved.Quality.ID == check.QualityProbe.ID) {
		return check.Verdict
	}
	if saved != nil {
		return saved.Verdict
	}
	return ""
}

func managementCompaction(target subTarget, model string) string {
	if model != "" && !subCapabilityModel(model) {
		return "not_applicable"
	}
	if target.Compaction == nil || target.Compaction.Model != "" && !subCapabilityModel(target.Compaction.Model) || target.Compaction.Status == "" {
		return "untested"
	}
	return target.Compaction.Status
}

func managementToolCandidates(target subTarget) []string {
	models := []string{}
	for _, check := range target.Models {
		if check.Result != nil && check.Result.Availability.Status == "success" {
			models = append(models, check.Model)
		}
	}
	if target.Result != nil && target.Result.Availability.Status == "success" {
		models = append(models, target.Result.Model)
	}
	for _, entry := range managementToolModels(target.ToolUse) {
		models = append(models, entry.Model)
	}
	out := []string{}
	for _, model := range managementUnique(models) {
		if subCapabilityModel(model) {
			out = append(out, model)
		}
	}
	return out
}

func managementToolStatus(target subTarget, model string) string {
	if model != "" {
		if !subCapabilityModel(model) {
			return "not_applicable"
		}
		for _, entry := range managementToolModels(target.ToolUse) {
			if entry.Model == model {
				if entry.Result != nil && entry.Result.Status != "" {
					return entry.Result.Status
				}
				break
			}
		}
		return "untested"
	}
	statuses := []string{}
	for _, candidate := range managementToolCandidates(target) {
		statuses = append(statuses, managementToolStatus(target, candidate))
	}
	for _, status := range []string{"unsupported", "error", "untested"} {
		if slices.Contains(statuses, status) {
			return status
		}
	}
	if len(statuses) == 0 {
		return "untested"
	}
	return "supported"
}

func managementToolMatches(target subTarget, filter, model string) bool {
	if filter == "" {
		return true
	}
	candidates := managementToolCandidates(target)
	if model != "" || filter == "supported" || len(candidates) == 0 {
		return managementToolStatus(target, model) == filter
	}
	for _, candidate := range candidates {
		if managementToolStatus(target, candidate) == filter {
			return true
		}
	}
	return false
}

func managementPriceAssessment(check managementQueryCheck, prices []Price) (status, comparison string) {
	comparison = "unknown"
	var usage *subUsage
	if check.Result != nil {
		usage = check.Result.Availability.Usage
	}
	var expected *Price
	canonical := canonicalPriceModel(check.Model)
	for _, price := range prices {
		if price.Model == canonical && subValidCost(&price.Input) != nil && subValidCost(&price.Output) != nil &&
			(price.Verified || price.Source == "manual" || price.Input > 0 || price.Output > 0) {
			copy := price
			expected = &copy
			break
		}
	}
	var tier *PromptPriceTier
	for _, price := range modelCatalog {
		if price.Model == canonical {
			tier = price.PromptPriceTier
			break
		}
	}
	input := int64(0)
	if usage != nil {
		for _, count := range []*int64{usage.InputTokens, usage.CacheReadTokens, usage.CacheWriteTokens} {
			if subValidCount(count) != nil {
				input += *count
			}
		}
	}
	incomplete := tier != nil && input <= tier.ThresholdTokens && (usage == nil || subValidCount(usage.InputTokens) == nil || subValidCount(usage.CacheReadTokens) == nil || subValidCount(usage.CacheWriteTokens) == nil)
	if expected != nil && tier != nil && input > tier.ThresholdTokens {
		expected.Input *= tier.InputMultiplier
		expected.Output *= tier.OutputMultiplier
	}
	if !incomplete && usage != nil && usage.Status == "matched" && expected != nil {
		different := func(actual *float64, value float64, tokens *int64) bool {
			n := int64(1)
			if tokens != nil {
				n = max(n, *tokens)
			}
			return subValidCost(actual) != nil && math.Abs(*actual-value) > math.Max(1e-6, .00005/float64(n))+math.Abs(value)*1e-9
		}
		if different(usage.InputPrice, expected.Input, usage.InputTokens) || different(usage.OutputPrice, expected.Output, usage.OutputTokens) {
			comparison = "mismatch"
		} else if subValidCost(usage.InputPrice) != nil && subValidCost(usage.OutputPrice) != nil {
			comparison = "match"
		}
	}
	switch {
	case check.Result == nil:
		return "untested", comparison
	case usage != nil && usage.Status == "pending":
		return "pending", comparison
	case usage == nil || usage.Status != "matched":
		return "unknown", comparison
	case expected == nil || !expected.Verified:
		return "unpriced", comparison
	case comparison == "mismatch":
		return "abnormal", comparison
	case comparison == "unknown":
		return "unknown", comparison
	default:
		return "normal", comparison
	}
}

func managementPriceMatches(checks []managementQueryCheck, filter string, prices []Price) bool {
	if filter == "" {
		return true
	}
	allNormal, abnormal := len(checks) > 0, false
	for _, check := range checks {
		status, comparison := managementPriceAssessment(check, prices)
		if (filter == "unconfirmed_match" || filter == "unconfirmed_mismatch") && status != "normal" && status != "abnormal" && comparison == strings.TrimPrefix(filter, "unconfirmed_") {
			return true
		}
		allNormal, abnormal = allNormal && status == "normal", abnormal || status == "abnormal"
	}
	status := "unconfirmed"
	if abnormal {
		status = "abnormal"
	} else if allNormal {
		status = "normal"
	}
	return status == filter
}

// Facets intentionally use the same pre-rate/platform candidate set as the UI;
// changing one facet must not erase options selected in another facet.
func filterManagementQueryRows(rows []managementQueryRow, f managementQueryFilters, evidence managementFilterEvidence) managementFilteredRows {
	out := managementFilteredRows{Rows: []managementQueryRow{}, Rates: []float64{}, Platforms: []string{}}
	candidates := []managementQueryRow{}
	lower := cases.Lower(language.Und)
	search := lower.String(f.Search)
	for _, row := range rows {
		if f.AccountID != "" && (f.AccountID == "__unassigned__" && len(row.AccountIDs) != 0 || f.AccountID != "__unassigned__" && !slices.Contains(row.AccountIDs, f.AccountID)) {
			continue
		}
		if row.Configured != nil && row.Account.ID != "" && f.Model != "" && !slices.ContainsFunc(row.Checks, func(c managementQueryCheck) bool { return c.Model == f.Model }) {
			continue
		}
		available := row.Configured != nil || row.Target.Active
		if f.GroupStatus != "all" && (f.GroupStatus == "unavailable" && available || f.GroupStatus != "unavailable" && !available) {
			continue
		}
		source, bases := "", ""
		if row.Configured != nil {
			source = row.Configured.SourceName
			addresses := []string{}
			for _, member := range row.Configured.Members {
				addresses = append(addresses, member.Base)
			}
			bases = strings.Join(addresses, " ")
			if bases == "" {
				bases = row.Configured.Base
			}
		}
		text := strings.Join([]string{row.Account.Name, row.Account.Email, row.Target.Name, row.Target.Channel, row.Target.Platform, source, bases}, " ")
		if !strings.Contains(lower.String(text), search) {
			continue
		}
		checks := row.Checks
		if row.Selected != nil {
			checks = []managementQueryCheck{*row.Selected}
		}
		if f.Availability != "" && !slices.ContainsFunc(checks, func(c managementQueryCheck) bool {
			return f.Availability == "untested" && c.Result == nil || f.Availability != "untested" && c.Result != nil && c.Result.Availability.Status == f.Availability
		}) {
			continue
		}
		if !managementPriceMatches(checks, f.PriceStatus, evidence.Prices) ||
			f.ModelMatch != "" && !slices.ContainsFunc(checks, func(c managementQueryCheck) bool { return managementModelMatch(c) == f.ModelMatch }) ||
			f.Compaction != "" && managementCompaction(row.Target, f.Model) != f.Compaction ||
			!managementToolMatches(row.Target, f.ToolUse, f.Model) || f.Quality != "" && managementQuality(&row.Target) != f.Quality {
			continue
		}
		if f.MinQuality != "" {
			minimum, _ := strconv.ParseFloat(f.MinQuality, 64)
			if row.Target.History.Successful == 0 || float64(row.Target.History.Passed)*100 < minimum*float64(row.Target.History.Successful) {
				continue
			}
		}
		candidates = append(candidates, row)
	}
	rates, platforms := map[float64]bool{}, map[string]bool{}
	withinRate := func(row managementQueryRow) bool {
		billing := managementBillingRate(row, f.Model)
		if f.MaxRate == "" {
			return true
		}
		if f.MaxRate == "mismatch" {
			return billing.Mismatch
		}
		limit, err := strconv.ParseFloat(f.MaxRate, 64)
		return err == nil && billing.Rate != nil && *billing.Rate <= limit
	}
	for _, row := range candidates {
		billing := managementBillingRate(row, f.Model)
		if (f.Platform == "" || row.Target.Platform == f.Platform) && billing.Rate != nil {
			rates[*billing.Rate] = true
		}
		if withinRate(row) && row.Target.Platform != "" {
			platforms[row.Target.Platform] = true
		}
		if f.Fingerprint != "" && evidence.FingerprintReady {
			statuses := evidence.Fingerprints[row.ID]
			if f.Fingerprint == "not_applicable" && len(statuses) > 0 || f.Fingerprint != "not_applicable" && !slices.Contains(statuses, f.Fingerprint) {
				continue
			}
		}
		if !withinRate(row) || f.Platform != "" && row.Target.Platform != f.Platform || f.Joined != "" && evidence.Joined[row.ID] != f.Joined {
			continue
		}
		out.Rows = append(out.Rows, row)
	}
	if f.MaxRate != "" {
		if value, err := strconv.ParseFloat(f.MaxRate, 64); err == nil && !math.IsInf(value, 0) && !math.IsNaN(value) {
			rates[value] = true
		}
	}
	for rate := range rates {
		out.Rates = append(out.Rates, rate)
	}
	for platform := range platforms {
		out.Platforms = append(out.Platforms, platform)
	}
	sort.Float64s(out.Rates)
	sort.Slice(out.Platforms, func(i, j int) bool {
		return slices.Compare(utf16.Encode([]rune(out.Platforms[i])), utf16.Encode([]rune(out.Platforms[j]))) < 0
	})
	if f.Sort != "" {
		sortManagementRowsByRate(out.Rows, f.Model, f.Sort)
	}
	return out
}

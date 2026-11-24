package main

import (
	"encoding/json"
	"slices"
)

const tierUnknown = "unknown"

var managementTierFilterOptions = []string{"", "supported", "not_honored", "unsupported", tierUnknown}

func (f managementQueryFilters) hasServiceTiers() bool { return f.Fast != "" || f.Ultrafast != "" }

type managementTierPair struct {
	Fast      string `json:"fast"`
	Ultrafast string `json:"ultrafast"`
}

var unknownTierPair = managementTierPair{tierUnknown, tierUnknown}

func managementTierMatches(pairs []managementTierPair, f managementQueryFilters) bool {
	if !f.hasServiceTiers() {
		return true
	}
	matches := func(pair managementTierPair) bool {
		return (f.Fast == "" || pair.Fast == f.Fast) && (f.Ultrafast == "" || pair.Ultrafast == f.Ultrafast)
	}
	if len(pairs) == 0 {
		return matches(unknownTierPair)
	}
	return slices.ContainsFunc(pairs, matches)
}

func serviceTierFilterStatus(result serviceTierResult, now int64) string {
	if serviceTierDigest(result.Binding) && result.Binding == result.VerifiedBinding && serviceTierFresh(result.VerifiedAt, now) && result.Status != "unsupported" {
		return "supported"
	}
	if serviceTierDigest(result.Binding) && serviceTierFresh(result.CheckedAt, now) &&
		(result.Status == "not_honored" || result.Status == "unsupported") {
		return result.Status
	}
	return tierUnknown
}

func managementServiceTiers(in managementQueryInput, rows []managementQueryRow, f managementQueryFilters, now int64) map[string][]managementTierPair {
	// Tier jobs store the credential-bound serviceTierScope in Fingerprint.
	// Ordinary availability jobs use the unrelated configuredProbeFingerprint.
	type receiptKey struct{ source, provider, model, tier, scope string }
	checks := map[receiptKey]string{}
	for _, check := range in.Checks {
		if !isServiceTier(check.Kind) || !serviceTierDigest(check.Fingerprint) {
			continue
		}
		var result serviceTierResult
		if json.Unmarshal(check.Result, &result) != nil {
			continue
		}
		if status := serviceTierFilterStatus(result, now); status != tierUnknown {
			checks[receiptKey{check.Source, check.Provider, check.Model, check.Kind, check.Fingerprint}] = status
		}
	}
	if len(checks) == 0 {
		return nil
	}
	unavailable := map[string]bool{}
	for _, id := range in.UnavailableSources {
		unavailable[id] = true
	}
	for _, id := range in.UnavailableImports {
		unavailable[id] = true
	}
	joined := map[string][]subInstalledChannel{}
	for _, member := range in.Imports {
		// Only a current, unambiguously bound installation can qualify a site
		// row. Never borrow a diagnosis from a sibling group or a test key.
		if member.Kind != "configured" && member.ServiceTierScope != "" && member.AccountID != "" && member.GroupID > 0 {
			id := qualityGroupID(member.AccountID, member.GroupID)
			joined[id] = append(joined[id], member)
		}
	}
	out := map[string][]managementTierPair{}
	for _, row := range rows {
		var pairs []managementTierPair
		add := func(member subInstalledChannel, selected string, site bool) {
			if !serviceTierDigest(member.ServiceTierScope) || unavailable[member.SourceID] || unavailable[member.SourceName] {
				return
			}
			appendPair := func(model string) {
				pair := unknownTierPair
				if v := checks[receiptKey{member.SourceID, member.Provider, model, "fast", member.ServiceTierScope}]; v != "" {
					pair.Fast = v
				}
				if v := checks[receiptKey{member.SourceID, member.Provider, model, "ultrafast", member.ServiceTierScope}]; v != "" {
					pair.Ultrafast = v
				}
				if !slices.Contains(pairs, pair) {
					pairs = append(pairs, pair)
				}
			}
			if selected == "" {
				for _, model := range member.Models {
					appendPair(model)
				}
			} else if !site {
				if slices.Contains(member.Models, selected) {
					appendPair(selected)
				}
			} else {
				for _, model := range member.Models {
					upstream := member.ModelMappings[model]
					if upstream == selected || upstream == "" && model == selected {
						appendPair(model)
					}
				}
			}
		}
		if row.Configured != nil {
			for _, member := range row.Configured.Members {
				if f.AccountID == "" || f.AccountID == "__unassigned__" && len(member.AccountIDs) == 0 ||
					slices.Contains(member.AccountIDs, f.AccountID) || member.AccountID == f.AccountID {
					add(member.subInstalledChannel, f.Model, false)
				}
			}
		} else {
			for _, member := range joined[row.ID] {
				add(member, f.Model, true)
			}
		}
		if len(pairs) > 1 || len(pairs) == 1 && pairs[0] != unknownTierPair {
			out[row.ID] = pairs
		}
	}
	return out
}

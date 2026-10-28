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

// Independent prior facet/filter pipeline, including its repeated rate reads.
func previousFilterManagementQueryRows(rows []managementQueryRow, f managementQueryFilters, evidence managementFilterEvidence) managementFilteredRows {
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

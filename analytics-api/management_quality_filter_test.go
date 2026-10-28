package main

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

// Keep the prior complete-list lookup independent of the optimized lookup.
func previousManagementQuality(target *subTarget) string {
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

func TestManagementQualityLookupPreservesDuplicatesNilLegacyAndSharedPriority(t *testing.T) {
	if !slices.Contains(subModels, "gpt-6-astra") {
		t.Fatal("Astra legacy fallback requires the built-in Astra check")
	}
	rng := rand.New(rand.NewSource(271643))
	for test := 0; test < 1200; test++ {
		legacy := &subResult{Model: "legacy-other-name", CheckedAt: int64(test % 3), Verdict: "legacy", Quality: subProbe{ID: fmt.Sprint(test % 4)}}
		target := subTarget{Result: legacy}
		if test%3 == 0 {
			target.Result = nil
		}
		for i := 0; i < test%289; i++ {
			name := fmt.Sprintf("model-%d", i%23)
			if rng.Intn(7) == 0 {
				name = "gpt-6-astra"
			}
			var result *subResult
			if rng.Intn(3) != 0 {
				result = &subResult{Model: "unrelated-result-name", CheckedAt: int64(i % 3), Verdict: fmt.Sprint(i), Quality: subProbe{ID: fmt.Sprint(i % 4)}}
			}
			target.Models = append(target.Models, subModelResult{Model: name, State: fmt.Sprint(i % 5), Result: result})
		}
		// Explicit trailing nil must suppress the legacy result, even if an
		// earlier duplicate had a saved result. A shared verdict can still win.
		if test%11 == 0 {
			target.Models = append(target.Models, subModelResult{Model: "gpt-6-astra", Result: legacy}, subModelResult{Model: "gpt-6-astra"})
		}
		if test%5 != 0 {
			target.QualityCheck = &ChannelCheck{CheckedAt: int64(rng.Intn(3)), Verdict: "shared"}
			if test%4 != 0 {
				target.QualityCheck.QualityProbe = &subProbe{ID: fmt.Sprint(rng.Intn(4))}
			}
		}
		before := mustJSON(target)
		want := previousManagementQuality(&target)
		if got := managementQuality(&target); got != want || mustJSON(target) != before {
			t.Fatalf("case %d changed verdict or mutated evidence: got %q want %q", test, got, want)
		}
	}
}

func TestManagementQualityLookupKeepsExactSharedReceiptPrecedence(t *testing.T) {
	saved := &subResult{CheckedAt: 42, Verdict: "saved", Quality: subProbe{ID: "same"}}
	for _, tc := range []struct {
		name   string
		target subTarget
		want   string
	}{
		{"legacy", subTarget{Result: saved}, "saved"},
		{"nil_suppresses_legacy", subTarget{Result: saved, Models: []subModelResult{{Model: "gpt-6-astra", Result: saved}, {Model: "gpt-6-astra"}}}, ""},
		{"last_duplicate_wins", subTarget{Models: []subModelResult{{Model: "gpt-6-astra"}, {Model: "gpt-6-astra", Result: saved}}}, "saved"},
		{"same_receipt_and_clock", subTarget{Result: saved, QualityCheck: &ChannelCheck{CheckedAt: 42, Verdict: "shared", QualityProbe: &subProbe{ID: "same"}}}, "saved"},
		{"different_receipt", subTarget{Result: saved, QualityCheck: &ChannelCheck{CheckedAt: 42, Verdict: "shared", QualityProbe: &subProbe{ID: "different"}}}, "shared"},
		{"different_clock", subTarget{Result: saved, QualityCheck: &ChannelCheck{CheckedAt: 43, Verdict: "shared", QualityProbe: &subProbe{ID: "same"}}}, "shared"},
		{"no_shared_receipt", subTarget{Result: saved, QualityCheck: &ChannelCheck{CheckedAt: 42, Verdict: "shared"}}, "shared"},
		{"shared_survives_explicit_nil", subTarget{Result: saved, Models: []subModelResult{{Model: "gpt-6-astra"}}, QualityCheck: &ChannelCheck{CheckedAt: 42, Verdict: "shared", QualityProbe: &subProbe{ID: "same"}}}, "shared"},
		{"empty_identical_receipts", subTarget{Result: &subResult{Verdict: "saved"}, QualityCheck: &ChannelCheck{Verdict: "shared", QualityProbe: &subProbe{}}}, "saved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := mustJSON(tc.target)
			if got := managementQuality(&tc.target); got != tc.want || got != previousManagementQuality(&tc.target) || mustJSON(tc.target) != before {
				t.Fatalf("changed receipt precedence: got %q want %q", got, tc.want)
			}
		})
	}
}

var managementQualityFilterSink string

func BenchmarkManagementQualityLookup(b *testing.B) {
	for _, size := range []int{0, 33, 160, 288} {
		for _, position := range []string{"absent", "first", "last"} {
			target := subTarget{Result: &subResult{Verdict: "legacy"}}
			for i := 0; i < size; i++ {
				name := fmt.Sprintf("model-%d", i)
				if position == "first" && i == 0 || position == "last" && i == size-1 {
					name = "gpt-6-astra"
				}
				target.Models = append(target.Models, subModelResult{Model: name, Result: &subResult{Verdict: "saved"}})
			}
			for _, run := range []struct {
				name string
				fn   func(*subTarget) string
			}{{"previous_complete_list", previousManagementQuality}, {"reverse_lookup", managementQuality}} {
				b.Run(fmt.Sprintf("%d/%s/%s", size, position, run.name), func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						managementQualityFilterSink = run.fn(&target)
					}
				})
			}
		}
	}
}

func BenchmarkManagementQualityPage(b *testing.B) {
	targets := make([]subTarget, 749)
	for i := range targets {
		for j := 0; j < 288; j++ {
			name := fmt.Sprintf("model-%d", j)
			if j == i%288 {
				name = "gpt-6-astra"
			}
			targets[i].Models = append(targets[i].Models, subModelResult{Model: name, Result: &subResult{Verdict: "saved"}})
		}
	}
	for _, run := range []struct {
		name string
		fn   func(*subTarget) string
	}{{"previous_complete_list", previousManagementQuality}, {"reverse_lookup", managementQuality}} {
		b.Run(run.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for j := range targets {
					managementQualityFilterSink = run.fn(&targets[j])
				}
			}
		})
	}
}

package main

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// Keep the pre-optimization decoder as an independent behavior/performance
// baseline. It intentionally includes the discarded legacy reply in the JSON.
func legacyNormalizeManagementAccounts(accounts []subAccount) error {
	return normalizeManagementAccountsJSONBaseline(accounts, func(target subTarget) any { return compactSummary(target) })
}

// The immediately preceding production implementation, before rates bypassed
// the roundtrip. Keep all its normalization and partial-error behavior intact.
func previousNormalizeManagementAccounts(accounts []subAccount) error {
	return normalizeManagementAccountsJSONBaseline(accounts, compactManagementTargetMetadata)
}

func normalizeManagementAccountsJSONBaseline(accounts []subAccount, compact func(subTarget) any) error {
	for i := range accounts {
		for j := range accounts[i].Targets {
			target := &accounts[i].Targets[j]
			models := target.Models
			target.Models = nil
			raw, err := json.Marshal(compact(*target))
			if err != nil {
				return err
			}
			*target = subTarget{}
			if err := json.Unmarshal(raw, target); err != nil {
				return err
			}
			for k := range models {
				model := &models[k]
				model.Message = summaryMessage(model.Message)
				compactSubModelResult(model.Result)
				if model.Result != nil {
					model.Result.Availability.ID, model.Result.Quality.ID = "", ""
					model.Result.Availability.Message = summaryMessage(model.Result.Availability.Message)
					model.Result.Quality.Message = summaryMessage(model.Result.Quality.Message)
				}
			}
			target.Models, target.DetailsOmitted = models, true
		}
	}
	return nil
}

func managementNormalizeFixture() subTarget {
	probe := subProbe{ID: "private-id", CurlToken: "private-curl", Text: strings.Repeat("reply🙂", 1024), StartedAt: 123,
		RequestIDs: []string{"private-request"}, Status: "success", RequestedModel: "alias", ResponseModel: "upstream", ModelMatch: "match",
		Message: strings.Repeat("消息🙂", 80), TTFT: usageInt(0), ResponseCreatedMS: usageInt(1), FirstResponseMS: usageInt(2),
		Protocol: "responses", Duration: 100, HTTPStatus: 200, TerminalStatus: "complete",
		Usage: &subUsage{Status: "matched", LogID: 123, ActualCost: usageFloat(1), InputPrice: usageFloat(0),
			OutputPrice: usageFloat(3), InputTokens: usageInt(0), OutputTokens: usageInt(20), Message: "private receipt"}}
	return subTarget{GroupID: 7, KeyID: 42, Name: "group", State: "done", Rate: .2, Active: true, Platform: "openai", Message: strings.Repeat("🙂", 180),
		Result:     &subResult{Model: "alias", CheckedAt: 123, Verdict: "pass", Availability: probe, Quality: probe},
		ToolUse:    &subCapabilityResult{Status: "success", Message: "tool", Attempts: []subProbe{probe}},
		Compaction: &subCapabilityResult{Status: "success", Attempts: []subProbe{probe}},
		Models:     []subModelResult{{Model: "nil", State: "idle"}}}
}

func TestManagementNormalizeLegacyResultMatchesJSONBaselineAndClonesSharedEvidence(t *testing.T) {
	edge := managementNormalizeFixture().Result
	edge.CheckedAt, edge.Availability.Duration = 1<<53+1, 1<<53+1
	edge.Availability.TTFT = usageInt(1<<53 + 1)
	edge.Availability.Usage.InputTokens = usageInt(1<<53 + 1)
	edge.Model, edge.Availability.Message = "invalid\xff", strings.Repeat("\xff🙂", 180)
	nonfinite := managementNormalizeFixture().Result
	nonfinite.Quality.Usage.ActualCost = usageFloat(math.NaN())
	for _, result := range []*subResult{nil, {}, managementNormalizeFixture().Result, edge, nonfinite} {
		fixture := managementNormalizeFixture()
		fixture.Result = result
		before := mustJSON(fixture)
		old := []subAccount{{Targets: []subTarget{fixture}}}
		current := []subAccount{{Targets: []subTarget{fixture}}}
		if err := legacyNormalizeManagementAccounts(old); err != nil {
			t.Fatal(err)
		}
		if err := normalizeManagementAccounts(current); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(old, current) {
			t.Fatalf("legacy group summary changed: got=%s want=%s", mustJSON(current), mustJSON(old))
		}
		if mustJSON(fixture) != before {
			t.Fatal("shared result, usage or capability evidence was mutated")
		}
		if result != nil && current[0].Targets[0].Result == result {
			t.Fatal("group result still aliases the shared detail result")
		}
	}
}

func TestManagementNormalizeFiniteGuardCoversEveryUsageFloat(t *testing.T) {
	// A new receipt float must not accidentally disappear before the legacy
	// JSON validation step. Check the guard against all pointer-float fields.
	u := subUsage{}
	v := reflect.ValueOf(&u).Elem()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if field.Type() != reflect.TypeOf((*float64)(nil)) {
			continue
		}
		for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			field.Set(reflect.ValueOf(usageFloat(value)))
			if managementUsageJSONFinite(&u) {
				t.Fatal("nonfinite field omitted from guard", v.Type().Field(i).Name)
			}
		}
		field.SetZero()
	}
}

func BenchmarkManagementNormalizeLegacyGroups(b *testing.B) {
	for _, entry := range []struct {
		name      string
		normalize func([]subAccount) error
	}{{"json", legacyNormalizeManagementAccounts}, {"projected", normalizeManagementAccounts}} {
		b.Run(entry.name, func(b *testing.B) {
			fixture := managementNormalizeFixture()
			fixture.ToolUse, fixture.Compaction, fixture.Models = nil, nil, nil
			accounts := []subAccount{{Targets: make([]subTarget, 749)}}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for i := range accounts[0].Targets {
					accounts[0].Targets[i] = fixture
				}
				if err := entry.normalize(accounts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

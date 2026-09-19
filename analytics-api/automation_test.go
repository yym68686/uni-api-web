package main

import "testing"

func TestAutomationPolicyDefaultsAndValidation(t *testing.T) {
	p, err := automationPolicy(AutomationPolicy{})
	if err != nil || len(p.Metrics) != 4 || p.MinSuccessSamples != 100 || p.Action != "suggest" {
		t.Fatalf("defaults=%+v err=%v", p, err)
	}
	if _, err = automationPolicy(AutomationPolicy{Metrics: []string{"unknown"}, ConfidenceLevel: .95, LatencyQuantile: .5, RequireConsecutive: 1, MaxMoves: 1}); err == nil {
		t.Fatal("unknown metric accepted")
	}
	if _, err = automationPolicy(AutomationPolicy{Metrics: []string{"success"}, ConfidenceLevel: 1, LatencyQuantile: .5, RequireConsecutive: 1, MaxMoves: 1}); err == nil {
		t.Fatal("invalid confidence accepted")
	}
}

func TestAutomationDominanceUsesEveryConfiguredMetricAndThreshold(t *testing.T) {
	p := defaultAutomationPolicy()
	p.Metrics = []string{"latency", "success", "cache"}
	p.MinLatencySamples, p.MinSuccessSamples, p.MinCacheSamples = 50, 100, 50
	p.LatencyMinPercent, p.LatencyMinMS, p.SuccessMinPP, p.CacheMinPP = 10, 100, 2, 5
	a := automationMetric{Provider: "a", Latency: 1000, LatencyN: 100, Success: .999, SuccessN: 1000, Cache: .90, CacheN: 1000}
	b := automationMetric{Provider: "b", Latency: 1300, LatencyN: 100, Success: .90, SuccessN: 1000, Cache: .70, CacheN: 1000}
	if !dominates(a, b, p, 1.96) {
		t.Fatal("dominant channel rejected")
	}
	b.Success = .995
	if dominates(a, b, p, 1.96) {
		t.Fatal("confidence/threshold gate ignored")
	}
	p.Metrics = []string{"success"}
	b.Success = .90
	if !dominates(a, b, p, 1.96) {
		t.Fatal("selected metric should be the only gate")
	}
}

func TestAutomationAllHigherRule(t *testing.T) {
	p := defaultAutomationPolicy()
	p.Metrics = []string{"success"}
	p.MinSuccessSamples = 1
	p.SuccessMinPP = 1
	metrics := map[string]automationMetric{
		"a": {Provider: "a", Success: .95, SuccessN: 100},
		"b": {Provider: "b", Success: .99, SuccessN: 100},
		"c": {Provider: "c", Success: .995, SuccessN: 100},
	}
	if dominatesAllHigher([]string{"a", "b", "c"}, 2, metrics, p, 1.96) {
		t.Fatal("channel did not dominate the first higher priority channel")
	}
}

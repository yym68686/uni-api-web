package main

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestAutomationPolicyValidation(t *testing.T) {
	p := defaultAutomationPolicy()
	if _, e := automationPolicy(p); e != nil {
		t.Fatal(e)
	}
	for _, modify := range []func(*AutomationPolicy){func(p *AutomationPolicy) { p.Metrics = nil }, func(p *AutomationPolicy) { p.Metrics = []string{"success", "success"} }, func(p *AutomationPolicy) { p.ConfidenceLevel = 1 }, func(p *AutomationPolicy) { p.QualityConcurrency = 0 }, func(p *AutomationPolicy) { p.RequireConsecutive = 0 }, func(p *AutomationPolicy) { p.Stream = "all" }, func(p *AutomationPolicy) { p.MaxMoves = 1000000 }} {
		bad := p
		modify(&bad)
		if _, e := automationPolicy(bad); e == nil {
			t.Fatalf("invalid policy accepted: %+v", bad)
		}
	}
}
func metricFixture(name string, latency, success, cache float64) automationMetric {
	return automationMetric{Provider: name, Upstream: "m", Eligible: true, Latency: latency, Success: success, Cache: cache, Quality: 1, LatencyCI: [2]float64{latency, latency}, SuccessCI: [2]float64{success, success}, CacheCI: [2]float64{cache, cache}, QualityCI: [2]float64{1, 1}, LatencyN: 100, SuccessN: 100, CacheN: 100, QualityN: 100}
}
func TestAutomationParetoNoTradeoffsAndOneMaterialImprovement(t *testing.T) {
	p := defaultAutomationPolicy()
	a := metricFixture("a", 1000, .99, .9)
	b := metricFixture("b", 1200, .99, .9)
	if !dominates(a, b, p, 0) {
		t.Fatal("equal other dimensions must allow material latency gain")
	}
	a.Cache = .89
	if dominates(a, b, p, 0) {
		t.Fatal("better latency must not offset worse cache")
	}
	a.Cache = .9
	a.Latency = 1199
	a.LatencyCI = [2]float64{1199, 1199}
	if dominates(a, b, p, 0) {
		t.Fatal("1 ms must not trigger adjustment")
	}
	a.Latency = 1000
	a.LatencyCI = [2]float64{900, 1250}
	if dominates(a, b, p, 0) {
		t.Fatal("uncertain latency must not promote")
	}
	a = metricFixture("a", 1000, .99, .9)
	a.SuccessN = 99
	if dominates(a, b, p, 0) {
		t.Fatal("insufficient denominator")
	}
	a = metricFixture("a", 1000, .99, .9)
	a.Upstream = "other"
	if dominates(a, b, p, 0) {
		t.Fatal("mixed upstream model")
	}
	a = metricFixture("a", 1000, .99, .9)
	a.Reason = "stale"
	if dominates(a, b, p, 0) {
		t.Fatal("stale data")
	}
}
func TestAutomationPlanPreservesMissingAndDisabledChannels(t *testing.T) {
	p := defaultAutomationPolicy()
	p.MaxMoves = 10
	metrics := map[string]automationMetric{"a": metricFixture("a", 1500, .99, .9), "b": metricFixture("b", 1200, .99, .9), "c": metricFixture("c", 1000, .99, .9)}
	after, moves, _ := automationPlan([]string{"a", "missing", "b", "c"}, metrics, p)
	if fmt.Sprint(after) != "[a missing c b]" || len(moves) != 1 {
		t.Fatal(after, moves)
	}
	m := metrics["b"]
	m.Eligible = false
	metrics["b"] = m
	after, moves, _ = automationPlan([]string{"a", "b", "c"}, metrics, p)
	if fmt.Sprint(after) != "[a b c]" || len(moves) != 0 {
		t.Fatal("disabled moved", after, moves)
	}
}
func TestAutomationIntervalsRetainWeightingAndRejectUnknownTails(t *testing.T) {
	ratio, ci, ok := cacheInterval([]cacheBlock{{1000, 900}, {10, 0}, {1000, 900}, {10, 0}}, .95)
	if !ok || math.Abs(ratio-1800.0/2020) > 1e-9 || ci[0] > ratio || ci[1] < ratio {
		t.Fatal(ratio, ci, ok)
	}
	if _, _, ok = cacheInterval([]cacheBlock{{1000, 900}}, .95); ok {
		t.Fatal("one minute falsely precise")
	}
	if _, _, ok = quantileInterval([]float64{1, 2, 3}, .95, confidenceZ(.95)); ok {
		t.Fatal("unbounded quantile reported as finite")
	}
	lo, hi := wilson(0, 100, confidenceZ(.95))
	if lo != 0 || hi <= 0 {
		t.Fatal(lo, hi)
	}
	if math.Abs(confidenceZ(.95)-1.95996398454) > 1e-8 {
		t.Fatal("custom confidence z")
	}
}
func TestAutomationMetricsUseScopedResponseCreatedAndWindow(t *testing.T) {
	e := stateTestEngine(t)
	now := time.Now()
	task := AutomationTask{SourceID: "test", KeyID: "key", Model: "m", Range: "1h", Policy: defaultAutomationPolicy()}
	task.Policy.Metrics = []string{"latency", "success", "cache"}
	catalog := []automationChannel{{Provider: "a", Upstream: "m", Model: "m", Eligible: true}}
	facts := []Fact{}
	input, cached := int64(100), int64(70)
	created, text := 200.0, 900.0
	for i := 0; i < 100; i++ {
		at := now.Add(-time.Duration(i) * time.Second).UnixMilli()
		for _, kind := range []string{"attempt", "request"} {
			f := Fact{Schema: 1, SourceID: "test", EventID: fmt.Sprintf("%s-%d", kind, i), Kind: kind, AtMS: at, KeyID: "key", Provider: "a", Model: "m", UpstreamModel: "m", Endpoint: "/v1/responses", Stream: true, Outcome: "success"}
			if kind == "attempt" {
				f.ResponseCreatedMS = &created
				f.FirstTextMS = &text
			} else {
				f.InputTokens = &input
				f.CacheReadTokens = &cached
			}
			facts = append(facts, f)
		}
	}
	foreign := facts[0]
	foreign.EventID = "foreign"
	foreign.KeyID = "wrong"
	foreign.Outcome = "failed"
	facts = append(facts, foreign)
	old := facts[0]
	old.EventID = "old"
	old.AtMS = now.Add(-2 * time.Hour).UnixMilli()
	old.Outcome = "failed"
	facts = append(facts, old)
	if err := e.Import(context.Background(), "automation-fixture", "v1", facts); err != nil {
		t.Fatal(err)
	}
	s := &Service{engine: e}
	m, err := s.automationMetrics(context.Background(), task, catalog)
	if err != nil {
		t.Fatal(err)
	}
	a := m["a"]
	if a.SuccessN != 100 || a.LatencyN != 100 || a.Latency != 200 || a.CacheN != 100 || math.Abs(a.Cache-.7) > 1e-9 || a.Reason != "" {
		t.Fatalf("wrong scope or statistic: %+v", a)
	}
}

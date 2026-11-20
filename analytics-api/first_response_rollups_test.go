package main

import (
	"context"
	"testing"
	"time"
)

func TestFirstResponseRollupsSeparateSuccessAndFailure(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	base := Fact{Schema: 1, SourceID: "source", KeyID: "caller", Kind: "attempt", Provider: "mixed", Model: "alias", UpstreamModel: "actual", Endpoint: "/v1/responses", Stream: true}
	add := func(id, kind, provider, outcome string, at time.Time, ms *float64) Fact {
		f := base
		f.EventID, f.Kind, f.Provider, f.Outcome, f.AtMS, f.ResponseCreatedMS = id, kind, provider, outcome, at.UnixMilli(), ms
		return f
	}
	recent, older := now.Add(-2*time.Minute), now.Add(-72*time.Hour)
	fast, slow, middle, ignored := 200.0, 7500.0, 3000.0, 10.0
	no := false
	demoted := add("demoted", "attempt", "mixed", "success", recent.Add(time.Second), &middle)
	demoted.ResponseCompleted = &no
	otherKey := add("other-key", "attempt", "mixed", "success", recent, &ignored)
	otherKey.KeyID = "other"
	facts := []Fact{
		add("finished", "attempt", "mixed", "completed", older, &middle),
		add("success", "attempt", "mixed", "success", recent, &slow),
		add("success-without-clock", "attempt", "mixed", "success", recent.Add(time.Second), nil),
		add("failed", "attempt", "mixed", "failed", recent, &fast),
		demoted, otherKey,
		add("cancelled", "attempt", "mixed", "hedge_cancelled", recent, &ignored),
		add("skipped", "attempt", "mixed", "skipped", recent, &ignored),
		add("request-copy", "request", "mixed", "success", recent, &ignored),
		add("only-failed", "attempt", "all-failed", "failed", recent, &fast),
	}
	if err := e.Import(ctx, "first-response-timing", "one", facts); err != nil {
		t.Fatal(err)
	}
	check := func(stats map[string]any, key string, n int64, p50, p95 any) {
		t.Helper()
		got := stats[key].(map[string]any)
		if got["sample_count"] != n || got["p50_ms"] != p50 || got["p95_ms"] != p95 {
			t.Fatalf("%s: %#v, want %d samples and %v / %v", key, got, n, p50, p95)
		}
	}
	filter := QueryFilter{Range: "all", Timeseries: true, To: now.Unix(), SourceID: "source", KeyID: "caller", Model: "alias", UpstreamModel: "actual", Endpoint: "/v1/responses", Stream: "true", Provider: "mixed"}
	result, err := e.Query(ctx, filter)
	if err != nil || len(result.Data) != 1 {
		t.Fatal(err, result.Data)
	}
	row := result.Data[0]
	check(row.Stats, "successful_response_created", 2, middle, slow)
	check(row.Stats, "failed_response_created", 2, fast, middle)
	if row.Stats["success_rate_denominator"] != int64(5) || *row.Stats["successful_response_created"].(map[string]any)["last_ms"].(*float64) != slow {
		t.Fatalf("unexpected attempts or latest clock: %#v", row.Stats)
	}
	if len(row.Points) != 2 {
		t.Fatalf("expected a historical day and recent bucket: %#v", row.Points)
	}
	check(row.Points[0], "successful_response_created", 1, middle, middle)
	check(row.Points[1], "successful_response_created", 1, slow, slow)
	filter.Range = "15m"
	filter.Provider = "all-failed"
	result, err = e.Query(ctx, filter)
	if err != nil || len(result.Data) != 1 || len(result.Data[0].Points) != 1 {
		t.Fatal(err, result.Data)
	}
	row = result.Data[0]
	check(row.Stats, "successful_response_created", 0, nil, nil)
	check(row.Stats, "failed_response_created", 1, fast, fast)
	check(row.Points[0], "successful_response_created", 0, nil, nil)
	check(row.Points[0], "response_created", 1, fast, fast)

	s := &Service{engine: e}
	policy := defaultAutomationPolicy()
	policy.Metrics = []string{"latency", "success"}
	metrics, err := s.automationMetrics(ctx, AutomationTask{SourceID: "source", KeyID: "caller", Model: "alias", Range: "15m", Policy: policy}, []automationChannel{{Provider: "mixed", Upstream: "actual", Eligible: true}, {Provider: "all-failed", Upstream: "actual", Eligible: true}})
	if err != nil {
		t.Fatal(err)
	}
	if m := metrics["mixed"]; m.LatencyN != 1 || m.Latency != slow || m.SuccessN != 4 || m.Success != .5 {
		t.Fatalf("automation mixed the latency and success populations: %#v", m)
	}
	if m := metrics["all-failed"]; m.LatencyN != 0 || m.SuccessN != 1 || m.Success != 0 {
		t.Fatalf("automation used failed latency: %#v", m)
	}
}

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

func TestNativeFirstTextRollupsOnlyUseScopedSuccessfulAttempts(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	base := Fact{Schema: 1, Kind: "attempt", SourceID: "source", KeyID: "caller", Provider: "native", Model: "claude-opus-5-5", UpstreamModel: "claude-opus-5-5", Endpoint: "/v1/messages", Stream: true, Outcome: "success"}
	add := func(id string, at time.Time, ms float64) Fact {
		f := base
		f.EventID, f.AtMS, f.FirstTextMS = id, at.UnixMilli(), &ms
		return f
	}
	recent, older := now.Add(-2*time.Minute), now.Add(-72*time.Hour)
	completed := add("completed", recent.Add(time.Second), 500)
	completed.Outcome = "completed"
	requestCopy := add("request-copy", recent, 20)
	requestCopy.Kind = "request"
	failed := add("failed", recent, 7500)
	failed.Outcome = "failed"
	cancelled := add("cancelled", recent, 10000)
	cancelled.Outcome = "hedge_cancelled"
	wrongStream := add("nonstream", recent, 50)
	wrongStream.Stream = false
	wrongKey := add("other-key", recent, 50)
	wrongKey.KeyID = "other"
	toolOnly := add("no-text", recent, 20)
	toolOnly.FirstOutputMS, toolOnly.FirstTextMS = toolOnly.FirstTextMS, nil
	facts := []Fact{add("older", older, 100), add("recent", recent, 200), completed, requestCopy, failed, cancelled, wrongStream, wrongKey, toolOnly}
	if err := e.Import(ctx, "native-first-text", "one", facts); err != nil {
		t.Fatal(err)
	}
	check := func(stats map[string]any, n int64, p50, p95, last float64) {
		t.Helper()
		created := stats["successful_response_created"].(map[string]any)
		got := stats["successful_first_text"].(map[string]any)
		lastValue, ok := got["last_ms"].(*float64)
		if created["sample_count"] != int64(0) || got["sample_count"] != n || got["p50_ms"] != p50 || got["p95_ms"] != p95 || !ok || lastValue == nil || *lastValue != last {
			t.Fatalf("created: %#v; text: %#v; want %d text samples, %v / %v, last %v", created, got, n, p50, p95, last)
		}
	}
	filter := QueryFilter{Range: "24h", Timeseries: true, To: now.Unix(), SourceID: base.SourceID, KeyID: base.KeyID, Provider: base.Provider, Model: base.Model, Endpoint: base.Endpoint, Stream: "true"}
	query := func(wantPoints int) AnalyticChannel {
		t.Helper()
		result, err := e.Query(ctx, filter)
		if err != nil || len(result.Data) != 1 || len(result.Data[0].Points) != wantPoints {
			t.Fatal(err, result.Data)
		}
		return result.Data[0]
	}
	row := query(1)
	check(row.Stats, 2, 200, 500, 500)
	check(row.Points[0], 2, 200, 500, 500)
	if row.Stats["success"] != float64(3) || row.Stats["first_text"].(map[string]any)["sample_count"] != int64(4) {
		t.Fatalf("unexpected attempt populations: %#v", row.Stats)
	}
	filter.Range = "all"
	row = query(2)
	check(row.Stats, 3, 200, 500, 500)
	check(row.Points[0], 1, 100, 100, 100)
	check(row.Points[1], 2, 200, 500, 500)
}

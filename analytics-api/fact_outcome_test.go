package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestResponsesCompletionEvidenceControlsSuccessMetrics(t *testing.T) {
	for _, window := range []string{"1h", "7d"} {
		t.Run(window, func(t *testing.T) {
			e := stateTestEngine(t)
			at := time.Now().Add(-2 * time.Minute)
			if window == "7d" {
				at = at.Add(-72 * time.Hour)
			}
			yes, no := true, false
			text := 1.0
			cases := []struct {
				outcome, terminal string
				completed         *bool
				want              string
			}{
				{"success", "completed", &yes, "success"}, // Tool-only completion needs no text.
				{"success", "incomplete", &no, "failed"},
				{"success", "protocol_error", &no, "failed"}, // HTTP 200 + text + EOF.
				{"success", "", &no, "failed"},
				{"incomplete", "", nil, "failed"},
				{"failed", "completed", &yes, "failed"}, // Never promote another failure.
				{"success", "", nil, "success"},         // Legacy: missing evidence is not proof of EOF.
				{"client_cancelled", "", &no, "client_cancelled"},
				{"hedge_cancelled", "", &no, "hedge_cancelled"},
				{"skipped", "", &no, "skipped"},
			}
			var facts []Fact
			for i, c := range cases {
				for _, kind := range []string{"attempt", "request"} {
					f := Fact{Schema: 1, SourceID: "test", EventID: fmt.Sprintf("%s-%d", kind, i), Kind: kind, AtMS: at.UnixMilli(), KeyID: "key", Provider: "p", Model: "m", UpstreamModel: "m", Endpoint: "/v1/responses", Stream: true, Status: 200, Outcome: c.outcome, TerminalKind: c.terminal, ResponseCompleted: c.completed}
					if i == 1 {
						f.FailureReason = "responses_max_output_tokens"
					}
					if i == 2 {
						f.FirstOutputMS = &text
						f.FirstTextMS = &text
					}
					if got := factMetricOutcome(f); got != c.want {
						t.Fatalf("%d: got %s want %s", i, got, c.want)
					}
					facts = append(facts, f)
				}
			}
			ctx := context.Background()
			if err := e.Import(ctx, "completion-fixture", "v1", facts); err != nil {
				t.Fatal(err)
			}
			// Replay must not double-count the final request and its attempt.
			if err := e.Import(ctx, "completion-fixture", "v1", facts); err != nil {
				t.Fatal(err)
			}
			q, err := e.Query(ctx, QueryFilter{Range: window, SourceID: "test", Provider: "p", Model: "m", KeyID: "key", Endpoint: "/v1/responses", Stream: "true", Timeseries: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(q.Data) != 1 {
				t.Fatalf("channels: %+v", q.Data)
			}
			s := q.Data[0].Stats
			if s["success"] != float64(2) || s["failed"] != float64(5) || s["success_rate"] != 2.0/7.0 {
				t.Fatalf("channel: %+v", s)
			}
			if len(q.Data[0].Points) != 1 {
				t.Fatal(q.Data[0].Points)
			}
			reasons := s["failure_reasons"].(map[string]any)
			for _, key := range []string{"responses_max_output_tokens", "responses_incomplete", "missing_response_completed", "protocol_error", "unknown"} {
				if reasons[key] != float64(1) {
					t.Fatalf("failure counts: %+v", reasons)
				}
			}
			point := q.Data[0].Points[0]
			if point["success"] != int64(2) || point["failed"] != int64(5) || point["success_rate"] != 2.0/7.0 {
				t.Fatalf("trend: %+v", point)
			}
			policy := defaultAutomationPolicy()
			policy.Metrics = []string{"success"}
			metrics, err := (&Service{engine: e}).automationMetrics(ctx, AutomationTask{SourceID: "test", KeyID: "key", Model: "m", Range: window, Policy: policy}, []automationChannel{{Provider: "p", Upstream: "m", Model: "m", Eligible: true}})
			if err != nil {
				t.Fatal(err)
			}
			if metrics["p"].SuccessN != 7 || metrics["p"].Success != 2.0/7.0 {
				t.Fatalf("automation: %+v", metrics["p"])
			}
		})
	}
}

func TestResponsesCompletionEvidenceDoesNotReclassifyOtherProtocols(t *testing.T) {
	no := false
	for _, f := range []Fact{
		{Kind: "attempt", Endpoint: "/v1/chat/completions", Stream: true, Outcome: "success", ResponseCompleted: &no},
		{Kind: "request", Endpoint: "/v1/responses", Stream: false, Outcome: "success", ResponseCompleted: &no},
		{Kind: "dispatch", Endpoint: "/v1/responses", Stream: true, Outcome: "", ResponseCompleted: &no},
	} {
		if factMetricOutcome(f) != f.Outcome {
			t.Fatal(f)
		}
	}
}

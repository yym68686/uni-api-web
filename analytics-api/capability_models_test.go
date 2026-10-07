package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCapabilityCandidatesOnlyIncludeGPTAndCodex(t *testing.T) {
	models := []subModelResult{}
	for _, name := range []string{"claude-opus-4-8", "gemini-3.1-pro", "deepseek", "custom-model", "gpt-6-astra", "codex-auto-review", "gpt-5.6-terra", "gpt-6-astra"} {
		models = append(models, subModelResult{Model: name, State: "done", Result: &subResult{Availability: subProbe{Status: "success"}}})
	}
	models = append(models, subModelResult{Model: "gpt-failed", State: "done", Result: &subResult{Availability: subProbe{Status: "error"}}})
	if got := subCompactionModels(models); !reflect.DeepEqual(got, []string{"gpt-5.6-terra", "gpt-6-astra", "codex-auto-review"}) {
		t.Fatal(got)
	}
}
func TestIneligibleCapabilitiesNeverSendRequestsIncludingOldQueuedJobs(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer upstream.Close()
	for _, model := range []string{"claude-opus-4-8", "gemini-3.1-pro", "deepseek", "custom-model", "vendor/gpt-6-astra"} {
		for _, probe := range []func(context.Context, *http.Client, string, string, string) subProbe{subProbeToolUse, subProbeCompaction} {
			result := probe(context.Background(), upstream.Client(), upstream.URL, "fixture", model)
			if result.Status != "not_applicable" || result.ID != "" || result.CurlToken != "" {
				t.Fatalf("unexpected skipped result: %+v", result)
			}
		}
		var result any
		if err := (&Service{}).runConfiguredCheck(context.Background(), &configuredCheck{Kind: "tool-use", Model: model}, "", "", false, &result); err != nil {
			t.Fatal(err)
		}
		if result.(subCapabilityResult).Status != "not_applicable" {
			t.Fatal("old queued job was not skipped")
		}
		for _, kind := range []string{"compaction", "tool-use"} {
			r := httptest.NewRequest("POST", "/v1/channel-management/checks", strings.NewReader(mustJSON(map[string]any{"kind": kind, "targets": []any{map[string]any{"source_id": "unused", "provider": "unused", "models": []string{model}}}})))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			(&Service{}).queueConfiguredChecks(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("accepted ineligible model %s for %s", model, kind)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("sent %d disallowed requests", calls.Load())
	}
}

func TestCapabilityDispatcherSkipsOtherSuccessfulModels(t *testing.T) {
	service, _, src := bindingFixture(t, "https://unused.example")
	const run = "capability-dispatch"
	_, err := service.control.db.Exec(`INSERT INTO console_configured_checks(source_id,provider,kind,source_target,state,run_id) VALUES($1,'fixture','tool-use','fixture','running',$2)`, src.ID, run)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	err = service.expandConfiguredToolChecks(context.Background(), &configuredCheck{Source: src.ID, Provider: "fixture"}, "fixture", run, map[string]bool{"gpt-6-astra": true, "codex-auto-review": true, "claude-opus-4-8": true, "gemini-3.1-pro": true, "deepseek": true}, &result)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := service.control.db.Query(`SELECT model FROM console_configured_checks WHERE source_id=$1 AND model<>'' ORDER BY model`, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var models []string
	for rows.Next() {
		var model string
		if err = rows.Scan(&model); err != nil {
			t.Fatal(err)
		}
		models = append(models, model)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(models, []string{"codex-auto-review", "gpt-6-astra"}) {
		t.Fatal(models)
	}
}

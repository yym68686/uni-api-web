package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDrawerTimeseriesKeepsScopedAttemptHistograms(t *testing.T) {
	for _, window := range []string{"1h", "7d"} {
		t.Run(window, func(t *testing.T) {
			e, err := OpenEngine(filepath.Join(t.TempDir(), "drawer.duckdb"), Config{Timezone: "UTC"})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			now := time.Now().UTC().Truncate(time.Minute)
			at := now.Add(-10 * time.Minute)
			if window == "7d" {
				at = now.Add(-72 * time.Hour)
			}
			base := Fact{Schema: 1, SourceID: "chosen", KeyID: "caller", Provider: "native", Model: "alias", UpstreamModel: "actual", Kind: "attempt", Outcome: "success", Stream: true, Endpoint: "/v1/responses", AtMS: at.UnixMilli()}
			facts := []Fact{}
			for i, latency := range []float64{100, 100, 100, 1000} {
				f := base
				f.EventID = fmt.Sprintf("latency-%d", i)
				f.ResponseCreatedMS = &latency
				text := latency + 100
				f.FirstTextMS = &text
				if i == 3 {
					f.Outcome = "failed"
				}
				facts = append(facts, f)
			}
			for _, outcome := range []string{"cancelled", "client_cancelled", "hedge_cancelled", "skipped"} {
				f := base
				f.EventID = outcome
				f.Outcome = outcome
				facts = append(facts, f)
			}
			// A winning request fact can also carry clocks; only attempts belong on this curve.
			f := base
			f.EventID = "request-copy"
			f.Kind = "request"
			high := 999999.0
			f.ResponseCreatedMS = &high
			f.FirstTextMS = &high
			facts = append(facts, f)
			for _, dimension := range []string{"source", "key", "provider", "model", "upstream", "endpoint", "stream"} {
				f = base
				f.EventID = "other-" + dimension
				f.ResponseCreatedMS = &high
				switch dimension {
				case "source":
					f.SourceID = "other"
				case "key":
					f.KeyID = "other"
				case "provider":
					f.Provider = "other"
				case "model":
					f.Model = "other"
				case "upstream":
					f.UpstreamModel = "other"
				case "endpoint":
					f.Endpoint = "/v1/chat/completions"
				case "stream":
					f.Stream = false
				}
				facts = append(facts, f)
			}
			if err = e.Import(context.Background(), "fixture.jsonl", "one", facts); err != nil {
				t.Fatal(err)
			}
			result, err := e.Query(context.Background(), QueryFilter{SourceID: "chosen", KeyID: "caller", Provider: "native", Model: "alias", UpstreamModel: "actual", Endpoint: "/v1/responses", Stream: "true", Range: window, Timeseries: true, To: now.Unix()})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Data) != 1 || len(result.Data[0].Points) != 1 {
				t.Fatal("unexpected series", result.Data)
			}
			p := result.Data[0].Points[0]
			if p["success"] != int64(3) || p["failed"] != int64(1) || p["success_rate"] != .75 || p["success_rate_denominator"] != int64(4) {
				t.Fatal("incorrect attempt rate", p)
			}
			latency := p["response_created"].(map[string]any)
			if latency["sample_count"] != int64(4) || latency["mean_ms"] != 325.0 || latency["p50_ms"] != 100.0 || latency["p95_ms"] != 1000.0 {
				t.Fatal("histograms not merged correctly", latency)
			}
			text := p["first_text"].(map[string]any)
			if text["sample_count"] != int64(4) || text["mean_ms"] != 425.0 {
				t.Fatal("text clocks mixed", text)
			}
			if window == "7d" && result.BucketSeconds < 86400 {
				t.Fatal("daily range duplicated minute data")
			}
		})
	}
}

func TestDrawerIndependentChecksTargetInstalledChannels(t *testing.T) {
	s, account, src := bindingFixture(t, "https://unrelated.test")
	var availability, quality, toolCalls, compactCalls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/observability/runtime":
			writeJSON(w, 200, map[string]any{"capabilities": map[string]bool{"targeted_responses": true, "targeted_unconfigured_models": true}})
		case "/v1/channel-settings/providers":
			writeJSON(w, 200, map[string]any{"providers": []configuredProvider{{Provider: "installed", Temporary: true, Base: "https://upstream.test/v1/responses", API: "secret"}}})
		case "/v1/model-channels":
			writeJSON(w, 200, map[string]any{"data": []any{map[string]string{"provider": "installed", "model": checkModel}, map[string]string{"provider": "installed", "model": "alias", "upstream_model": "actual"}}})
		case "/v1/responses":
			if r.Header.Get("X-Uni-API-Provider") != "installed" {
				t.Error("not directed to exact channel")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			raw := mustJSON(body)
			switch {
			case strings.Contains(raw, "compaction_trigger"):
				compactCalls.Add(1)
				compactSSE(w, fmt.Sprint(body["model"]), `[{"type":"compaction","encrypted_content":"opaque"}]`)
			case strings.Contains(raw, "TOOL_PROBE_OK"):
				toolCalls.Add(1)
				if body["model"] != "alias" {
					t.Error("lost public alias", body["model"])
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, strings.ReplaceAll(strings.ReplaceAll(toolUseSSE(nil, []toolUseItem{validToolUseItem()}), `data: {"response":{"id":"tool-response"}}`, `data: {"type":"response.created","response":{"id":"tool-response"}}`), `data: {"response":{"id":"tool-response","model"`, `data: {"type":"response.completed","response":{"id":"tool-response","model"`))
			case strings.Contains(raw, "say test"):
				availability.Add(1)
				subSSE(w, "test", fmt.Sprint(body["model"]))
			default:
				quality.Add(1)
				if body["model"] != checkModel {
					t.Error("wrong quality model")
				}
				subSSE(w, "21", checkModel)
			}
		default:
			t.Errorf("unexpected side effect %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	src.Base = gateway.URL
	if _, err := s.control.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_channel_checks WHERE source_id=$1`, src.ID) })
	token, _ := s.control.newSession(context.Background(), account.Owner)
	queue := func(kind, model string, want int) {
		t.Helper()
		models := []string{}
		if model != "" {
			models = append(models, model)
		}
		r := httptest.NewRequest("POST", "/v1/channel-management/checks", strings.NewReader(mustJSON(map[string]any{"kind": kind, "targets": []any{map[string]any{"source_id": src.ID, "provider": "installed", "models": models}}})))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		var out struct{ Queued int }
		json.Unmarshal(w.Body.Bytes(), &out)
		if out.Queued != want {
			t.Fatal("wrong queue count", out.Queued, want)
		}
	}
	queue("availability", checkModel, 1)
	queue("availability", checkModel, 0)
	queue("quality", checkModel, 1) // Separate job: availability must not gray out quality.
	queue("tool-use", "alias", 1)
	queue("compaction", "", 1)
	for range 4 {
		if !s.configuredCheckOne(context.Background()) {
			t.Fatal("missing independent job")
		}
	}
	if availability.Load() != 1 || quality.Load() != 1 || toolCalls.Load() != 1 || compactCalls.Load() != 1 {
		t.Fatal("unexpected paid calls", availability.Load(), quality.Load(), toolCalls.Load(), compactCalls.Load())
	}
	var state, verdict string
	if err := s.control.db.QueryRow(`SELECT state,result->>'verdict' FROM console_configured_checks WHERE source_id=$1 AND kind='availability'`, src.ID).Scan(&state, &verdict); err != nil || state != "done" || verdict != "not_applicable" {
		t.Fatal(state, verdict, err)
	}
	var n int
	s.control.db.QueryRow(`SELECT count(*) FROM console_quality_history WHERE source_id=$1`, src.ID).Scan(&n)
	if n != 1 {
		t.Fatal("availability polluted quality history", n)
	}
	// Existing inventory behavior is preserved; only diagnostics include temporary providers.
	providers, err := configuredProviders(context.Background(), src)
	if err != nil || len(providers) != 0 {
		t.Fatal("temporary provider leaked into inventory", providers, err)
	}
}

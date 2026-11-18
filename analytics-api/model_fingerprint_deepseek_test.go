package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDeepSeekFingerprintReferenceMapping(t *testing.T) {
	const model = "deepseek-v4.1-flash-1017"
	want := fingerprintReference{ClassID: model, Effort: "low"}
	if !fingerprintModel(model) || fingerprintModels[model] != want {
		t.Fatal("missing exact low-effort DeepSeek reference", fingerprintModels[model])
	}
	for _, target := range []fingerprintTarget{{Source: "source", Provider: "native", Model: model}, {Account: "account", Group: 1, Model: model}} {
		if !target.valid() {
			t.Fatal("DeepSeek target rejected", target)
		}
	}
	for _, alias := range []string{"deepseek-flash", "deepseek-v4.1-flash"} {
		if fingerprintModel(alias) {
			t.Fatal("another request model must not inherit the 1017 identity", alias)
		}
	}
}

func TestDeepSeekFingerprintConfiguredAutomaticQueue(t *testing.T) {
	const model = "deepseek-v4.1-flash-1017"
	for _, test := range []struct {
		name, kind string
		available  bool
		queued     int
	}{
		{"successful model check", "model", true, 1},
		{"failed model check", "model", false, 0},
		{"standalone availability", "availability", true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer gateway-secret" {
					t.Error("wrong gateway credential")
				}
				switch r.URL.Path {
				case "/v1/observability/runtime":
					writeJSON(w, 200, map[string]any{"capabilities": map[string]bool{"targeted_responses": true}})
				case "/v1/channel-settings/providers":
					writeJSON(w, 200, map[string]any{"providers": []configuredProvider{{Provider: "native", Base: "https://upstream.example/v1", API: "upstream-secret"}}})
				case "/v1/model-channels":
					writeJSON(w, 200, map[string]any{"data": []any{map[string]string{"provider": "native", "model": model}}})
				case "/v1/responses":
					calls.Add(1)
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if r.Method != "POST" || r.Header.Get("X-Uni-API-Provider") != "native" || body["model"] != model {
						t.Error("wrong targeted availability request")
					}
					if !test.available {
						writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"message": "offline fixture"}})
						return
					}
					subSSE(w, "test", model)
				default:
					t.Error("unexpected request", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer gateway.Close()
			s, _, src := bindingFixture(t, "https://site.example")
			src.Base = gateway.URL
			ctx := context.Background()
			if _, err := s.control.saveSource(ctx, src, false); err != nil {
				t.Fatal(err)
			}
			check := configuredCheck{Source: src.ID, Provider: "native", Kind: test.kind, Model: model}
			var result any
			if err := s.runConfiguredCheck(ctx, &check, controlTarget(src), randomID(), false, &result); err != nil {
				t.Fatal(err)
			}
			var queued int
			if err := s.control.db.QueryRow(`SELECT count(*) FROM console_model_fingerprints WHERE source_id=$1 AND provider='native' AND model=$2 AND state='queued'`, src.ID, model).Scan(&queued); err != nil {
				t.Fatal(err)
			}
			if queued != test.queued || calls.Load() != 1 {
				t.Fatalf("queued=%d want=%d calls=%d; fingerprint collection must remain asynchronous", queued, test.queued, calls.Load())
			}
		})
	}
}

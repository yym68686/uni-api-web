package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFullConfiguredModelCheckQueuesEachApplicableCapability(t *testing.T) {
	ctx := context.Background()
	store, err := newControlStore(isolatedControlTestDSN(t), strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	src := controlSource{sourceView: sourceView{ID: "suite", Name: "fixture", Base: "https://fixture.test"}, Key: "fixture"}
	if _, err := store.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	token, err := store.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{control: store}
	scope := strings.Repeat("a", 64)
	r := httptest.NewRequest("POST", "/v1/channel-management/checks", strings.NewReader(
		`{"kind":"check","full_model_check":true,"targets":[{"source_id":"suite","provider":"group","models":["gpt-6.2-sol","other"],"tier_models":["gpt-6.2-sol"],"expected_scope":"`+scope+`"}]}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	rows, err := store.db.Query(`SELECT kind,model,requested_scope,full_model_check,state FROM console_configured_checks WHERE source_id='suite' AND provider='group'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]bool{"model/gpt-6.2-sol": true, "model/other": true, "fast/gpt-6.2-sol": true,
		"ultrafast/gpt-6.2-sol": true, "compaction/gpt-6.2-sol": true, "tool-use/gpt-6.2-sol": true}
	for rows.Next() {
		var kind, model, binding, state string
		var full bool
		if err := rows.Scan(&kind, &model, &binding, &full, &state); err != nil {
			t.Fatal(err)
		}
		job := kind + "/" + model
		if !want[job] || full != (kind == "model") || state != "queued" || isServiceTier(kind) && binding != scope {
			t.Fatalf("unexpected suite job %s full=%v state=%s scope=%q", job, full, state, binding)
		}
		delete(want, job)
	}
	if rows.Err() != nil || len(want) != 0 {
		t.Fatal(rows.Err(), want)
	}
}

func TestSiteTierProbeVerifiesActualModelAndEffectiveTier(t *testing.T) {
	const model = "gpt-6.2-sol"
	complete := func(name, tier string) string {
		return mustJSON(map[string]any{"id": "resp-fixture", "model": name, "status": "completed", "service_tier": tier, "output": []any{}})
	}
	for _, test := range []struct {
		name, requested, contentType, body, status string
		code                                       int
	}{
		{"priority", "fast", "application/json", complete(model, "priority"), "supported", 200},
		{"stream", "ultrafast", "text/event-stream", "data: " + mustJSON(map[string]any{"type": "response.completed", "response": json.RawMessage(complete(model, "ultrafast"))}) + "\n\n", "supported", 200},
		{"downgraded", "ultrafast", "application/json", complete(model, "fast"), "not_honored", 200},
		{"missing effective tier", "fast", "application/json", complete(model, ""), "inconclusive", 200},
		{"different model", "ultrafast", "application/json", complete("wrong", "ultrafast"), "inconclusive", 200},
		{"truncated SSE", "fast", "text/event-stream", `data: {"type":"response.created","response":{"service_tier":"fast"}}`, "inconclusive", 200},
		{"explicit rejection", "fast", "application/json", `{"error":{"param":"service_tier","code":"unsupported_service_tier"}}`, "unsupported", 422},
		{"unrelated rejection", "fast", "application/json", `{"error":{"param":"model","code":"unsupported_service_tier"}}`, "inconclusive", 400},
		{"redirect", "fast", "application/json", "", "inconclusive", 307},
	} {
		t.Run(test.name, func(t *testing.T) {
			count := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				var body map[string]any
				if r.Method != "POST" || r.URL.Path != "/v1/responses" || json.NewDecoder(r.Body).Decode(&body) != nil ||
					body["model"] != model || body["service_tier"] != test.requested || body["store"] != false || body["stream"] != true ||
					r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("unexpected probe request")
				}
				w.Header().Set("Content-Type", test.contentType)
				if test.code == 307 {
					w.Header().Set("Location", "/unexpected")
				}
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer up.Close()
			out := probeSiteTier(context.Background(), up.Client(), up.URL, "fixture", model, test.requested)
			if out.Status != test.status || count != 1 {
				t.Fatalf("got %v, calls=%d", out, count)
			}
		})
	}
}

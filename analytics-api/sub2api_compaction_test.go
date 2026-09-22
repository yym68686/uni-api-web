package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func subFixtureCompaction(r *http.Request) bool {
	raw, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body struct {
		Input []struct {
			Type string `json:"type"`
		} `json:"input"`
	}
	_ = json.Unmarshal(raw, &body)
	for _, item := range body.Input {
		if item.Type == "compaction_trigger" {
			return true
		}
	}
	return false
}

func compactSSE(w http.ResponseWriter, model, output string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "event: response.created\ndata: {\"response\":{\"id\":\"resp-compact\"}}\n\nevent: response.completed\ndata: {\"response\":{\"id\":\"resp-compact\",\"status\":\"completed\",\"model\":%q,\"output\":%s}}\n\n", model, output)
}

func TestCompactionRequiresValidOutputAndPreservesRequestShape(t *testing.T) {
	for _, tc := range []struct {
		name, output, status string
		http                 int
	}{
		{"compaction", `[{"type":"compaction","encrypted_content":"opaque-secret"}]`, "supported", 200},
		{"text", `[{"type":"message","content":[{"type":"output_text","text":"I compressed it"}]}]`, "unsupported", 200},
		{"empty", `[]`, "unsupported", 200},
		{"no-state", `[{"type":"compaction"}]`, "unsupported", 200},
		{"multiple", `[{"type":"compaction","encrypted_content":"a"},{"type":"compaction","encrypted_content":"b"}]`, "unsupported", 200},
		{"bad-request", "", "unsupported", 400},
		{"auth", "", "error", 401},
		{"rate-limit", "", "error", 429},
		{"timeout", "", "error", 504},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid request")
				}
				if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer api-secret" || body["model"] != "gpt-6-astra" || body["stream"] != true || body["store"] != false {
					t.Error("unexpected compaction request")
				}
				input := body["input"].([]any)
				if len(input) != 3 || input[0].(map[string]any)["type"] != "additional_tools" || input[2].(map[string]any)["type"] != "compaction_trigger" {
					t.Error("compaction input changed")
				}
				meta := body["client_metadata"].(map[string]any)
				var nested map[string]any
				if json.Unmarshal([]byte(meta["x-codex-turn-metadata"].(string)), &nested) != nil || nested["session_id"] != body["prompt_cache_key"] || nested["turn_id"] != meta["turn_id"] || nested["request_kind"] != "compaction" {
					t.Error("inconsistent metadata")
				}
				w.Header().Set("X-Client-Request-ID", "billing-compact")
				if tc.http != 200 {
					http.Error(w, "private api-secret", tc.http)
					return
				}
				compactSSE(w, "gpt-6-astra", tc.output)
			}))
			defer server.Close()
			out := subProbeCompaction(context.Background(), server.Client(), server.URL, "api-secret", "gpt-6-astra")
			if out.Status != tc.status {
				t.Fatalf("%+v", out)
			}
			raw := mustJSON(out)
			if strings.Contains(raw, "api-secret") || strings.Contains(raw, "opaque-secret") {
				t.Fatal("secret leaked")
			}
			if !strings.Contains(raw, "client:billing-compact") {
				t.Fatal("missing billing correlation")
			}
		})
	}
	for _, body := range []string{"data: [DONE]\n\n", "data: {\"type\":\"response.created\"}\n\n", "data: broken\n\n"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, body)
		}))
		out := subProbeCompaction(context.Background(), server.Client(), server.URL, "secret", "gpt-6-astra")
		server.Close()
		if out.Status != "error" {
			t.Fatal("incomplete stream claimed support", out)
		}
	}
}

func TestCompactionQueueSelectsAvailableModelsAndPreservesOtherResults(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("c", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "compact-" + randomID()[:12]
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, id)
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, body.Model)
		if body.Model == "gpt-5.6-sol" {
			http.Error(w, "unsupported", 400)
			return
		}
		compactSSE(w, body.Model, `[{"type":"compaction","encrypted_content":"opaque"}]`)
	}))
	defer server.Close()
	previousHTTP := subHTTP
	subHTTP = server.Client()
	defer func() { subHTTP = previousHTTP }()
	key, _ := store.encrypt("key")
	_, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$1,'Fixture',$2,'test@example.com','')`, id, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key,remote_key_id) VALUES($1,1,'group','openai',$2,42),($1,2,'no-model','openai',$2,43)`, id, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"gpt-5.6-terra", "gpt-5.6-sol", "gpt-6-astra", "glm-5.3"} {
		status := "success"
		if model == "gpt-5.6-terra" {
			status = "error"
		}
		_, err = store.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,1,$2,'done',$3)`, id, model, mustJSON(subResult{Model: model, CheckedAt: 1, Availability: subProbe{Status: status}, Verdict: "pass"}))
		if err != nil {
			t.Fatal(err)
		}
	}
	var before string
	store.db.QueryRow(`SELECT jsonb_agg(result ORDER BY model)::text FROM console_sub_models WHERE account_id=$1`, id).Scan(&before)
	service := &Service{control: store}
	token, _ := store.newSession(context.Background(), id)
	foreign, _ := store.newSession(context.Background(), id+"-foreign")
	call := func(cookie, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/sub2api/compaction-checks", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, req)
		return w
	}
	body := fmt.Sprintf(`{"targets":[{"account_id":%q,"group_id":1},{"account_id":%q,"group_id":2}]}`, id, id)
	if w := call(foreign, body); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := call(token, body); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(token, body); w.Code != 409 {
		t.Fatal("duplicate queued")
	}
	if !service.subWorkOne(context.Background()) {
		t.Fatal("job missing")
	}
	if !reflect.DeepEqual(calls, []string{"gpt-5.6-sol", "gpt-6-astra"}) {
		t.Fatal("wrong candidates or failed early stop", calls)
	}
	var raw []byte
	var state string
	if err = store.db.QueryRow(`SELECT compaction,compaction_state FROM console_sub_targets WHERE account_id=$1 AND group_id=1`, id).Scan(&raw, &state); err != nil {
		t.Fatal(err)
	}
	var result subCompaction
	json.Unmarshal(raw, &result)
	if state != "done" || result.Status != "supported" || result.Model != "gpt-6-astra" || len(result.Attempts) != 2 {
		t.Fatal(string(raw), state)
	}
	var after string
	store.db.QueryRow(`SELECT jsonb_agg(result ORDER BY model)::text FROM console_sub_models WHERE account_id=$1`, id).Scan(&after)
	if before != after {
		t.Fatal("compaction changed model or quality result")
	}
	store.db.QueryRow(`SELECT compaction FROM console_sub_targets WHERE account_id=$1 AND group_id=2`, id).Scan(&raw)
	if !strings.Contains(string(raw), `"status": "error"`) {
		t.Fatal("no usable models misclassified", string(raw))
	}
	if w := call(token, body); w.Code != 202 {
		t.Fatal(w.Code)
	}
	_, err = store.db.Exec(`UPDATE console_sub_accounts SET state='interrupted',job_id='' WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	service.subExpireJobs(context.Background())
	store.db.QueryRow(`SELECT compaction_state FROM console_sub_targets WHERE account_id=$1 AND group_id=1`, id).Scan(&state)
	if state != "interrupted" {
		t.Fatal("cancelled detection stuck", state)
	}
	if len(calls) != 2 {
		t.Fatal("paid probes replayed")
	}
}

func TestCompactionImportIsAtomicAndPreservesExistingRules(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			var applied retainedSnapshot
			mutations := 0
			revision := "r1"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/channel-settings/export":
					writeJSON(w, 200, map[string]any{"revision": revision, "channel_settings": map[string]any{"existing": map[string]any{"set": map[string]any{"/preferences/cooldown_period": 30}, "remove": []string{}}}, "temporary_definitions": map[string]any{"existing": map[string]any{"provider": "existing", "base_url": "https://old.test/v1/responses", "api": []string{"old-secret"}, "model": []string{checkModel}}}})
				case "/v1/model-channels":
					writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"provider": "configured", "model": checkModel}, map[string]any{"provider": "existing", "model": checkModel}}})
				case "/v1/channel-controls/restore":
					var body struct {
						Revision string           `json:"revision"`
						Snapshot retainedSnapshot `json:"snapshot"`
					}
					json.NewDecoder(r.Body).Decode(&body)
					if body.Revision != "r1" {
						t.Error("revision missing")
					}
					applied = body.Snapshot
					mutations++
					writeJSON(w, 200, map[string]any{"revision": "r2"})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			state := map[string]any{"temporary_channel_restore": true, "channel_settings": true, "rules": []retainedRule{{KeyID: "key", Model: checkModel, Order: []string{"configured", "existing"}, Disabled: []string{"existing"}}, {KeyID: "other", Model: "other-model", Order: []string{"unrelated"}, Disabled: []string{}}}, "temporary_channels": []retainedChannel{{Provider: "existing", KeyID: "key", Models: []string{checkModel}}}}
			src := controlSource{sourceView: sourceView{Base: server.URL}}
			service := &Service{}
			in := subImportInput{Revision: "r1", Models: []string{checkModel}, Position: 2, CompactionEnabled: &enabled}
			_, code, err := service.subImportCompactionChannel(context.Background(), src, state, in, "key", "sub2api-new", "https://new.test", "new-secret")
			if err != nil || code != 200 || mutations != 1 {
				t.Fatal(code, err, mutations)
			}
			if len(applied.Channels) != 2 || len(applied.Settings) != 1 || !reflect.DeepEqual(applied.Rules[0].Order, []string{"configured", "sub2api-new", "existing"}) || !reflect.DeepEqual(applied.Rules[0].Disabled, []string{"existing"}) || applied.Rules[1].KeyID != "other" {
				t.Fatal("other configuration changed")
			}
			var doc map[string]any
			json.Unmarshal(applied.Channels[1].Definition, &doc)
			types := doc["exclude_request_types"].([]any)
			if enabled && len(types) != 0 || !enabled && (len(types) != 1 || types[0] != "compaction") {
				t.Fatal("incorrect compaction policy", types)
			}
			revision = "changed"
			_, code, err = service.subImportCompactionChannel(context.Background(), src, state, in, "key", "sub2api-new", "https://new.test", "new-secret")
			if err == nil || code != 409 || mutations != 1 {
				t.Fatal("stale snapshot applied")
			}
		})
	}
}

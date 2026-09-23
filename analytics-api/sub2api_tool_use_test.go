package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func toolUseSSE(items []toolUseItem, final []toolUseItem) string {
	var body strings.Builder
	fmt.Fprint(&body, "event: response.created\ndata: {\"response\":{\"id\":\"tool-response\"}}\n\n")
	for _, item := range items {
		fmt.Fprintf(&body, "event: response.output_item.done\ndata: %s\n\n", mustJSON(map[string]any{"item": item}))
	}
	if final == nil {
		final = []toolUseItem{}
	}
	fmt.Fprintf(&body, "event: response.completed\ndata: %s\n\n", mustJSON(map[string]any{"response": map[string]any{"id": "tool-response", "status": "completed", "model": "returned-model", "output": final}}))
	return body.String()
}

func validToolUseItem() toolUseItem {
	return toolUseItem{ID: "item-exec", CallID: "call-exec", Type: "custom_tool_call", Namespace: "functions", Name: "exec", Input: `text("TOOL_PROBE_OK");`}
}

func TestToolUseProbeChecksEntireStreamAndExactCallable(t *testing.T) {
	valid := validToolUseItem()
	bare := valid
	bare.Namespace = ""
	qualified := bare
	qualified.Name = "functions.exec"
	wrongNS := valid
	wrongNS.Namespace = "other"
	wrongInput := valid
	wrongInput.Input = `tools.exec_command({cmd:"anything"})`
	missingCallID := valid
	missingCallID.CallID = ""
	wrongType := valid
	wrongType.Type = "function_call"
	wrongType.Input = ""
	wrongType.Arguments = `{"input":"text(\"TOOL_PROBE_OK\");"}`
	fallback := toolUseItem{Type: "function_call", Name: "js", Namespace: "mcp__cua_repl", CallID: "call-fallback", Arguments: `{"code":"NO_EXEC"}`}
	other := toolUseItem{Type: "function_call", Name: "probe_json", Namespace: "functions", CallID: "call-other", Arguments: `{"marker":"TOOL_PROBE_OK"}`}
	noID := valid
	noID.ID = ""
	for _, tc := range []struct{ name, body, status, marker string }{
		{"done-final-empty", toolUseSSE([]toolUseItem{valid}, nil), "supported", "TOOL_PROBE_OK"},
		{"final-only", toolUseSSE(nil, []toolUseItem{valid}), "supported", "TOOL_PROBE_OK"},
		{"deduplicated", toolUseSSE([]toolUseItem{valid}, []toolUseItem{noID}), "supported", "TOOL_PROBE_OK"},
		{"bare-name", toolUseSSE([]toolUseItem{bare}, nil), "supported", ""},
		{"qualified-name", toolUseSSE([]toolUseItem{qualified}, nil), "supported", ""},
		{"no-exec", toolUseSSE([]toolUseItem{fallback}, nil), "unsupported", "NO_EXEC"},
		{"wrong-function", toolUseSSE([]toolUseItem{other}, nil), "unsupported", ""},
		{"wrong-namespace", toolUseSSE([]toolUseItem{wrongNS}, nil), "unsupported", ""},
		{"wrong-input", toolUseSSE([]toolUseItem{wrongInput}, nil), "unsupported", ""},
		{"missing-call-id", toolUseSSE([]toolUseItem{missingCallID}, nil), "unsupported", ""},
		{"function-not-custom", toolUseSSE([]toolUseItem{wrongType}, nil), "unsupported", ""},
		{"multiple-calls", toolUseSSE([]toolUseItem{valid, other}, nil), "unsupported", ""},
		{"conflicting-terminal", toolUseSSE([]toolUseItem{valid}, []toolUseItem{wrongInput}), "error", ""},
		{"ordinary-text", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"text(\\\"TOOL_PROBE_OK\\\");\"}\n\n" + toolUseSSE(nil, []toolUseItem{{Type: "message"}}), "unsupported", ""},
		{"added-only", "data: " + mustJSON(map[string]any{"type": "response.output_item.added", "item": valid}) + "\n\n" + toolUseSSE(nil, nil), "unsupported", ""},
		{"truncated-after-call", "data: " + mustJSON(map[string]any{"type": "response.output_item.done", "item": valid}) + "\n\n", "error", ""},
		{"failed-after-call", strings.Split(toolUseSSE([]toolUseItem{valid}, nil), "event: response.completed")[0] + "data: {\"type\":\"response.failed\"}\n\n", "error", ""},
		{"done-only", "data: [DONE]\n\n", "error", ""},
		{"bad-json", "data: invalid\n\n", "error", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer fixture-secret" || body["model"] != "gpt-5.6-sol" || body["stream"] != true || body["store"] != false {
					t.Error("incorrect probe request")
				}
				input := body["input"].([]any)
				additional := input[0].(map[string]any)
				if additional["type"] != "additional_tools" || additional["role"] != "developer" {
					t.Error("tool definitions not preserved")
				}
				ns := additional["tools"].([]any)[0].(map[string]any)
				exec := ns["tools"].([]any)[0].(map[string]any)
				if ns["name"] != "functions" || exec["type"] != "custom" || exec["name"] != "exec" || exec["format"].(map[string]any)["syntax"] != "lark" {
					t.Error("custom exec/grammar lost")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Client-Request-ID", "upstream-tool-id")
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			result := subProbeToolUse(context.Background(), server.Client(), server.URL, "fixture-secret", "gpt-5.6-sol")
			if result.Status != tc.status || !strings.Contains(result.Text, tc.marker) {
				t.Fatalf("%+v", result)
			}
			if tc.status == "supported" && result.ModelMatch != "mismatch" {
				t.Fatal("tool result conflated with model match")
			}
			if strings.Contains(mustJSON(result), "fixture-secret") || !strings.Contains(mustJSON(result), "client:upstream-tool-id") {
				t.Fatal("credential or correlation error")
			}
		})
	}
}

func TestToolUseHTTPFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		code         int
		body, status string
	}{
		{400, `{"error":{"code":"unsupported_value","param":"input[0].type","message":"additional_tools is not supported"}}`, "unsupported"},
		{422, `{"error":{"code":"unsupported_parameter","param":"tools"}}`, "unsupported"},
		{400, `{"error":{"message":"insufficient balance"}}`, "error"},
		{404, `{"error":{"message":"model missing"}}`, "error"},
		{429, `rate limit fixture-secret`, "error"},
		{403, `insufficient balance fixture-secret`, "error"},
		{401, `auth fixture-secret`, "error"},
		{504, `timeout fixture-secret`, "error"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); fmt.Fprint(w, tc.body) }))
		out := subProbeToolUse(context.Background(), server.Client(), server.URL, "fixture-secret", "gpt-6-astra")
		server.Close()
		if out.Status != tc.status || strings.Contains(mustJSON(out), "fixture-secret") {
			t.Fatal(tc.code, out)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := subProbeToolUse(ctx, http.DefaultClient, "http://127.0.0.1:1", "fixture-secret", "gpt-6-astra")
	if out.Status != "error" {
		t.Fatal("cancelled probe marked supported")
	}
}

func TestToolUseQueueRunsEveryAvailableModelAndPreservesOtherChecks(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("t", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "tools-" + randomID()[:12]
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, id)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "gpt-5.6-sol" && body.Model != checkModel && body.Model != "gpt-6-sol" {
			t.Error("selected unavailable or wrong model", body.Model)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if body.Model == "gpt-6-sol" {
			w.WriteHeader(503)
			return
		}
		if body.Model == "gpt-5.6-sol" {
			fmt.Fprint(w, toolUseSSE([]toolUseItem{validToolUseItem()}, nil))
		} else {
			fmt.Fprint(w, toolUseSSE([]toolUseItem{{Type: "function_call", Name: "js", Namespace: "mcp__cua_repl", CallID: "fallback", Arguments: `{"code":"NO_EXEC"}`}}, nil))
		}
	}))
	defer server.Close()
	previous := subHTTP
	subHTTP = server.Client()
	defer func() { subHTTP = previous }()
	key, _ := store.encrypt("probe-secret")
	_, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$1,'Fixture',$2,'fixture@example.com','')`, id, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key,remote_key_id,compaction,compaction_state) VALUES($1,1,'available','openai',$2,42,'{"status":"supported","model":"gpt-6-astra","attempts":[]}','done'),($1,2,'untested','openai',$2,43,NULL,'idle'),($1,3,'unselected','openai',$2,44,NULL,'idle')`, id, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"gpt-5.6-terra", "gpt-5.6-sol", "gpt-6-astra", "gpt-6-sol"} {
		status := "success"
		if model == "gpt-5.6-terra" {
			status = "error"
		}
		_, err = store.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,1,$2,'done',$3)`, id, model, mustJSON(subResult{Model: model, Verdict: "pass", Availability: subProbe{Status: status}}))
		if err != nil {
			t.Fatal(err)
		}
	}
	var before string
	store.db.QueryRow(`SELECT jsonb_agg(result ORDER BY model)::text FROM console_sub_models WHERE account_id=$1`, id).Scan(&before)
	service := &Service{control: store}
	token, _ := store.newSession(context.Background(), id)
	foreign, _ := store.newSession(context.Background(), id+"foreign")
	request := func(method, path, body, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, req)
		return w
	}
	body := fmt.Sprintf(`{"targets":[{"account_id":%q,"group_id":1,"models":["gpt-6-astra"]},{"account_id":%q,"group_id":2},{"account_id":%q,"group_id":1}]}`, id, id, id)
	for _, tc := range []struct {
		cookie string
		code   int
	}{{"", 401}, {foreign, 409}, {token, 202}, {token, 409}} {
		if w := request("POST", "/v1/sub2api/tool-use-checks", body, tc.cookie); w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if !service.subWorkOne(context.Background()) || calls.Load() != 3 {
		t.Fatal("did not test all three available models", calls.Load())
	}
	var state, raw string
	store.db.QueryRow(`SELECT tool_use_state,tool_use::text FROM console_sub_targets WHERE account_id=$1 AND group_id=1`, id).Scan(&state, &raw)
	var result subCapabilityResult
	_ = json.Unmarshal([]byte(raw), &result)
	if state != "done" || result.Status != "unsupported" || len(result.Models) != 3 || len(result.Attempts) != 3 || result.Attempts[1].Text != "NO_EXEC" {
		t.Fatal(raw)
	}
	if result.Models[0].Result.Status != "supported" || result.Models[1].Result.Status != "unsupported" || result.Models[2].Result.Status != "error" {
		t.Fatal("model results conflated", raw)
	}
	var after string
	store.db.QueryRow(`SELECT jsonb_agg(result ORDER BY model)::text FROM console_sub_models WHERE account_id=$1`, id).Scan(&after)
	if before != after {
		t.Fatal("quality or availability overwritten")
	}
	store.db.QueryRow(`SELECT compaction->>'status' FROM console_sub_targets WHERE account_id=$1 AND group_id=1`, id).Scan(&state)
	if state != "supported" {
		t.Fatal("compaction overwritten")
	}
	store.db.QueryRow(`SELECT tool_use->>'status' FROM console_sub_targets WHERE account_id=$1 AND group_id=2`, id).Scan(&state)
	if state != "error" {
		t.Fatal("no available models misclassified")
	}
	store.db.QueryRow(`SELECT tool_use_state FROM console_sub_targets WHERE account_id=$1 AND group_id=3`, id).Scan(&state)
	if state != "idle" {
		t.Fatal("unselected group tested")
	}
	// Billing attaches to the new probe without changing other checks.
	_, err = store.db.Exec(`UPDATE console_sub_usage SET status='matched',result='{"status":"matched","actual_cost":0.001}' WHERE account_id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	w := request("GET", "/v1/sub2api/accounts", "", token)
	var listed struct {
		Data []subAccount `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listed) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(listed.Data) != 1 || listed.Data[0].Targets[0].ToolUse.Models[0].Result.Attempts[0].Usage.Status != "matched" {
		t.Fatal("tool usage missing from accounts")
	}
	w = request("GET", "/v1/sub2api/accounts", "", foreign)
	if !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatal("cross-owner results exposed")
	}
	if w = request("POST", "/v1/sub2api/tool-use-checks", body, token); w.Code != 202 {
		t.Fatal(w.Code)
	}
	if w = request("POST", "/v1/sub2api/accounts/"+id+"/stop", "{}", token); w.Code != 200 {
		t.Fatal(w.Code)
	}
	store.db.QueryRow(`SELECT tool_use_state FROM console_sub_targets WHERE account_id=$1 AND group_id=1`, id).Scan(&state)
	if state != "interrupted" || service.subWorkOne(context.Background()) || calls.Load() != 3 {
		t.Fatal("cancelled probes replayed")
	}
}

func TestToolUseCompletedModelsSurviveInterruption(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("t", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id, job := "tools-progress-"+randomID()[:12], randomID()
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, id)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 {
			// The first completed result must already be durable while the next
			// request is still in flight, including when that request is cancelled.
			var status string
			if e := store.db.QueryRow(`SELECT tool_use->'models'->0->'result'->>'status' FROM console_sub_targets WHERE account_id=$1 AND group_id=1`, id).Scan(&status); e != nil || status != "supported" {
				t.Errorf("first result not persisted before next probe: %q %v", status, e)
			}
			cancel()
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, toolUseSSE([]toolUseItem{validToolUseItem()}, nil))
	}))
	defer server.Close()
	previous := subHTTP
	subHTTP = server.Client()
	defer func() { subHTTP = previous }()
	key, _ := store.encrypt("probe-secret")
	if _, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth,state,job_id) VALUES($1,$1,'Fixture',$2,'fixture@example.com','','running',$3)`, id, server.URL, job); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key,remote_key_id,tool_use_state) VALUES($1,1,'available','openai',$2,42,'queued')`, id, key); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"gpt-5.6-sol", "gpt-6-astra", "gpt-6-sol"} {
		if _, err = store.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,1,$2,'done',$3)`, id, model, mustJSON(subResult{Model: model, Availability: subProbe{Status: "success"}})); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{control: store}
	if service.subTestAllModelTools(ctx, id, server.URL, job) == nil || calls.Load() != 2 {
		t.Fatal("interrupted job continued probing", calls.Load())
	}
	var raw []byte
	if err = store.db.QueryRow(`SELECT tool_use FROM console_sub_targets WHERE account_id=$1 AND group_id=1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result subCapabilityResult
	if json.Unmarshal(raw, &result) != nil || len(result.Models) != 3 || result.Models[0].State != "done" || result.Models[0].Result.Status != "supported" || result.Models[1].State != "running" || result.Models[2].State != "queued" {
		t.Fatal("partial results lost or unfinished models marked passed", string(raw))
	}
}

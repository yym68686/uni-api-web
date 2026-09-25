package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfiguredChecksWithoutAccountPersistAllKinds(t *testing.T) {
	var calls atomic.Int32
	var capability atomic.Bool
	capability.Store(true)
	providers := []configuredProvider{{Provider: "native", Base: "https://upstream.example/v1/responses", API: "private-upstream"}}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gateway-secret" {
			t.Error("wrong source authentication")
		}
		switch r.URL.Path {
		case "/v1/observability/runtime":
			writeJSON(w, 200, map[string]any{"capabilities": map[string]bool{"targeted_responses": capability.Load()}})
		case "/v1/channel-settings/providers":
			writeJSON(w, 200, map[string]any{"providers": providers})
		case "/v1/model-channels":
			writeJSON(w, 200, map[string]any{"data": []any{map[string]string{"provider": "native", "model": checkModel}, map[string]string{"provider": "native", "model": "custom-model"}}})
		case "/v1/responses":
			calls.Add(1)
			if r.Header.Get("X-Uni-API-Provider") != "native" {
				t.Error("probe was not targeted")
			}
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("bad body")
			}
			raw := mustJSON(body)
			if strings.Contains(raw, "compaction_trigger") {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: response.completed\ndata: {\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"compaction\",\"encrypted_content\":\"opaque\"}]}}\n\n")
			} else if strings.Contains(raw, "TOOL_PROBE_OK") {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, strings.ReplaceAll(strings.ReplaceAll(toolUseSSE(nil, func() []toolUseItem {
					if body["model"] == "custom-model" {
						return []toolUseItem{{Type: "function_call", Name: "js", Namespace: "mcp__cua_repl", CallID: "noexec", Arguments: `{"code":"NO_EXEC"}`}}
					}
					return []toolUseItem{validToolUseItem()}
				}()), `data: {"response":{"id":"tool-response"}}`, `data: {"type":"response.created","response":{"id":"tool-response"}}`), `data: {"response":{"id":"tool-response","model"`, `data: {"type":"response.completed","response":{"id":"tool-response","model"`))
			} else {
				subSSE(w, "21", body["model"].(string))
			}
		default:
			t.Errorf("unexpected gateway request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	s, a, src := bindingFixture(t, "https://unrelated.example")
	src.Base = gateway.URL
	if _, err := s.control.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_channel_checks WHERE source_id=$1`, src.ID) })
	token, _ := s.control.newSession(context.Background(), a.Owner)
	request := func(method string, body any, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/channel-management/checks", strings.NewReader(mustJSON(body)))
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("GET", nil, ""); w.Code != 401 {
		t.Fatal("missing auth", w.Code)
	}
	enqueue := func(kind, provider string, models []string) int {
		t.Helper()
		w := request("POST", map[string]any{"kind": kind, "targets": []any{map[string]any{"source_id": src.ID, "provider": provider, "models": models}}}, token)
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		var out struct {
			Queued int `json:"queued"`
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		return out.Queued
	}
	if n := enqueue("check", "native", []string{checkModel, "custom-model"}); n != 2 {
		t.Fatal(n)
	}
	if n := enqueue("quality", "native", nil); n != 0 {
		t.Fatal("duplicate Astra job", n)
	}
	for i := 0; i < 2; i++ {
		if !s.configuredCheckOne(context.Background()) {
			t.Fatal("missing job")
		}
	}
	for _, kind := range []string{"quality", "compaction", "tool-use"} {
		if enqueue(kind, "native", nil) != 1 || !s.configuredCheckOne(context.Background()) {
			t.Fatal(kind)
		}
	}
	for range 2 {
		if !s.configuredCheckOne(context.Background()) {
			t.Fatal("missing model tool job")
		}
	}
	var childCount int
	s.control.db.QueryRow(`SELECT count(*) FROM console_configured_checks WHERE source_id=$1 AND kind='tool-use' AND model<>''`, src.ID).Scan(&childCount)
	if childCount != 2 {
		t.Fatal("missing model tool results", childCount)
	}
	w := request("GET", nil, token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Data []configuredCheck `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal(w.Body.String())
	}
	native := 0
	for _, c := range out.Data {
		if c.Source != src.ID {
			continue
		}
		native++
		if c.State != "done" || c.Fingerprint == "" {
			t.Fatalf("bad state %+v", c)
		}
		if c.Kind == "model" {
			var result subResult
			json.Unmarshal(c.Result, &result)
			if result.Availability.Status != "success" {
				t.Fatal(result)
			}
			if c.Model == checkModel && (result.Verdict != "pass" || c.History.Successful != 2 || c.History.Passed != 2) {
				t.Fatal(result, c.History)
			}
		} else {
			var result subCapabilityResult
			json.Unmarshal(c.Result, &result)
			if c.Kind == "tool-use" && c.Model == "" {
				if len(result.Models) != 2 {
					t.Fatal("missing fanout", result)
				}
				continue
			}
			want := "supported"
			if c.Kind == "tool-use" && c.Model == "custom-model" {
				want = "unsupported"
			}
			if result.Status != want {
				t.Fatal(c.Kind, result)
			}
		}
	}
	if native != 6 {
		t.Fatal(native)
	}
	if strings.Contains(w.Body.String(), "gateway-secret") || strings.Contains(w.Body.String(), "private-upstream") {
		t.Fatal("secret leaked")
	}
	// Removing/changing the source or provider must not invoke the default route.
	before := calls.Load()
	enqueue("check", "missing", []string{checkModel})
	s.configuredCheckOne(context.Background())
	enqueue("check", "native", []string{"not-configured"})
	s.configuredCheckOne(context.Background())
	capability.Store(false)
	enqueue("quality", "native", nil)
	s.configuredCheckOne(context.Background())
	if calls.Load() != before {
		t.Fatal("unknown target/model/old gateway issued billable request")
	}
	w = request("GET", nil, token)
	if !strings.Contains(w.Body.String(), "来源不支持渠道定向检测") {
		t.Fatal(w.Body.String())
	}
	// Lost workers expose interruption; the next click gets a new job, never an automatic replay.
	_, err := s.control.db.Exec(`UPDATE console_configured_checks SET state='running',deadline=now()-interval '1 second' WHERE source_id=$1`, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.configuredCheckOne(context.Background())
	if calls.Load() != before {
		t.Fatal("expired job replayed")
	}
	if enqueue("quality", "native", nil) != 1 {
		t.Fatal("interrupted job could not be retried")
	}
	// Clean queued fixtures before other worker integration tests start.
	s.control.db.Exec(`DELETE FROM console_configured_checks WHERE source_id=$1`, src.ID)
}

func TestConfiguredChecksThroughRealGatewayWithoutClientRoutes(t *testing.T) {
	binary := os.Getenv("TEST_UNI_API_BINARY")
	if binary == "" {
		t.Skip("local uni-api binary required")
	}
	var otherCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/other/") {
			otherCalls.Add(1)
			http.Error(w, "wrong channel", 500)
			return
		}
		if strings.Contains(r.URL.Path, "/fail/") {
			http.Error(w, "upstream failure", 503)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		raw := mustJSON(body)
		if strings.Contains(raw, "compaction_trigger") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-compact\",\"status\":\"in_progress\"}}\n\n")
			fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", mustJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp-compact", "status": "completed", "model": body["model"], "output": []any{map[string]string{"id": "compact-item", "type": "compaction", "encrypted_content": "opaque"}}}}))
		} else if strings.Contains(raw, "TOOL_PROBE_OK") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, strings.ReplaceAll(strings.ReplaceAll(toolUseSSE(nil, []toolUseItem{validToolUseItem()}), `data: {"response":{"id":"tool-response"}}`, `data: {"type":"response.created","response":{"id":"tool-response"}}`), `data: {"response":{"id":"tool-response","model"`, `data: {"type":"response.completed","response":{"id":"tool-response","model"`))
		} else if strings.Contains(r.URL.Path, "/gemini/") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"modelVersion\":\"gemini-fixture\",\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"test\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1}}\n\n")
		} else if strings.Contains(r.URL.Path, "/claude/") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"role\":\"assistant\",\"model\":\"claude-fixture\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"test\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		} else {
			subSSE(w, "21", fmt.Sprint(body["model"]))
		}
	}))
	defer upstream.Close()
	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(listener.Addr().String(), ":")[1]
	listener.Close()
	providers := []any{}
	for _, name := range []string{"native", "other", "fail"} {
		providers = append(providers, map[string]any{"provider": name, "engine": "gpt", "base_url": upstream.URL + "/" + name + "/v1/responses", "api": "fixture-key", "model": []string{checkModel, "custom-model"}})
	}
	providers = append(providers, map[string]any{"provider": "claude", "engine": "claude", "base_url": upstream.URL + "/claude/v1/messages", "api": "fixture-claude", "model": []string{"claude-fixture"}})
	providers = append(providers, map[string]any{"provider": "gemini", "engine": "gemini", "base_url": upstream.URL + "/gemini/", "api": "fixture-gemini", "model": []string{"gemini-fixture"}})
	config := map[string]any{"providers": providers, "api_keys": []any{map[string]any{"api": "admin-fixture", "role": "admin", "model": []string{"unconfigured-model"}}}, "preferences": map[string]any{"AUTO_RETRY": true}}
	path := filepath.Join(dir, "api.json")
	if err = os.WriteFile(path, []byte(mustJSON(config)), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "gateway.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(binary)
	cmd.Dir = dir
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "PORT=" + port, "DISABLE_DATABASE=true", "UNI_API_CONFIG_PATH=" + path, "RUST_RESPONSES_CONFIG_SNAPSHOT_PATH=" + filepath.Join(dir, "snapshot.json"), "UNI_API_SHARED_MEMORY_RESERVATION_PATH=" + filepath.Join(dir, "ledger"), "RUST_REQUEST_SPOOL_DIRECTORY=" + filepath.Join(dir, "spool"), "RUST_REQUEST_SPOOL_DISK_RESERVE_BPS=0", "RUST_REQUEST_SPOOL_INODE_RESERVE_BPS=0", "NO_PROXY=127.0.0.1,localhost"}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			raw, _ := os.ReadFile(log.Name())
			t.Log(string(raw))
		}
	}()
	s, _, src := bindingFixture(t, "https://unrelated.example")
	src.Base = "http://127.0.0.1:" + port
	src.Key = "admin-fixture"
	if _, err = s.control.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_channel_checks WHERE source_id=$1`, src.ID) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var before map[string]any
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		before, _, err = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	// A drawer may point to a key-owned temporary channel rather than the
	// base-config provider. Its diagnostics must use that exact effective copy.
	keys, _, err := fetchSource(ctx, src, "/v1/api-keys", nil)
	if err != nil {
		t.Fatal(err)
	}
	key := keys["data"].([]any)[0].(map[string]any)["key_id"].(string)
	before, _, err = subGateway(ctx, src, "POST", "/v1/temporary-channels", map[string]any{"revision": before["revision"], "api_key_id": key, "provider": "sub2api-drawer", "base_url": upstream.URL + "/native/v1/responses", "api_key": "fixture-key", "models": []string{checkModel, "custom-model"}, "position": 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ provider, kind, model, want string }{
		{"sub2api-drawer", "availability", checkModel, "success"}, {"sub2api-drawer", "tool-use", "custom-model", "supported"}, {"sub2api-drawer", "compaction", "", "supported"},
		{"native", "model", checkModel, "success"}, {"native", "model", "new-unconfigured-model", "success"}, {"native", "compaction", "", "supported"}, {"native", "tool-use", checkModel, "supported"}, {"claude", "model", "claude-fixture", "success"}, {"claude", "model", "claude-new-unconfigured", "success"}, {"gemini", "model", "gemini-fixture", "success"}, {"gemini", "model", "gemini-new-unconfigured", "success"}, {"fail", "model", "custom-model", "error"},
	} {
		c := configuredCheck{Source: src.ID, Provider: test.provider, Kind: test.kind, Model: test.model}
		var result any
		if err = s.runConfiguredCheck(ctx, &c, controlTarget(src), randomID(), false, &result); err != nil {
			t.Fatal(test, err)
		}
		status := ""
		switch r := result.(type) {
		case subResult:
			status = r.Availability.Status
		case subCapabilityResult:
			status = r.Status
		}
		if c.Kind == "model" && status == "success" {
			_, e := s.control.db.Exec(`INSERT INTO console_configured_checks(source_id,provider,kind,model,source_target,fingerprint,state,result) VALUES($1,$2,'model',$3,$4,$5,'done',$6) ON CONFLICT(source_id,provider,kind,model) DO UPDATE SET result=excluded.result`, src.ID, c.Provider, c.Model, controlTarget(src), c.Fingerprint, mustJSON(result))
			if e != nil {
				t.Fatal(e)
			}
		}
		if status != test.want {
			t.Fatalf("%+v: %+v", test, result)
		}
	}
	if otherCalls.Load() != 0 {
		t.Fatal("fell back to another channel")
	}
	after, _, err := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if err != nil || mustJSON(before) != mustJSON(after) {
		t.Fatal("diagnostics mutated routing", err)
	}
}

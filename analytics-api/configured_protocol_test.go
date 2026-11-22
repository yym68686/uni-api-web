package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfiguredProtocolOverridesRetainAliasesAndRejectUnknownModels(t *testing.T) {
	doc := map[string]any{configuredProtocolField: map[string]string{"real": "gemini", "old": "chat"}, "engine": "codex"}
	if err := configuredModelProtocols(doc, map[string]string{"other": "chat"}, map[string]string{"alias": "real"}); err == nil {
		t.Fatal("accepted protocol for a model outside the save")
	}
	if err := configuredModelProtocols(doc, nil, map[string]string{"changed-alias": "real"}); err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(doc[configuredProtocolField]); got != `{"real":"gemini"}` {
		t.Fatal(got)
	}
	if err := configuredModelProtocols(doc, map[string]string{"real": ""}, map[string]string{"changed-alias": "real"}); err != nil || doc[configuredProtocolField] != nil || doc["engine"] != "codex" {
		t.Fatal("reset did not return to the original channel configuration", err, doc)
	}
}

func TestConfiguredProtocolsServeOnOneChannelWithRealGateway(t *testing.T) {
	binary, dsn := os.Getenv("TEST_UNI_API_BINARY"), os.Getenv("TEST_CONTROL_DATABASE_URL")
	if binary == "" || dsn == "" {
		t.Skip("local uni-api binary and PostgreSQL required")
	}
	const gemini = "gemini-4-carbon"
	const claude = "claude-fixture"
	const chat = "chat-fixture"
	const responses = "responses-fixture"
	const provider = "manual"
	hits := make(chan string, 20)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Channel-Fixture") != "retained" {
			t.Error("channel preferences were lost")
		}
		path := strings.TrimPrefix(r.URL.Path, "/tenant")
		if path == r.URL.Path {
			t.Error("deployment prefix was lost")
		}
		r.URL.Path = path
		model := subFixtureProbeModel(r)
		switch {
		case model == gemini && path == "/v1beta/models/"+gemini+":streamGenerateContent":
			subNativeSSE(w, model, "test")
		case model == claude && path == "/v1/messages":
			subNativeSSE(w, model, "test")
		case model == chat && path == "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"chat-fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"test\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"fixture\",\"model\":\"chat-fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		case model == responses && path == "/v1/responses":
			subSSE(w, "test", model)
		default:
			http.Error(w, "wrong protocol for model", 404)
			return
		}
		hits <- path
	}))
	defer upstream.Close()
	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := strings.Split(address, ":")[1]
	listener.Close()
	models := []string{gemini, claude, chat, responses}
	config := map[string]any{
		"providers":   []any{map[string]any{"provider": provider, "base_url": upstream.URL + "/tenant/v1/responses", "api": "fixture-private-upstream", "model": models, "preferences": map[string]any{"headers": map[string]string{"X-Channel-Fixture": "retained"}}}},
		"api_keys":    []any{map[string]any{"api": "fixture-admin", "role": "admin", "model": []string{"all"}}, map[string]any{"api": "fixture-caller-a", "model": []string{"all"}}, map[string]any{"api": "fixture-caller-b", "model": []string{"all"}}},
		"preferences": map[string]any{"AUTO_RETRY": false},
	}
	path := filepath.Join(dir, "api.json")
	if err := os.WriteFile(path, []byte(mustJSON(config)), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary)
	cmd.Dir = dir
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	cmd.Env = append(os.Environ(), "PORT="+port, "DISABLE_DATABASE=true", "UNI_API_CONFIG_PATH="+path,
		"RUST_RESPONSES_CONFIG_SNAPSHOT_PATH="+filepath.Join(dir, "snapshot.json"), "UNI_API_SHARED_MEMORY_RESERVATION_PATH="+filepath.Join(dir, "ledger"),
		"RUST_REQUEST_SPOOL_DIRECTORY="+filepath.Join(dir, "spool"), "RUST_REQUEST_SPOOL_DISK_RESERVE_BPS=0", "RUST_REQUEST_SPOOL_INODE_RESERVE_BPS=0", "NO_PROXY=127.0.0.1,localhost")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Log(log.String())
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := newControlStore(dsn, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src := controlSource{sourceView: sourceView{ID: "manual-protocol-" + randomID()[:10], Name: "Fixture", Base: "http://" + address}, Key: "fixture-admin"}
	if _, err = store.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, src.ID)
	token, err := store.newSession(ctx, "manual-protocol-owner")
	if err != nil {
		t.Fatal(err)
	}
	serve := (&Service{control: store}).Handler()
	var state map[string]any
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		state, _, err = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || state[configuredProtocolField] != true {
		t.Fatal("gateway protocol capability missing", err)
	}
	post := func(path string, body map[string]any, want int) map[string]any {
		t.Helper()
		body["source_id"], body["provider"], body["revision"] = src.ID, provider, state["revision"]
		req := httptest.NewRequest("POST", path, strings.NewReader(mustJSON(body))).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		serve.ServeHTTP(w, req)
		if w.Code != want || strings.Contains(w.Body.String(), "fixture-private-upstream") {
			t.Fatalf("%s: got %d %s", path, w.Code, w.Body.String())
		}
		var result map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result
	}
	assertHit := func(want string) {
		t.Helper()
		select {
		case got := <-hits:
			if got != want {
				t.Fatalf("upstream got %q, want %q", got, want)
			}
		case <-ctx.Done():
			t.Fatal("no upstream request received")
		}
	}
	for _, entry := range []struct{ model, protocol, path string }{
		{gemini, "gemini", "/v1beta/models/" + gemini + ":streamGenerateContent"},
		{claude, "messages", "/v1/messages"},
		{chat, "chat", "/v1/chat/completions"},
	} {
		result := post("/v1/channel-management/detect-protocol", map[string]any{"model": entry.model}, 200)
		if result["protocol"] != entry.protocol {
			t.Fatal("wrong protocol suggestion", result, entry)
		}
		assertHit(entry.path)
	}
	unauthorized, err := newProbeRequest(ctx, src.Base, "fixture-caller-a", claude, "messages", "say test", "ordinary-caller")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized = (configuredProbeTransport{provider: provider, fingerprintProtocol: "endpoint-messages"}).prepare(unauthorized)
	rejected, err := http.DefaultClient.Do(unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	rejected.Body.Close()
	if rejected.StatusCode != http.StatusForbidden {
		t.Fatal("ordinary caller could invoke an endpoint override", rejected.StatusCode)
	}
	unchanged, _, err := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if err != nil || unchanged["revision"] != state["revision"] {
		t.Fatal("detection changed configuration")
	}
	key := "key-" + tokenHash("fixture-caller-a")
	keyB := "key-" + tokenHash("fixture-caller-b")
	post("/v1/channel-management", map[string]any{"api_key_id": key, "edit_provider": provider, "models": models, "position": 1,
		"protocols": map[string]string{gemini: "gemini", claude: "messages", chat: "chat"}}, 200)
	catalog := func(key string) []managementModel {
		t.Helper()
		data, _, err := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
		var rows []managementModel
		if err != nil || decodeMap(data["data"], &rows) != nil {
			t.Fatal("catalog unavailable", err)
		}
		return rows
	}
	for _, row := range catalog(keyB) {
		if row.Provider != provider || row.Protocol != "" || row.Engine != "codex" {
			t.Fatal("other caller was affected", row)
		}
	}
	seen := map[string]string{}
	for _, row := range catalog(key) {
		if row.Provider == configuredImportName(provider, key) {
			seen[row.Model] = row.Protocol
		}
	}
	if len(seen) != 4 || seen[gemini] != "gemini" || seen[claude] != "messages" || seen[chat] != "chat" || seen[responses] != "" {
		t.Fatal("wrong per-model catalog", seen)
	}
	for i, model := range models {
		req, _ := http.NewRequestWithContext(ctx, "POST", src.Base+"/v1/responses", strings.NewReader(mustJSON(map[string]any{"model": model, "input": "say test", "stream": true})))
		req.Header.Set("Authorization", "Bearer fixture-caller-a")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), "test") {
			t.Fatalf("serving %s: %d %s", model, resp.StatusCode, body)
		}
		assertHit([]string{"/v1beta/models/" + gemini + ":streamGenerateContent", "/v1/messages", "/v1/chat/completions", "/v1/responses"}[i])
	}
	state, _, err = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if err != nil {
		t.Fatal(err)
	}
	post("/v1/channel-management", map[string]any{"api_key_id": key, "edit_provider": configuredImportName(provider, key), "models": models, "position": 1,
		"protocols": map[string]string{claude: ""}}, 200)
	for _, row := range catalog(key) {
		if row.Provider == configuredImportName(provider, key) && ((row.Model == claude && row.Protocol != "") || (row.Model == gemini && row.Protocol != "gemini")) {
			t.Fatal("reset changed a different model or did not restore defaults", row)
		}
	}
}

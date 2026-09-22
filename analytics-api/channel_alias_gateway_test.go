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

func TestModelAliasesServeBothNamesWithRealGateway(t *testing.T) {
	binary := os.Getenv("TEST_UNI_API_BINARY")
	if binary == "" {
		t.Skip("local uni-api binary required")
	}
	hits := make(chan string, 20)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		hits <- fmt.Sprint(body["model"])
		if r.Header.Get("X-Fixture") != "kept" {
			t.Error("provider settings lost")
		}
		subSSE(w, "test", fmt.Sprint(body["model"]))
	}))
	defer upstream.Close()
	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(listener.Addr().String(), ":")[1]
	listener.Close()
	config := map[string]any{"providers": []any{map[string]any{"provider": "fugue-codex", "engine": "gpt", "base_url": upstream.URL + "/v1/responses", "api": "fixture-upstream-key", "model": []any{"codex-auto-review", map[string]string{"codex-auto-review": "old-alias"}}, "preferences": map[string]any{"headers": map[string]string{"X-Fixture": "kept"}}}}, "api_keys": []any{map[string]any{"api": "admin-fixture", "role": "admin", "model": []string{"all"}}, map[string]any{"api": "caller-a", "model": []string{"unconfigured-model"}}, map[string]any{"api": "caller-b", "model": []string{"unconfigured-model"}}}, "preferences": map[string]any{"AUTO_RETRY": false}}
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
	src := controlSource{sourceView: sourceView{Base: "http://127.0.0.1:" + port}, Key: "admin-fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var state map[string]any
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		state, _, err = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	keys, _, err := fetchSource(ctx, src, "/v1/api-keys", nil)
	if err != nil {
		t.Fatal(err)
	}
	key := keys["data"].([]any)[1].(map[string]any)["key_id"].(string)
	keyB := keys["data"].([]any)[2].(map[string]any)["key_id"].(string)

	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src.ID = "alias-fixture-" + randomID()[:10]
	src.Name = "Gateway"
	if _, err = store.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, src.ID)
	service := &Service{control: store}
	token, _ := store.newSession(ctx, "alias-owner")
	submit := func(key string, models []string, mappings map[string]string, want int) {
		t.Helper()
		state, _, err = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
		if err != nil {
			t.Fatal(err)
		}
		body := map[string]any{"source_id": src.ID, "provider": "fugue-codex", "api_key_id": key, "revision": state["revision"], "models": models, "model_mappings": mappings, "position": 1}
		req := httptest.NewRequest("POST", "/v1/channel-management", strings.NewReader(mustJSON(body)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("submit %d: %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "fixture-upstream-key") {
			t.Fatal("secret leaked")
		}
	}
	assertCatalog := func(key string, names ...string) {
		t.Helper()
		catalog, _, e := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
		if e != nil {
			t.Fatal(e)
		}
		var rows []struct {
			Model    string `json:"model"`
			Upstream string `json:"upstream_model"`
		}
		decodeMap(catalog["data"], &rows)
		if len(rows) != len(names) {
			t.Fatal("wrong aliases", mustJSON(rows), names)
		}
		for _, name := range names {
			found := false
			for _, row := range rows {
				if row.Model == name && row.Upstream == "codex-auto-review" {
					found = true
				}
			}
			if !found {
				t.Fatal("wrong model mapping", name, mustJSON(rows))
			}
		}
	}
	probe := func(token, model string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "POST", src.Base+"/v1/responses", bytes.NewBufferString(mustJSON(map[string]any{"model": model, "input": "say test", "stream": true})))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal("request failed", resp.StatusCode)
		}
		select {
		case model := <-hits:
			if model != "codex-auto-review" {
				t.Fatal("wrong upstream", model)
			}
		case <-ctx.Done():
			t.Fatal("no upstream request")
		}
	}
	submit(key, []string{}, map[string]string{"gpt-5.6-luna": "codex-auto-review"}, 200)
	assertCatalog(key, "gpt-5.6-luna")
	assertCatalog(keyB)
	probe("caller-a", "gpt-5.6-luna")
	submit(keyB, []string{"codex-auto-review"}, map[string]string{"gpt-5.6-luna": "old-alias"}, 200)
	assertCatalog(keyB, "codex-auto-review", "gpt-5.6-luna")
	probe("caller-b", "gpt-5.6-luna")
	probe("caller-b", "codex-auto-review")
	// Duplicate public names and nonexistent upstream choices cannot change serving state.
	submit(key, []string{"codex-auto-review"}, map[string]string{"codex-auto-review": "old-alias"}, 400)
	submit(key, []string{}, map[string]string{"new-alias": "missing-model"}, 400)
	assertCatalog(key, "gpt-5.6-luna")
	// Editing an existing mapping removes the old alias from catalog and service.
	submit(key, []string{"codex-auto-review"}, map[string]string{}, 200)
	assertCatalog(key, "codex-auto-review")
	probe("caller-a", "codex-auto-review")
	record, e := store.retainedRecord(ctx, src.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := store.retainedSnapshot(record)
	if e != nil {
		t.Fatal(e)
	}
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if _, _, e = subGateway(ctx, src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": state["revision"], "snapshot": snapshot}); e != nil {
		t.Fatal("restore lost aliases", e)
	}
	assertCatalog(keyB, "codex-auto-review", "gpt-5.6-luna")
	probe("caller-b", "gpt-5.6-luna")
	// The same alias document path used by sub2api imports serves both names too.
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	enabled := false
	in := subImportInput{Revision: state["revision"].(string), Models: []string{"codex-auto-review"}, ModelMappings: map[string]string{"review-alias": "codex-auto-review"}, Position: 1, CompactionEnabled: &enabled}
	applied, _, e := service.subImportCompactionChannel(ctx, src, state, in, key, "sub2api-alias-fixture", upstream.URL, "fixture-upstream-key")
	if e != nil {
		t.Fatal(e)
	}
	if applied["revision"] == state["revision"] {
		t.Fatal("not applied")
	}
	catalog, _, _ := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
	if !strings.Contains(mustJSON(catalog), "review-alias") {
		t.Fatal("sub alias absent")
	}
}

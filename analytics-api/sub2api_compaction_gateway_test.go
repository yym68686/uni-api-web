package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Optional real-gateway contract test. All traffic and credentials are local
// fixtures; no production configuration/environment is inherited by the child.
func TestCompactionImportRoutesWithRealGateway(t *testing.T) {
	binary := os.Getenv("TEST_UNI_API_BINARY")
	if binary == "" {
		t.Skip("local uni-api binary required")
	}
	hits := make(chan string, 10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits <- r.URL.Path; subSSE(w, "test") }))
	defer upstream.Close()
	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(listener.Addr().String(), ":")[1]
	listener.Close()
	config := map[string]any{"providers": []any{map[string]any{"provider": "regular", "engine": "gpt", "base_url": upstream.URL + "/regular/v1/responses", "api": "fixture-key", "model": []string{checkModel}}}, "api_keys": []any{map[string]any{"api": "admin-fixture", "model": []string{"all"}}}, "preferences": map[string]any{"AUTO_RETRY": false}}
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	key := keys["data"].([]any)[0].(map[string]any)["key_id"].(string)
	// Include a legacy import: its credentials are intentionally absent from
	// settings/export, and must be recovered from this exact retained revision.
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("g", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src.ID = "gateway-compact-" + randomID()[:10]
	src.Name = "Fixture"
	if _, err = store.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, src.ID)
	state, _, err = subGateway(ctx, src, "POST", "/v1/temporary-channels", map[string]any{"revision": state["revision"], "api_key_id": key, "provider": "sub2api-legacy", "base_url": upstream.URL + "/legacy/v1/responses", "api_key": "legacy-fixture", "models": []string{checkModel}, "position": 2})
	if err != nil {
		t.Fatal(err)
	}
	var live retainedLive
	if decodeMap(state, &live) != nil {
		t.Fatal("invalid live state")
	}
	snapshot := retainedSnapshot{Version: 1, Rules: live.Rules, Channels: []retainedChannel{{Provider: "sub2api-legacy", KeyID: key, Base: upstream.URL + "/legacy/v1/responses", Key: "legacy-fixture", Models: []string{checkModel}}}}
	_, err = store.retainedRecord(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, _ := store.encrypt(mustJSON(snapshot))
	_, err = store.db.ExecContext(ctx, `UPDATE console_control_snapshots SET encrypted_snapshot=$2,revision=$3,target_hash=$4 WHERE source_id=$1`, src.ID, encrypted, live.Revision, controlTarget(src))
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	service := &Service{control: store}
	in := subImportInput{Revision: state["revision"].(string), Models: []string{checkModel}, Position: 1, CompactionEnabled: &enabled}
	applied, code, err := service.subImportCompactionChannel(ctx, src, state, in, key, "sub2api-compaction-fixture", upstream.URL+"/new", "fixture-key")
	if err != nil || code != 200 {
		t.Fatal("atomic creation failed", code, err)
	}
	probe := func(compaction bool, want string) {
		t.Helper()
		input := []any{map[string]string{"role": "user", "content": "test"}}
		if compaction {
			input = append(input, map[string]string{"type": "compaction_trigger"})
		}
		raw := []byte(mustJSON(map[string]any{"model": checkModel, "input": input, "stream": true}))
		req, _ := http.NewRequestWithContext(ctx, "POST", src.Base+"/v1/responses", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer admin-fixture")
		req.Header.Set("Content-Type", "application/json")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode)
		}
		select {
		case got := <-hits:
			if got != want {
				t.Fatalf("compaction=%v got %s want %s", compaction, got, want)
			}
		case <-ctx.Done():
			t.Fatal("no upstream request")
		}
	}
	probe(false, "/new/v1/responses")
	probe(true, "/regular/v1/responses")
	_, code, err = service.settingsGateway(ctx, src, "PATCH", "/v1/channel-settings", map[string]any{"revision": applied["revision"], "operation_id": "compaction-only-fixture", "changes": []any{map[string]any{"provider": "sub2api-compaction-fixture", "set": map[string]any{"/exclude_request_types": []string{}, "/only_request_types": []string{"compaction"}}}}})
	if err != nil || code != 200 {
		t.Fatal("setting compaction-only failed", code, err)
	}
	probe(false, "/regular/v1/responses")
	probe(true, "/new/v1/responses")
	exported, _, err := service.settingsGateway(ctx, src, "GET", "/v1/channel-settings/export", nil)
	if err != nil {
		t.Fatal(err)
	}
	var definition map[string]json.RawMessage
	if decodeMap(exported["temporary_definitions"], &definition) != nil || len(definition["sub2api-compaction-fixture"]) == 0 {
		t.Fatal("channel cannot be retained")
	}
}

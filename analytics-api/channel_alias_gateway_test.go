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
	config["providers"] = append(config["providers"].([]any), map[string]any{"provider": "peer", "engine": "gpt", "base_url": upstream.URL + "/v1/responses", "api": "peer-fixture-key", "model": []any{"codex-auto-review", map[string]string{"codex-auto-review": "old-alias"}}, "preferences": map[string]any{"headers": map[string]string{"X-Fixture": "kept"}}})
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
	in := subImportInput{Revision: state["revision"].(string), Models: []string{"codex-auto-review"}, ModelMappings: map[string]string{"review-alias": "codex-auto-review"}, Position: 1, Positions: map[string]int{"codex-auto-review": 2, "review-alias": 1}, CompactionEnabled: &enabled}
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
	var siteRows []struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	decodeMap(catalog["data"], &siteRows)
	siteRanks := map[string]int{}
	for _, row := range siteRows {
		siteRanks[row.Model]++
		if row.Provider == "sub2api-alias-fixture" {
			want := 1
			if row.Model == "codex-auto-review" {
				want = 2
			}
			if siteRanks[row.Model] != want {
				t.Fatal("site import ignored per-model position", siteRows)
			}
		}
	}
	// Edit a native channel's positions only on the selected caller key.
	adminKey := keys["data"].([]any)[0].(map[string]any)["key_id"].(string)
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	state, _, e = subGateway(ctx, src, "POST", "/v1/channel-controls", map[string]any{"revision": state["revision"], "action": "set", "api_key_id": adminKey, "model": "codex-auto-review", "order": []string{"fugue-codex", "peer"}, "disabled": []string{"peer"}})
	if e != nil {
		t.Fatal(e)
	}
	patchRoutes := func(revision string, want int) {
		t.Helper()
		body := map[string]any{"api_key_id": adminKey, "revision": revision, "moves": []routeMove{{Provider: "fugue-codex", Model: "codex-auto-review", Position: 2}, {Provider: "fugue-codex", Model: "old-alias", Position: 1}}}
		req := httptest.NewRequest("PATCH", "/v1/sources/"+src.ID+"/channel-routes", strings.NewReader(mustJSON(body)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, req)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	staleRevision := state["revision"].(string)
	patchRoutes(staleRevision, 200)
	patchRoutes(staleRevision, 409)
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	var edited retainedLive
	decodeMap(state, &edited)
	for _, rule := range edited.Rules {
		if rule.KeyID == adminKey && rule.Model == "codex-auto-review" {
			if mustJSON(rule.Order) != `["peer","fugue-codex"]` || mustJSON(rule.Disabled) != `["peer"]` {
				t.Fatal("native edit changed policy", rule)
			}
		}
	}
	assertCatalog(keyB, "codex-auto-review", "gpt-5.6-luna")
	// Updating an existing API-key copy uses its own definition and independent
	// model positions, without creating a second copy or changing caller-b.
	copyProvider := configuredImportName("fugue-codex", key)
	editedBody := map[string]any{"source_id": src.ID, "provider": "fugue-codex", "edit_provider": copyProvider, "api_key_id": key, "revision": state["revision"], "models": []string{"codex-auto-review"}, "model_mappings": map[string]string{"review-alias": "codex-auto-review"}, "position": 1, "positions": map[string]int{"codex-auto-review": 2, "review-alias": 1}}
	reqEdit := httptest.NewRequest("POST", "/v1/channel-management", strings.NewReader(mustJSON(editedBody)))
	reqEdit.Header.Set("Content-Type", "application/json")
	reqEdit.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
	resEdit := httptest.NewRecorder()
	service.Handler().ServeHTTP(resEdit, reqEdit)
	if resEdit.Code != 200 {
		t.Fatal(resEdit.Code, resEdit.Body.String())
	}
	catalog, _, _ = fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
	var ordered []struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	decodeMap(catalog["data"], &ordered)
	counts := map[string]int{}
	for _, row := range ordered {
		counts[row.Model]++
		if row.Provider == copyProvider {
			want := 1
			if row.Model == "codex-auto-review" {
				want = 2
			}
			if counts[row.Model] != want {
				t.Fatal("models did not retain individual positions", ordered)
			}
		}
	}
	assertCatalog(keyB, "codex-auto-review", "gpt-5.6-luna")
	record, e = store.retainedRecord(ctx, src.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e = store.retainedSnapshot(record)
	if e != nil {
		t.Fatal(e)
	}
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if _, _, e = subGateway(ctx, src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": state["revision"], "snapshot": snapshot}); e != nil {
		t.Fatal(e)
	}
	probe("caller-a", "review-alias")

	// Base-config edits replace only this caller's route with an owned copy.
	nativeEdit := func(models []string, mappings map[string]string, editProvider string, revision string, want int) {
		t.Helper()
		body := map[string]any{"source_id": src.ID, "provider": "fugue-codex", "edit_provider": editProvider, "api_key_id": adminKey, "revision": revision, "models": models, "model_mappings": mappings, "position": 1}
		req := httptest.NewRequest("POST", "/v1/channel-management", strings.NewReader(mustJSON(body)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		res := httptest.NewRecorder()
		service.Handler().ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("native edit %d: %s", res.Code, res.Body.String())
		}
	}
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	oldRevision := state["revision"].(string)
	nativeEdit([]string{}, map[string]string{"native-alias": "codex-auto-review"}, "fugue-codex", oldRevision, 200)
	nativeEdit([]string{"codex-auto-review"}, nil, "fugue-codex", oldRevision, 409)
	probe("admin-fixture", "native-alias")
	blockedReq, _ := http.NewRequestWithContext(ctx, "POST", src.Base+"/v1/responses", strings.NewReader(`{"model":"codex-auto-review","input":"say test","stream":true}`))
	blockedReq.Header.Set("Authorization", "Bearer admin-fixture")
	blockedReq.Header.Set("Content-Type", "application/json")
	blockedResp, e := http.DefaultClient.Do(blockedReq)
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, blockedResp.Body)
	blockedResp.Body.Close()
	if blockedResp.StatusCode == 200 {
		t.Fatal("unchecked native model still served")
	}
	select {
	case <-hits:
		t.Fatal("unchecked model reached upstream")
	default:
	}
	assertCatalog(keyB, "codex-auto-review", "gpt-5.6-luna")
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	var nativeState retainedLive
	decodeMap(state, &nativeState)
	if !configuredReplacements(state, adminKey)["fugue-codex"] || configuredReplacements(state, keyB)["fugue-codex"] {
		t.Fatal("replacement scope leaked")
	}
	for _, rule := range nativeState.Rules {
		if rule.KeyID == adminKey && rule.Model == "codex-auto-review" && mustJSON(rule.Disabled) != `["peer"]` {
			t.Fatal("peer disable policy changed", rule)
		}
	}
	read := func(path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		res := httptest.NewRecorder()
		service.Handler().ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatal(res.Code, res.Body.String())
		}
		var data map[string]any
		json.Unmarshal(res.Body.Bytes(), &data)
		return data
	}
	routeList := read("/v1/sources/" + src.ID + "/channel-routes")
	var listed []channelRoute
	decodeMap(routeList["data"], &listed)
	foundCopy := false
	for _, r := range listed {
		if r.KeyID != adminKey {
			continue
		}
		if r.Provider == "fugue-codex" {
			t.Fatal("replaced native still listed", r)
		}
		if r.Provider == configuredImportName("fugue-codex", adminKey) {
			foundCopy = true
			if r.Model != "native-alias" || r.OriginProvider != "fugue-codex" || r.Position != 1 {
				t.Fatal("wrong edited membership", r)
			}
		}
	}
	if !foundCopy {
		t.Fatal("replacement missing")
	}
	opts := read("/v1/sub2api/channel-options?source_id=" + src.ID + "&api_key_id=" + adminKey)
	var optionRows []channelRoute
	decodeMap(opts["channels"], &optionRows)
	for _, r := range optionRows {
		if r.Provider == "fugue-codex" {
			t.Fatal("replaced model included in position options")
		}
	}
	nativeEdit([]string{"codex-auto-review"}, map[string]string{"native-alias": "codex-auto-review"}, configuredImportName("fugue-codex", adminKey), state["revision"].(string), 200)
	probe("admin-fixture", "codex-auto-review")
	probe("admin-fixture", "native-alias")
	record, e = store.retainedRecord(ctx, src.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e = store.retainedSnapshot(record)
	if e != nil {
		t.Fatal(e)
	}
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if _, _, e = subGateway(ctx, src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": state["revision"], "snapshot": snapshot}); e != nil {
		t.Fatal(e)
	}
	probe("admin-fixture", "native-alias")
	assertCatalog(keyB, "codex-auto-review", "gpt-5.6-luna")

	// Removing a native route is key-scoped; removing its owned copy cannot
	// resurrect the original provider suppressed during the edit.
	removeBinding := func(provider, caller, revision string, want int, authenticated bool) {
		t.Helper()
		req := httptest.NewRequest("DELETE", "/v1/channel-management", strings.NewReader(mustJSON(map[string]any{"source_id": src.ID, "provider": provider, "api_key_id": caller, "revision": revision})))
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		}
		res := httptest.NewRecorder()
		service.Handler().ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("remove %s: %d %s", provider, res.Code, res.Body.String())
		}
	}
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	rev := state["revision"].(string)
	removeBinding("peer", adminKey, rev, 401, false)
	removeBinding("peer", adminKey, "stale", 409, true)
	removeBinding(configuredImportName("fugue-codex", keyB), adminKey, rev, 409, true)
	removeBinding("peer", adminKey, rev, 200, true)
	routeList = read("/v1/sources/" + src.ID + "/channel-routes")
	decodeMap(routeList["data"], &listed)
	for _, row := range listed {
		if row.KeyID == adminKey && row.Provider == "peer" {
			t.Fatal("removed native still listed", row)
		}
	}
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	removeBinding(configuredImportName("fugue-codex", adminKey), adminKey, state["revision"].(string), 200, true)
	record, e = store.retainedRecord(ctx, src.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e = store.retainedSnapshot(record)
	if e != nil {
		t.Fatal(e)
	}
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if _, _, e = subGateway(ctx, src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": state["revision"], "snapshot": snapshot}); e != nil {
		t.Fatal(e)
	}
	routeList = read("/v1/sources/" + src.ID + "/channel-routes")
	decodeMap(routeList["data"], &listed)
	for _, row := range listed {
		if row.KeyID == adminKey {
			t.Fatal("deleted routes revived after restore", row)
		}
	}
	for _, model := range []string{"codex-auto-review", "native-alias"} {
		req, _ := http.NewRequestWithContext(ctx, "POST", src.Base+"/v1/responses", strings.NewReader(mustJSON(map[string]any{"model": model, "input": "say test", "stream": true})))
		req.Header.Set("Authorization", "Bearer admin-fixture")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Fatal("removed route still serves", model)
		}
		select {
		case <-hits:
			t.Fatal("removed route reached upstream")
		default:
		}
	}
	assertCatalog(keyB, "codex-auto-review", "gpt-5.6-luna")
	probe("caller-b", "codex-auto-review")
	raw, _, e := subGateway(ctx, src, "GET", "/v1/api_config", nil)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(mustJSON(raw["api_config"]), "fugue-codex") || !strings.Contains(mustJSON(raw["api_config"]), "peer") {
		t.Fatal("base config was deleted")
	}
	state, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	nativeEdit([]string{"codex-auto-review"}, nil, "", state["revision"].(string), 200)
	probe("admin-fixture", "codex-auto-review")

}

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
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestChannelSortOrdersPreserveHiddenSlotsAndModelScope(t *testing.T) {
	rows := []batchCatalogRow{{Provider: "a", Model: "m"}, {Provider: "hidden", Model: "m"}, {Provider: "b", Model: "m"}, {Provider: "a", Model: "other"}, {Provider: "b", Model: "other"}}
	changes, err := channelSortOrders(rows, []channelSortSelection{{Model: "m", Providers: []string{"b", "a", "not-in-this-key"}}}, "key", 1)
	if err != nil || len(changes) != 1 || !reflect.DeepEqual(changes[0].Before, []string{"a", "hidden", "b"}) || !reflect.DeepEqual(changes[0].After, []string{"b", "hidden", "a"}) {
		t.Fatal(changes, err)
	}
	if !verifySortCatalog(rows, changes[0], false) || verifySortCatalog(rows, changes[0], true) {
		t.Fatal("bad catalog verification")
	}
	rows[0].Upstream = "changed-alias"
	if verifySortCatalog(rows, changes[0], false) {
		t.Fatal("alias changed without conflict")
	}
	if _, err = channelSortOrders(rows, []channelSortSelection{{Model: "m", Providers: []string{"a", "a"}}}, "key", 1); err == nil {
		t.Fatal("duplicate provider accepted")
	}
}

func TestChannelSortRealGatewayApplyAndUndo(t *testing.T) {
	binary, dsn := os.Getenv("TEST_UNI_API_BINARY"), os.Getenv("TEST_CONTROL_DATABASE_URL")
	if binary == "" || dsn == "" {
		t.Skip("local uni-api binary and PostgreSQL required")
	}
	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(listener.Addr().String(), ":")[1]
	listener.Close()
	providers := []any{}
	for _, name := range []string{"a", "hidden", "b"} {
		providers = append(providers, map[string]any{"provider": name, "base_url": "http://127.0.0.1:1/v1/responses", "api": "fixture-key", "model": []string{"gpt-6-sol", "gpt-6-astra"}})
	}
	config := map[string]any{"providers": providers, "api_keys": []any{map[string]any{"api": "admin-fixture", "role": "admin", "model": []string{"all"}}, map[string]any{"api": "caller-fixture", "model": []string{"all"}}}}
	path := filepath.Join(dir, "api.json")
	os.WriteFile(path, []byte(mustJSON(config)), 0600)
	log, _ := os.Create(filepath.Join(dir, "gateway.log"))
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
	src := controlSource{sourceView: sourceView{ID: "sort-real-" + randomID()[:10], Name: "Local", Base: "http://127.0.0.1:" + port}, Key: "admin-fixture"}
	var keys []struct {
		ID       string `json:"key_id"`
		Position int    `json:"position"`
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		raw, _, e := fetchSource(context.Background(), src, "/v1/api-keys", nil)
		if e == nil && decodeMap(raw["data"], &keys) == nil && len(keys) == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(keys) != 2 {
		t.Fatal("gateway did not start")
	}
	key := ""
	for _, k := range keys {
		if k.Position == 2 {
			key = k.ID
		}
	}
	store, err := newControlStore(dsn, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	defer func() {
		store.db.Exec(`DELETE FROM console_control_snapshots WHERE source_id=$1`, src.ID)
		store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, src.ID)
	}()
	s := &Service{control: store}
	token, _ := store.newSession(context.Background(), "sort-real-fixture")
	call := func(body any) map[string]any {
		r := httptest.NewRequest("POST", "/v1/sources/"+src.ID+"/channel-sort", strings.NewReader(mustJSON(body)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("action %v: %d %s", body.(map[string]any)["action"], w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	prepared := call(map[string]any{"action": "prepare", "api_key_id": key, "models": []channelSortSelection{{Model: "gpt-6-sol", Providers: []string{"b", "a"}}}})
	changes := []channelSortChange{}
	decodeMap(prepared["changes"], &changes)
	if len(changes) != 1 || !reflect.DeepEqual(changes[0].After, []string{"b", "hidden", "a"}) {
		t.Fatal("unexpected real preview", changes)
	}
	call(map[string]any{"action": "apply", "receipt": prepared["receipt"]})
	state, _, _ := subGateway(context.Background(), src, "GET", "/v1/channel-controls", nil)
	actual, err := s.sortCatalog(context.Background(), src, state, key)
	if err != nil || !verifySortCatalog(actual, changes[0], true) {
		t.Fatal("real routing order did not change", err, actual)
	}
	status := call(map[string]any{"action": "status", "receipt": prepared["receipt"]})
	if status["status"] != "applied" {
		t.Fatal("real gateway normalization invalidated receipt", status)
	}
	call(map[string]any{"action": "undo", "receipt": prepared["receipt"]})
	state, _, _ = subGateway(context.Background(), src, "GET", "/v1/channel-controls", nil)
	actual, err = s.sortCatalog(context.Background(), src, state, key)
	if err != nil || !verifySortCatalog(actual, changes[0], false) {
		t.Fatal("real routing order did not restore", err, actual)
	}
	// Include a key-owned temporary channel with custom settings. Gateway JSON
	// canonicalization must not break the receipt or discard its definition.
	base, _, err := s.importSnapshot(context.Background(), src, state, state["revision"].(string))
	if err != nil {
		t.Fatal(err)
	}
	definition := json.RawMessage(`{"preferences":{"headers":{"X-Test":"retained"}},"provider":"temporary-sort","api":"temporary-fixture","base_url":"http://127.0.0.1:1/v1/responses","model":["gpt-6-sol"]}`)
	base.Channels = append(base.Channels, retainedChannel{Provider: "temporary-sort", KeyID: key, Base: "http://127.0.0.1:1/v1/responses", Key: "temporary-fixture", Models: []string{"gpt-6-sol"}, Definition: definition})
	state, _, err = subGateway(context.Background(), src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": state["revision"], "snapshot": base})
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := s.importSnapshot(context.Background(), src, state, state["revision"].(string))
	if err != nil {
		t.Fatal(err)
	}
	prepared = call(map[string]any{"action": "prepare", "api_key_id": key, "models": []channelSortSelection{{Model: "gpt-6-sol", Providers: []string{"b", "a"}}}})
	call(map[string]any{"action": "apply", "receipt": prepared["receipt"]})
	status = call(map[string]any{"action": "status", "receipt": prepared["receipt"]})
	if status["status"] != "applied" {
		t.Fatal("temporary definition normalization broke receipt", status)
	}
	call(map[string]any{"action": "undo", "receipt": prepared["receipt"]})
	state, _, _ = subGateway(context.Background(), src, "GET", "/v1/channel-controls", nil)
	after, _, err := s.importSnapshot(context.Background(), src, state, state["revision"].(string))
	if err != nil || sortSnapshotDigest(before) != sortSnapshotDigest(after) {
		t.Fatal("temporary definition or settings changed", err)
	}
}

func TestChannelSortReceiptsAuthenticateOwnerSourceAndTarget(t *testing.T) {
	store := &controlStore{key: []byte(strings.Repeat("x", 32))}
	src := controlSource{sourceView: sourceView{ID: "s", Base: "https://gateway.test"}, Key: "secret"}
	receipt := channelSortReceipt{Version: 1, Owner: "owner", Source: src.ID, Target: controlTarget(src)}
	token, err := store.sealChannelSort(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.openChannelSort(token, "owner", src); err != nil {
		t.Fatal(err)
	}
	if _, err = store.openChannelSort(token, "other", src); err == nil {
		t.Fatal("cross-owner token")
	}
	src.Base = "https://changed.test"
	if _, err = store.openChannelSort(token, "owner", src); err == nil {
		t.Fatal("retargeted source")
	}
	if _, err = store.openChannelSort(token+"x", "owner", src); err == nil {
		t.Fatal("tampered token")
	}
}

func TestSortDigestCanonicalizesRuleOrderAndPreservesSettings(t *testing.T) {
	a := retainedSnapshot{Version: 2, Rules: []retainedRule{{KeyID: "z", Order: []string{"b", "a"}, Disabled: []string{"y", "x"}}, {KeyID: "a", Order: []string{}}}, Channels: []retainedChannel{}}
	b := retainedSnapshot{Version: 2, Rules: []retainedRule{{KeyID: "a"}, {KeyID: "z", Order: []string{"b", "a"}, Disabled: []string{"x", "y"}}}}
	if sortSnapshotDigest(a) != sortSnapshotDigest(b) {
		t.Fatal("representation difference made undo impossible")
	}
	b.Rules[1].Order = []string{"a", "b"}
	if sortSnapshotDigest(a) == sortSnapshotDigest(b) {
		t.Fatal("order change not detected")
	}
}

func TestChannelSortPrepareApplyUndoHTTP(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "sort-" + randomID()[:10]
	owner := "sort-owner-" + randomID()[:10]
	key := "key-" + strings.Repeat("a", 64)
	other := "key-" + strings.Repeat("b", 64)
	revision := "boot:1"
	baseRevision := "base-1"
	writes := 0
	snapshot := retainedSnapshot{Version: 2, Channels: []retainedChannel{}, Settings: map[string]json.RawMessage{}, Rules: []retainedRule{{KeyID: other, Model: "m", Order: []string{"a", "hidden", "b"}, Disabled: []string{"hidden"}}}}
	initial := sortSnapshotDigest(snapshot)
	state := func() map[string]any {
		return map[string]any{"revision": revision, "instance_id": "boot", "config_revision": baseRevision, "temporary_channel_restore": true, "temporary_channel_management": true, "channel_settings": true, "rules": snapshot.Rules, "temporary_channels": []any{}}
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/channel-controls":
			writeJSON(w, 200, state())
		case "/v1/channel-settings/export":
			if snapshot.Settings == nil {
				snapshot.Settings = map[string]json.RawMessage{}
			}
			writeJSON(w, 200, map[string]any{"revision": revision, "temporary_definitions": map[string]any{}, "channel_settings": snapshot.Settings})
		case "/v1/api-keys":
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"key_id": key, "position": 1}, map[string]any{"key_id": other, "position": 2}}})
		case "/v1/model-channels":
			order := []string{"a", "hidden", "b"}
			for _, rule := range snapshot.Rules {
				if rule.KeyID == r.URL.Query().Get("api_key_id") && rule.Model == "m" {
					order = rule.Order
				}
			}
			rows := []batchCatalogRow{}
			for _, p := range order {
				rows = append(rows, batchCatalogRow{Provider: p, Model: "m", Upstream: "up"})
			}
			rows = append(rows, batchCatalogRow{Provider: "other-model", Model: "untouched"})
			writeJSON(w, 200, map[string]any{"data": rows})
		case "/v1/channel-controls/restore":
			var input struct {
				Revision string           `json:"revision"`
				Snapshot retainedSnapshot `json:"snapshot"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			if input.Revision != revision {
				http.Error(w, "revision mismatch", 409)
				return
			}
			writes++
			snapshot = input.Snapshot
			revision = fmt.Sprintf("boot:%d", writes+1)
			writeJSON(w, 200, state())
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	src := controlSource{sourceView: sourceView{ID: id, Name: "Fixture", Base: gateway.URL}, Key: "fixture"}
	if _, err = store.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec(`DELETE FROM console_control_snapshots WHERE source_id=$1`, id)
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
	s := &Service{control: store}
	session, _ := store.newSession(context.Background(), owner)
	call := func(body any, want int) map[string]any {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/v1/sources/"+id+"/channel-sort", strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
		}
		var response map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &response)
		return response
	}
	prepare := call(map[string]any{"action": "prepare", "api_key_id": key, "models": []channelSortSelection{{Model: "m", Providers: []string{"b", "a"}}}}, 200)
	if writes != 0 || len(prepare["changes"].([]any)) != 1 {
		t.Fatal("preview wrote routes or crossed keys")
	}
	token := prepare["receipt"]
	call(map[string]any{"action": "apply", "receipt": token}, 200)
	if writes != 1 {
		t.Fatal(writes)
	}
	if !reflect.DeepEqual(snapshot.Rules[0].Disabled, []string{"hidden"}) {
		t.Fatal("disabled state changed")
	}
	call(map[string]any{"action": "apply", "receipt": token}, 200)
	if writes != 1 {
		t.Fatal("double apply")
	}
	status := call(map[string]any{"action": "status", "receipt": token}, 200)
	if status["status"] != "applied" {
		t.Fatal(status)
	}
	baseRevision = "base-2"
	call(map[string]any{"action": "undo", "receipt": token}, 409)
	if writes != 1 {
		t.Fatal("undo overwrote changed base")
	}
	baseRevision = "base-1"
	call(map[string]any{"action": "undo", "receipt": token}, 200)
	if sortSnapshotDigest(snapshot) != initial || writes != 2 {
		t.Fatal("undo did not restore absence of original scoped rule")
	}
	call(map[string]any{"action": "undo", "receipt": token}, 200)
	if writes != 2 {
		t.Fatal("double undo")
	}
	all := call(map[string]any{"action": "prepare", "api_key_id": "", "models": []channelSortSelection{{Model: "m", Providers: []string{"b", "a"}}}}, 200)
	if len(all["changes"].([]any)) != 2 {
		t.Fatal("all keys not expanded")
	}
	snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: key, Model: "external", Order: []string{"external"}})
	revision = "external:1"
	call(map[string]any{"action": "apply", "receipt": all["receipt"]}, 409)
	if writes != 2 {
		t.Fatal("stale preview wrote routes")
	}
}

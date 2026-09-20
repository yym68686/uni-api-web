package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSettingsApplyRetainsEncryptedIntentAndRecoversLostACK(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "settings-" + randomID()[:8]
	defer func() {
		store.db.Exec(`DELETE FROM console_channel_settings_operations WHERE source_id=$1`, id)
		store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
	}()
	ctx := context.Background()
	revision := "r0"
	calls := 0
	lost := true
	settings := map[string]any{}
	candidate := map[string]any{"one": map[string]any{"set": map[string]any{"/api": "new-private-key", "/preferences/post_body_parameter_overrides/instructions": "<hello> 中文"}, "remove": []string{}}}
	intent := map[string]any{"settings": candidate, "temporary_definitions": map[string]any{}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Error("configuration did not use the dedicated admin key")
		}
		switch r.URL.Path {
		case "/v1/channel-settings/validate":
			writeJSON(w, 200, map[string]any{"status": "validated", "revision": revision, "intent": intent, "previews": []any{map[string]any{"provider": "one"}}})
		case "/v1/channel-settings/export":
			writeJSON(w, 200, map[string]any{"revision": revision, "channel_settings": settings, "temporary_definitions": map[string]any{}})
		case "/v1/channel-settings":
			var pending, encrypted string
			if e := store.db.QueryRow(`SELECT status,encrypted_intent FROM console_channel_settings_operations WHERE id=$1`, id).Scan(&pending, &encrypted); e != nil || pending != "pending" || strings.Contains(encrypted, "new-private-key") {
				t.Error("intent not durable/encrypted before gateway mutation", e)
			}
			calls++
			settings = candidate
			revision = "r1"
			if lost {
				lost = false
				http.Error(w, "lost ACK", 502)
				return
			}
			writeJSON(w, 200, map[string]any{"status": "applied", "revision": revision, "intent": intent})
		case "/v1/channel-settings/operations/" + id:
			http.NotFound(w, r) // Simulated process restart loses the in-memory operation cache.
		case "/v1/channel-controls":
			writeJSON(w, 200, map[string]any{"revision": revision, "instance_id": "boot-2", "channel_settings": true, "channel_settings_digest": tokenHash(canonicalSettings(settings)), "rules": []any{}, "temporary_channels": []any{}})
		case "/v1/channel-settings/providers":
			writeJSON(w, 200, map[string]any{"providers": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	src := controlSource{sourceView: sourceView{ID: id, Name: "settings", Base: upstream.URL}, Key: "catalog-key", ConfigKey: "admin-secret"}
	if _, err = store.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	svc := &Service{control: store}
	body := fmt.Sprintf(`{"revision":"r0","operation_id":%q,"changes":[{"provider":"one","set":{"/api":"new-private-key"}}]}`, id)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		svc.controlHandler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "new-private-key") {
			t.Fatal("plaintext credential reached browser")
		}
		return w
	}
	out := call("PATCH", "/v1/sources/"+id+"/channel-settings", body)
	if !strings.Contains(out.Body.String(), `"pending"`) {
		t.Fatal(out.Body.String())
	}
	out = call("GET", "/v1/sources/"+id+"/channel-settings/operations/"+id, "")
	if !strings.Contains(out.Body.String(), `"applied"`) {
		t.Fatal(out.Body.String())
	}
	record, err := store.retainedRecord(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.retainedSnapshot(record)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 2 || !strings.Contains(string(snapshot.Settings["one"]), "new-private-key") {
		t.Fatal("full settings did not survive retention")
	}
	if strings.Contains(record.Encrypted, "new-private-key") {
		t.Fatal("retained secret is plaintext")
	}
	if !controlEquivalent(retainedLive{SettingsSupported: true, SettingsDigest: tokenHash(canonicalSettings(settings))}, snapshot) {
		t.Fatal("canonical settings incorrectly differ (HTML/unicode escaping)")
	}
	call("PATCH", "/v1/sources/"+id+"/channel-settings", body)
	if calls != 1 {
		t.Fatal("retried an already applied operation")
	}
}

func TestSettingsGatewayOldVersionAndTemplateSecretBoundary(t *testing.T) {
	up := httptest.NewServer(http.NotFoundHandler())
	defer up.Close()
	svc := &Service{}
	_, code, e := svc.settingsGateway(context.Background(), controlSource{sourceView: sourceView{Base: up.URL}}, "GET", "/v1/channel-settings", nil)
	if code != 404 || e == nil || !strings.Contains(e.Error(), "尚不支持") {
		t.Fatal(code, e)
	}
	var value any
	_ = json.Unmarshal([]byte(`{"z":"<x>&中文","a":1}`), &value)
	if canonicalSettings(value) != `{"a":1,"z":"<x>&中文"}` {
		t.Fatal(canonicalSettings(value))
	}
}

// Optional cross-language regression against a disposable local native gateway.
func TestSettingsNativeGatewayRetention(t *testing.T) {
	base := os.Getenv("TEST_SETTINGS_GATEWAY_URL")
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if base == "" || dsn == "" {
		t.Skip("isolated native gateway and PostgreSQL required")
	}
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatal("test only permits an isolated local gateway")
	}
	store, err := newControlStore(dsn, strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "native-settings-" + randomID()[:8]
	defer func() {
		store.db.Exec(`DELETE FROM console_channel_settings_operations WHERE source_id=$1`, id)
		store.db.Exec(`DELETE FROM console_configured_channels WHERE source_id=$1`, id)
		store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
	}()
	src := controlSource{sourceView: sourceView{ID: id, Base: base, Name: "native fixture"}, Key: "qa-admin"}
	if _, err = store.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	svc := &Service{control: store}
	view, _, err := svc.settingsGateway(context.Background(), src, "GET", "/v1/channel-settings?provider=example", nil)
	if err != nil {
		t.Fatal(err)
	}
	in := channelSettingMutation{Operation: id, Revision: view["revision"].(string), Changes: []channelSettingChange{{Provider: "example", Set: map[string]any{"/preferences/cooldown_period": 0.0, "/api": []string{"changed-test-key", "other-test-key"}, "/preferences/headers": map[string]any{"X-Value": "<test>"}, "/model": []any{"gpt-6-astra", map[string]string{"upstream": "alias"}}}}}}
	req := httptest.NewRequest("PATCH", "/v1/sources/"+id+"/channel-settings", strings.NewReader(mustJSON(in)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.controlHandler().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"applied"`) {
		t.Fatalf("apply %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "changed-test-key") {
		t.Fatal("secret exposed")
	}
	record, _ := store.retainedRecord(context.Background(), id)
	saved, err := store.retainedSnapshot(record)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := svc.settingsGateway(context.Background(), src, "GET", "/v1/channel-controls", nil)
	if err != nil {
		t.Fatal(err)
	}
	live, _ := decodeRetained(raw)
	if !controlEquivalent(live, saved) {
		t.Fatal("Rust and Go disagree about retained settings")
	}
	// Replace the running overlay by an empty v1 snapshot, then restore the v2 snapshot.
	empty, _, err := svc.settingsGateway(context.Background(), src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": live.Revision, "snapshot": retainedSnapshot{Version: 1, Rules: []retainedRule{}, Channels: []retainedChannel{}}})
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := svc.settingsGateway(context.Background(), src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": empty["revision"], "snapshot": saved})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := decodeRetained(restored)
	if !controlEquivalent(after, saved) {
		t.Fatal("recovery changed exact settings")
	}
}

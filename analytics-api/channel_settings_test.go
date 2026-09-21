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
	var applied map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	// Generic creation must survive the Go persistence / Rust restoration boundary,
	// including names without legacy prefixes and cloud auth without an api field.
	keys, _, err := svc.settingsGateway(context.Background(), src, "GET", "/v1/api-keys", nil)
	if err != nil {
		t.Fatal(err)
	}
	keyID := keys["data"].([]any)[0].(map[string]any)["key_id"].(string)
	creation := channelSettingMutation{Operation: id + "-create", Revision: applied["revision"].(string), Changes: []channelSettingChange{{Provider: "custom-cloud.1", CreateToKey: keyID, Set: map[string]any{
		"/engine": "aws", "/base_url": "https://bedrock.example", "/aws_access_key": "fixture-access", "/aws_secret_key": "fixture-secret", "/model": []any{map[string]string{"vendor/model": "public-cloud"}},
	}}}}
	req = httptest.NewRequest("PATCH", "/v1/sources/"+id+"/channel-settings", strings.NewReader(mustJSON(creation)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	svc.controlHandler().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"applied"`) {
		t.Fatalf("creation %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "fixture-secret") {
		t.Fatal("created secret exposed")
	}
	record, _ := store.retainedRecord(context.Background(), id)
	saved, err := store.retainedSnapshot(record)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Channels) != 1 || saved.Channels[0].Provider != "custom-cloud.1" || !strings.Contains(string(saved.Channels[0].Definition), "fixture-secret") {
		t.Fatal("full generic definition not retained")
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

func TestSettingsRevealRequiresSessionAdminAndReturnsOnlyRequestedKeys(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "reveal-" + randomID()[:8]
	user := id
	defer func() {
		store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
		store.db.Exec(`DELETE FROM console_sessions WHERE username=$1`, user)
		store.db.Exec(`DELETE FROM console_users WHERE username=$1`, user)
	}()
	_, err = store.db.Exec(`INSERT INTO console_users(username,password_hash)VALUES($1,'fixture')`, user)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.newSession(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	reject := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/channel-settings/secrets" || r.URL.Query().Get("provider") != "one" || r.URL.Query().Get("revision") != "revision-one" {
			t.Error("incorrect reveal request")
		}
		if r.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Error("reveal did not use admin credential")
		}
		if reject {
			http.Error(w, "denied", 403)
			return
		}
		writeJSON(w, 200, map[string]any{"provider": "one", "revision": "revision-one", "keys": map[string]string{"reference-one": "fixture-private-key"}, "unrelated": "must-not-forward"})
	}))
	defer up.Close()
	src := controlSource{sourceView: sourceView{ID: id, Name: id, Base: up.URL}, Key: "catalog-key", ConfigKey: "admin-secret"}
	if _, err = store.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	svc := &Service{control: store}
	handler := svc.Handler()
	call := func(auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/sources/"+id+"/channel-settings/secrets?provider=one&revision=revision-one", nil)
		if auth {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call(false); w.Code != 401 || calls != 0 {
		t.Fatal("unauthenticated reveal reached gateway")
	}
	w := call(true)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("reveal or cache policy failed", w.Code)
	}
	var payload map[string]any
	if json.Unmarshal(w.Body.Bytes(), &payload) != nil || len(payload) != 1 || payload["keys"].(map[string]any)["reference-one"] != "fixture-private-key" {
		t.Fatal("incorrect reveal projection")
	}
	reject = true
	if w = call(true); w.Code != 403 || strings.Contains(w.Body.String(), "fixture-private-key") {
		t.Fatal("admin denial not preserved")
	}
	var auditCount int
	store.db.QueryRow(`SELECT count(*) FROM console_channel_settings_operations WHERE source_id=$1`, id).Scan(&auditCount)
	if auditCount != 0 {
		t.Fatal("read created audit/mutation state")
	}
}

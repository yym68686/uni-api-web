package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestConfiguredChannelKeysUseIndependentAdminCredential(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := "config-key-" + randomID()[:12]
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, source)
	configCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api-keys":
			if r.Header.Get("Authorization") != "Bearer observation-fixture" {
				t.Error("observation switched to admin credential")
			}
			writeJSON(w, 200, map[string]any{"can_inspect_all": true, "data": []any{}})
		case "/v1/api_config":
			configCalls++
			if r.Header.Get("Authorization") != "Bearer admin-fixture" {
				http.Error(w, "Permission denied", 403)
				return
			}
			writeJSON(w, 200, map[string]any{"api_config": map[string]any{"providers": []any{
				map[string]any{"provider": "walkcoding008", "base_url": "https://walkcoding.example/v1/responses", "api": []string{"route-fixture-1", "route-fixture-2"}},
				map[string]any{"provider": "other", "base_url": "https://other.example/v1/responses", "api": "unrelated-fixture"},
			}, "api_keys": []any{map[string]string{"api": "caller-fixture"}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	src := controlSource{sourceView: sourceView{ID: source, Name: "Fixture", Base: upstream.URL}, Key: "observation-fixture"}
	if _, err = store.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	originalTarget := controlTarget(src)
	s := &Service{control: store}
	token, _ := store.newSession(context.Background(), "config-key-fixture")
	call := func(method, path string, body any, authenticated bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		if authenticated {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	info := "/v1/sources/" + source + "/channel-info?provider=walkcoding008"
	if w := call("GET", info+"&reveal=true", nil, true); w.Code != 503 {
		t.Fatal("expected original permission mismatch", w.Code)
	}
	if w := call("GET", info+"&reveal=true", nil, false); w.Code != 401 {
		t.Fatal("anonymous key access", w.Code)
	}
	save := func(key string) *httptest.ResponseRecorder {
		return call("PUT", "/v1/sources/"+source, map[string]string{"name": "Fixture", "base": upstream.URL, "config_key": key}, true)
	}
	if w := save("invalid-fixture"); w.Code != 400 {
		t.Fatal("unvalidated credential accepted", w.Code)
	}
	if w := save("admin-fixture"); w.Code != 200 || strings.Contains(w.Body.String(), "admin-fixture") {
		t.Fatal("configuration access not saved safely", w.Code)
	}
	var encrypted string
	if err = store.db.QueryRow(`SELECT encrypted_config_key FROM console_sources WHERE id=$1`, source).Scan(&encrypted); err != nil || encrypted == "admin-fixture" || encrypted == "" {
		t.Fatal("credential not encrypted", err)
	}
	stored, err := store.source(context.Background(), source)
	if err != nil || stored.ConfigKey != "admin-fixture" || stored.Key != "observation-fixture" || controlTarget(stored) != originalTarget {
		t.Fatal("observation or recovery identity changed", err)
	}
	// Leaving the optional field blank preserves it on the same source.
	if w := save(""); w.Code != 200 {
		t.Fatal("blank edit lost configuration access", w.Code)
	}
	for _, path := range []string{info, "/v1/channel-sites", "/v1/sources"} {
		w := call("GET", path, nil, true)
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
		for _, secret := range []string{"route-fixture", "admin-fixture", "observation-fixture", "unrelated-fixture", "caller-fixture"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("secret included without reveal", path)
			}
		}
	}
	w := call("GET", info+"&reveal=true", nil, true)
	var revealed struct {
		Keys []string `json:"api_keys"`
		URL  string   `json:"dashboard_url"`
	}
	if json.Unmarshal(w.Body.Bytes(), &revealed) != nil || w.Code != 200 || len(revealed.Keys) != 2 || revealed.Keys[0] != "route-fixture-1" || revealed.URL != "https://walkcoding.example/dashboard" {
		t.Fatal("requested provider keys unavailable", w.Code)
	}
	for _, secret := range []string{"admin-fixture", "observation-fixture", "unrelated-fixture", "caller-fixture"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("unrelated secret exposed")
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("key response cached")
	}
	// An unavailable optional credential does not break configuration recovery.
	if _, err = store.db.Exec(`UPDATE console_sources SET encrypted_config_key='invalid' WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	stored, err = store.source(context.Background(), source)
	if err != nil || stored.Key != src.Key || controlTarget(stored) != originalTarget {
		t.Fatal("optional credential blocked recovery", err)
	}
	before := configCalls
	if _, err = configuredProviders(context.Background(), stored); err == nil || configCalls != before {
		t.Fatal("invalid optional secret silently fell back")
	}
	// Changing the source address cannot forward its saved admin credential.
	newOrigin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api-keys" || r.Header.Get("Authorization") != "Bearer observation-fixture" {
			t.Error("admin credential escaped to another source")
		}
		writeJSON(w, 200, map[string]any{"can_inspect_all": true, "data": []any{}})
	}))
	defer newOrigin.Close()
	w = call("PUT", "/v1/sources/"+source, map[string]string{"name": "Moved", "base": newOrigin.URL}, true)
	if w.Code != 200 {
		t.Fatal("source relocation failed", w.Code)
	}
	stored, err = store.source(context.Background(), source)
	if err != nil || stored.ConfigKey != "" || stored.HasConfigKey {
		t.Fatal("old admin credential retained on new origin")
	}
}

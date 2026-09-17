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

func TestChannelControlsProxyIsScopedAndDoesNotPersistIntent(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	calls := 0
	upstreamStatus := 200
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/channel-controls" || r.Header.Get("Authorization") != "Bearer control-test-secret" {
			t.Error("wrong destination or auth")
		}
		if r.Method == "POST" {
			var v map[string]any
			json.NewDecoder(r.Body).Decode(&v)
			if v["api_key_id"] != "key-one" {
				t.Error("key namespace not stripped", v)
			}
		}
		if upstreamStatus != 200 {
			http.Error(w, "control-test-secret upstream detail", upstreamStatus)
			return
		}
		writeJSON(w, 200, map[string]any{"revision": "process:1", "rules": []any{}, "reset_on_restart": true})
	}))
	defer upstream.Close()
	_, err = store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: "control-test", Name: "control", Base: upstream.URL}, Key: "control-test-secret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(stateTestEngine(t), Config{Upstream: upstream.URL, PublicOrigin: "https://console.test"})
	service.control = store
	token, err := store.newSession(context.Background(), "checker")
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, origin, key string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://console.test/v1/sources/control-test/channel-controls", strings.NewReader(`{"revision":"process:0","action":"set","api_key_id":"`+key+`","order":["a"],"disabled":[]}`))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if auth {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		}
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "", "control-test::key-one", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "https://other.test", "control-test::key-one", true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "https://console.test", "other-source::key-one", true); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if calls != 0 {
		t.Fatal("unauthorized mutation forwarded")
	}
	if w := call("GET", "", "", true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "https://console.test", "control-test::key-one", true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	upstreamStatus = 409
	if w := call("POST", "https://console.test", "control-test::key-one", true); w.Code != 409 || strings.Contains(w.Body.String(), "control-test-secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	got, err := store.source(context.Background(), "control-test")
	if err != nil || got.Key != "control-test-secret" {
		t.Fatal("source modified")
	}
}

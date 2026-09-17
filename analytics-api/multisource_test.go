package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSourceIsolationAndAllTotals(t *testing.T) {
	e := stateTestEngine(t)
	now := time.Now().UnixMilli()
	ctx := context.Background()
	n := int64(100)
	objects := []FactObject{}
	for _, id := range []string{"primary", "secondary"} {
		objects = append(objects, FactObject{Key: id, ETag: "1", Facts: []Fact{{Schema: 1, SourceID: id, EventID: id + "-request", Kind: "request", AtMS: now, Provider: "same", Model: "gpt-6-astra", UpstreamModel: "gpt-6-astra", Outcome: "success", InputTokens: &n}, {Schema: 1, SourceID: id, EventID: id + "-attempt", Kind: "attempt", AtMS: now, Provider: "same", Model: "gpt-6-astra", UpstreamModel: "gpt-6-astra", Outcome: "success"}}})
	}
	if err := e.ImportBatch(ctx, objects); err != nil {
		t.Fatal(err)
	}
	if err := e.ImportBatch(ctx, objects); err != nil {
		t.Fatal(err)
	}
	all, err := e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Data) != 2 || all.Total["requests"] != float64(2) {
		t.Fatalf("merged sources: %+v", all)
	}
	single, err := e.Query(ctx, QueryFilter{Range: "all", SourceID: "secondary", Timeseries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Data) != 1 || single.Data[0].SourceID != "secondary" || single.Total["requests"] != float64(1) {
		t.Fatalf("bad filter: %+v", single)
	}
	none, err := e.Query(ctx, QueryFilter{Range: "all", SourceIDs: []string{}})
	if err != nil || len(none.Data) != 0 {
		t.Fatalf("empty allowed scope leaked: %+v %v", none, err)
	}
}
func TestControlPostgresSessionAndSources(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_CONTROL_DATABASE_URL for database integration")
	}
	store, err := newControlStore(dsn, strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.db.Exec(`TRUNCATE console_users,console_sessions,console_sources`)
	if err = store.ensureBootstrap("admin", "a-long-test-password"); err != nil {
		t.Fatal(err)
	}
	if err = store.ensureBootstrap("attacker", "another-password"); err != nil {
		t.Fatal(err)
	}
	if store.authenticate(context.Background(), "attacker", "another-password") {
		t.Fatal("bootstrap reopened")
	}
	if !store.authenticate(context.Background(), "admin", "a-long-test-password") {
		t.Fatal("password failed")
	}
	token, err := store.newSession(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.session(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	store.logout(context.Background(), token)
	if _, err = store.session(context.Background(), token); err == nil {
		t.Fatal("logout did not revoke")
	}
	src, err := store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: "primary", Name: "One", Base: "https://example.com"}, Key: "platform-secret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(src)
	if strings.Contains(string(raw), "platform-secret") {
		t.Fatal("secret leak")
	}
	var enc string
	store.db.QueryRow(`SELECT encrypted_key FROM console_sources WHERE id='primary'`).Scan(&enc)
	if strings.Contains(enc, "platform-secret") {
		t.Fatal("stored plaintext")
	}
	got, err := store.source(context.Background(), "primary")
	if err != nil || got.Key != "platform-secret" {
		t.Fatal("credential round trip", err)
	}
	e := stateTestEngine(t)
	s, err := NewService(e, Config{Upstream: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	s.control = store
	for _, path := range []string{"/v1/sources", "/v1/analytics", "/v1/prices", "/v1/sources/all/proxy/v1/api-keys"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer platform-secret")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatalf("bearer bypass %s %d", path, w.Code)
		}
	}
	req := httptest.NewRequest("POST", "/v1/auth/setup", strings.NewReader(`{"Username":"attacker","Password":"another-password"}`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code == 200 || w.Code == 201 {
		t.Fatal("public setup exposed")
	}
	req = httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(`{"Username":"admin","Password":"a-long-test-password"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login %d", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure cookie")
	}

	t.Run("public origin survives proxy host rewriting", func(t *testing.T) {
		s.cfg.PublicOrigin = "https://console.example"
		defer func() { s.cfg.PublicOrigin = "" }()
		for _, origin := range []string{"https://console.example", "https://evil.example", "http://console.example"} {
			r := httptest.NewRequest("POST", "http://internal-service/v1/auth/login", strings.NewReader(`{"username":"admin","password":"a-long-test-password"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", origin)
			r.Header.Set("X-Forwarded-Host", "internal-proxy")
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			want := 403
			if origin == s.cfg.PublicOrigin {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("origin %s got %d want %d", origin, w.Code, want)
			}
		}
	})
	t.Run("source proxies preserve gateway identity and key scope", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer platform-secret" {
				t.Error("missing server credential")
			}
			if r.URL.Path == "/v1/api-keys" {
				writeJSON(w, 200, map[string]any{"can_inspect_all": true, "data": []map[string]any{{"key_id": "same-key"}}})
				return
			}
			if k := r.URL.Query().Get("api_key_id"); k != "" && k != "same-key" {
				t.Error("namespaced key escaped upstream", k)
			}
			writeJSON(w, 200, map[string]any{"data": []map[string]any{{"provider": "same", "model": "same"}}})
		}))
		defer upstream.Close()
		for _, id := range []string{"primary", "secondary"} {
			_, e := store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: id, Name: id, Base: upstream.URL}, Key: "platform-secret"}, false)
			if e != nil {
				t.Fatal(e)
			}
		}
		call := func(path string) map[string]any {
			t.Helper()
			rq := httptest.NewRequest("GET", path, nil)
			rq.AddCookie(cookie)
			rw := httptest.NewRecorder()
			s.Handler().ServeHTTP(rw, rq)
			if rw.Code != 200 {
				t.Fatalf("proxy %d", rw.Code)
			}
			var d map[string]any
			json.Unmarshal(rw.Body.Bytes(), &d)
			return d
		}
		keys := call("/v1/sources/all/proxy/v1/api-keys")["data"].([]any)
		if len(keys) != 2 || keys[0].(map[string]any)["key_id"] == keys[1].(map[string]any)["key_id"] {
			t.Fatal("key namespace collision")
		}
		channels := call("/v1/sources/all/proxy/v1/model-channels?api_key_id=secondary%3A%3Asame-key")["data"].([]any)
		if len(channels) != 1 || channels[0].(map[string]any)["source_id"] != "secondary" {
			t.Fatal("key leaked to another source")
		}
		store.deleteSource(context.Background(), "secondary")
		if _, err := store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: "secondary", Name: "secondary", Base: upstream.URL}, Key: "platform-secret"}, true); err != nil {
			t.Fatal("bootstrap should not fail or revive a disabled source", err)
		}
		if _, err := store.source(context.Background(), "secondary"); err == nil {
			t.Fatal("bootstrap revived removed source")
		}
	})
	req = httptest.NewRequest("POST", "/v1/sources", strings.NewReader(`{}`))
	req.AddCookie(cookie)
	req.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("CSRF accepted")
	}
}

func TestSourceFreshnessSurvivesEmptyWindowAndHonorsSourceScope(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	old := now - 3600_000
	fresh := now - 10_000
	err := e.ImportBatch(ctx, []FactObject{{Key: "old", ETag: "1", Facts: []Fact{{Schema: 1, EventID: "old", SourceID: "old", Kind: "request", AtMS: old}}}, {Key: "fresh", ETag: "1", Facts: []Fact{{Schema: 1, EventID: "fresh", SourceID: "fresh", Kind: "request", AtMS: fresh}}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Query(ctx, QueryFilter{Range: "5m", SourceID: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total["requests"] != float64(0) || len(result.SourceFreshness) != 1 || result.SourceFreshness[0].SourceID != "old" || result.SourceFreshness[0].LatestFactAt != old/1000 {
		t.Fatalf("empty window hid freshness: %+v", result)
	}
	result, err = e.Query(ctx, QueryFilter{Range: "5m", SourceIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SourceFreshness) != 0 {
		t.Fatal("source freshness leaked outside allowed scope")
	}
}

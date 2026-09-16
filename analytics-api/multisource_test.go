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
	req = httptest.NewRequest("POST", "/v1/sources", strings.NewReader(`{}`))
	req.AddCookie(cookie)
	req.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("CSRF accepted")
	}
}

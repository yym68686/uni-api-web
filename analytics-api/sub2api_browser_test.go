package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestSubLoginOffersBrowserAndSiteAgreementWithoutSavingPassword(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := "browser-" + randomID()[:8]
	token, _ := store.newSession(context.Background(), owner)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			writeJSON(w, 400, map[string]any{"code": 400, "reason": "TURNSTILE_VERIFICATION_FAILED", "message": "do not forward private-password"})
		case "/api/v1/settings/public":
			writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"login_agreement_enabled": true, "login_agreement_revision": "r1", "login_agreement_documents": []map[string]string{{"id": "terms", "title": "条款", "content_md": "条款正文"}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	previous := subHTTP
	u, _ := url.Parse(upstream.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}}
	defer func() { subHTTP = previous }()
	r := httptest.NewRequest("POST", "/v1/sub2api/accounts", strings.NewReader(`{"base":"https://site.example","email":"me@example.com","password":"private-password"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
	w := httptest.NewRecorder()
	(&Service{control: store}).Handler().ServeHTTP(w, r)
	var result struct {
		Required  bool `json:"requires_browser"`
		Agreement struct {
			Required bool   `json:"required"`
			Revision string `json:"revision"`
		} `json:"agreement"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != 200 || !result.Required || !result.Agreement.Required || result.Agreement.Revision != "r1" {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-password") {
		t.Fatal("credentials echoed")
	}
	var count int
	if err = store.db.QueryRow(`SELECT count(*) FROM console_sub_accounts WHERE owner=$1`, owner).Scan(&count); err != nil || count != 0 {
		t.Fatal("unverified account saved", count, err)
	}
}

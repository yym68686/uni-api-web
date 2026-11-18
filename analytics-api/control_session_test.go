package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestLoginSessionLifetime(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	user := "session-fixture-" + randomID()[:12]
	const password = "local-fixture-only"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("INSERT INTO console_users(username,password_hash) VALUES($1,$2)", user, string(hash)); err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec("DELETE FROM console_users WHERE username=$1", user)
	defer store.db.Exec("DELETE FROM console_sessions WHERE username=$1", user)
	service := &Service{control: store}
	for _, tc := range []struct {
		name, option string
		maxAge       int
		ttl          time.Duration
	}{
		{"browser", `,"browser_session":true`, 0, 8 * time.Hour},
		{"remembered", `,"browser_session":false`, 30 * 86400, 30 * 24 * time.Hour},
		{"legacy", "", 30 * 86400, 30 * 24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := `{"username":"` + user + `","password":"` + password + `"` + tc.option + `}`
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(input))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			service.authLogin(rec, req)
			cookies := rec.Result().Cookies()
			if rec.Code != http.StatusOK || len(cookies) != 1 {
				t.Fatalf("login status %d, cookies %d", rec.Code, len(cookies))
			}
			cookie := cookies[0]
			if cookie.MaxAge != tc.maxAge || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatal("unexpected session cookie lifetime or privacy controls")
			}
			var expires time.Time
			if err := store.db.QueryRow("SELECT expires_at FROM console_sessions WHERE token_hash=$1", tokenHash(cookie.Value)).Scan(&expires); err != nil {
				t.Fatal(err)
			}
			if diff := time.Until(expires) - tc.ttl; diff < -time.Minute || diff > time.Minute {
				t.Fatalf("server session lifetime differs by %v", diff)
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleSessionReadTraceScopeAndRedaction(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for _, tc := range []struct {
		method, path string
		correlated   bool
		observed     bool
	}{
		{http.MethodGet, "/v1/channel-management/query?filters=private-filter", true, true},
		{http.MethodGet, "/v1/sub2api/accounts/private-account/groups/19/details?view=summary", true, true},
		{http.MethodGet, "/v1/sub2api/channel-options?source_id=private-source", true, true},
		{http.MethodPost, "/v1/sub2api/accounts/private-account/import", true, false},
		{http.MethodGet, "/healthz", true, false},
		{http.MethodGet, "/v1/auth/me", true, false},
		{http.MethodGet, "/v1/channel-management/query", false, false},
	} {
		output.Reset()
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: "private-cookie"})
		r.Header.Set("Authorization", "Bearer private-credential")
		if tc.correlated {
			r = r.WithContext(context.WithValue(r.Context(), consoleRequestIDKey{}, "session-read-safe-123"))
		}
		if _, err := (&Service{}).controlUser(r); err != sql.ErrNoRows {
			t.Fatal("unavailable control store changed its authentication error", err)
		}
		if (output.Len() != 0) != tc.observed {
			t.Fatal("session observation scope changed", tc, output.String())
		}
		if tc.observed && !strings.Contains(output.String(), "console_session_read elapsed_ms=") {
			t.Fatal("session timing missing")
		}
		if strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "Bearer") {
			t.Fatal("session trace exposed private request values")
		}
	}
}

func TestConsoleSessionTracePreservesLiveMiddlewareAndHandlerChecks(t *testing.T) {
	s := managementBindingTestService(t)
	token, err := s.control.newSession(context.Background(), "private-owner")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	revoke := false
	handler := observeConsoleRequests(s.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revoke {
			if err := s.control.logout(r.Context(), token); err != nil {
				t.Fatal(err)
			}
		}
		owner, err := s.controlUser(r)
		if err != nil {
			http.Error(w, "live session rejected", http.StatusUnauthorized)
			return
		}
		if owner != "private-owner" {
			t.Error("session identity changed")
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	call := func() *httptest.ResponseRecorder {
		t.Helper()
		output.Reset()
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query?filters=private-filter", nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		r.Header.Set("X-Console-Request-ID", "session-read-safe-123")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if strings.Contains(output.String(), token) || strings.Contains(output.String(), "private-") {
			t.Fatal("session timing exposed token, username or filter")
		}
		return w
	}
	if w := call(); w.Code != http.StatusNoContent || strings.Count(output.String(), "console_session_read ") != 2 {
		t.Fatal("middleware/handler validation changed or was not measured", w.Code, output.String())
	}
	revoke = true
	if w := call(); w.Code != http.StatusUnauthorized || strings.Count(output.String(), "console_session_read ") != 2 {
		t.Fatal("revocation between validations was hidden", w.Code, output.String())
	}
	if w := call(); w.Code != http.StatusUnauthorized || strings.Count(output.String(), "console_session_read ") != 1 {
		t.Fatal("revoked session reached handler", w.Code, output.String())
	}
}

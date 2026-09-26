package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubSessionErrorSeparatesExpiredSessionFromBadPassword(t *testing.T) {
	for _, tc := range []struct {
		path, body  string
		wantSession bool
	}{
		{"/api/v1/auth/me", `{"code":"TOKEN_EXPIRED","message":"secret must not leak"}`, true},
		{"/api/v1/groups/available", `{"code":401}`, true},
		{"/api/v1/keys", `{"code":"UNAUTHORIZED"}`, true},
		{"/api/v1/auth/refresh", `{"code":401,"reason":"REFRESH_TOKEN_INVALID"}`, true},
		{"/api/v1/auth/login", `{"code":"INVALID_CREDENTIALS"}`, false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); w.Write([]byte(tc.body)) }))
			defer upstream.Close()
			err := subJSON(context.Background(), upstream.Client(), upstream.URL, "POST", tc.path, "fixture", nil, nil, "")
			if err == nil || strings.Contains(err.Error(), "secret") || (err.Error() == subSessionExpiredMessage) != tc.wantSession {
				t.Fatalf("wrong error classification: %v", err)
			}
		})
	}
}

func TestSubReloginUpdatesOnlyOwnedSessionAndDoesNotQueueChecks(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	ctx := context.Background()
	token, _ := s.control.newSession(ctx, owner)
	foreign, _ := s.control.newSession(ctx, owner+"-other")
	var logins int
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			logins++
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["email"] != "fixture@example.com" {
				t.Error("did not bind login to saved account")
			}
			if in["password"] != "correct" {
				w.WriteHeader(401)
				return
			}
			writeJSON(w, 200, map[string]any{"code": 0, "data": subAuth{Access: "fresh", Refresh: "fresh-refresh", ExpiresIn: 3600}})
		case "/api/v1/auth/me":
			writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"email": "fixture@example.com"}})
		default:
			t.Error("re-login requested sync, probe or site detection", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	call := func(cookie, password string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"base": "https://wrong.example", "email": "wrong@example.com", "password": password})
		r := httptest.NewRequest("POST", "/v1/sub2api/accounts/"+account+"/login", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := call(foreign, "correct"); w.Code != 404 || logins != 0 {
		t.Fatal("foreign login reached upstream", w.Code)
	}
	var before string
	s.control.db.QueryRow(`SELECT encrypted_auth FROM console_sub_accounts WHERE id=$1`, account).Scan(&before)
	if w := call(token, "bad"); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	var unchanged string
	s.control.db.QueryRow(`SELECT encrypted_auth FROM console_sub_accounts WHERE id=$1`, account).Scan(&unchanged)
	if unchanged != before {
		t.Fatal("failed login overwrote saved session")
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET state='error',message='old failure',synced_at=123 WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if w := call(token, "correct"); w.Code != 200 || !strings.Contains(w.Body.String(), `"queued":false`) || strings.Contains(w.Body.String(), "fresh") {
		t.Fatal(w.Code, w.Body.String())
	}
	var encoded, state, message, job string
	var synced int64
	if err := s.control.db.QueryRow(`SELECT encrypted_auth,state,message,job_kind,synced_at FROM console_sub_accounts WHERE id=$1`, account).Scan(&encoded, &state, &message, &job, &synced); err != nil {
		t.Fatal(err)
	}
	plain, err := s.control.decrypt(encoded)
	var saved subAuth
	if err != nil || json.Unmarshal([]byte(plain), &saved) != nil || saved.Access != "fresh" || saved.Refresh != "fresh-refresh" || state != "idle" || message != "" || job != "" || synced != 123 {
		t.Fatal("re-login did not only replace the session")
	}
	if _, err = s.control.db.Exec(`UPDATE console_sub_accounts SET state='running' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	calls := logins
	if w := call(token, "correct"); w.Code != 409 || logins != calls {
		t.Fatal("busy account login reached upstream", w.Code)
	}
}

func TestSubReloginChallengeCannotChangeAccountOrStartSync(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	ctx := context.Background()
	token, _ := s.control.newSession(ctx, owner)
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch r.URL.Path {
		case "/api/v1/auth/login":
			data = subAuth{Requires2FA: true, Temp: "challenge-token"}
		case "/api/v1/auth/login/2fa":
			data = subAuth{Access: "fresh", Refresh: "fresh-refresh"}
		case "/api/v1/auth/me":
			data = map[string]string{"email": "fixture@example.com"}
		default:
			t.Error("unexpected request", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": data})
	}))
	call := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	path := "/v1/sub2api/accounts/" + account + "/login"
	w := call(path, `{"password":"fixture"}`)
	var challenge struct {
		Challenge string `json:"challenge"`
	}
	json.Unmarshal(w.Body.Bytes(), &challenge)
	if w.Code != 200 || challenge.Challenge == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	body, _ := json.Marshal(map[string]string{"challenge": challenge.Challenge, "totp_code": "123456"})
	if w = call("/v1/sub2api/accounts", string(body)); w.Code != 400 {
		t.Fatal("re-login challenge started a new sync", w.Code)
	}
	if w = call(path, string(body)); w.Code != 200 || !strings.Contains(w.Body.String(), `"queued":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

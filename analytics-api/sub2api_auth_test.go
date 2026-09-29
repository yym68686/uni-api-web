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

func TestMissingPasswordDoesNotHideRefreshNetworkFailures(t *testing.T) {
	for _, err := range []error{nil, &subRemoteError{Status: 401}} {
		if got := subMissingLoginPasswordError(err); got.Error() != subMissingLoginPasswordMessage {
			t.Fatal(got)
		}
	}
	for _, status := range []int{403, 429, 503} {
		err := &subRemoteError{Status: status}
		if got := subMissingLoginPasswordError(err); got != err {
			t.Fatal("masked upstream failure", got)
		}
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
	if w := call(token, ""); w.Code != 400 || !strings.Contains(w.Body.String(), subMissingLoginPasswordMessage) {
		t.Fatal("missing legacy password was not explained", w.Code, w.Body.String())
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
	var encryptedPassword string
	if err = s.control.db.QueryRow(`SELECT encrypted_login_password FROM console_sub_accounts WHERE id=$1`, account).Scan(&encryptedPassword); err != nil {
		t.Fatal(err)
	}
	storedPassword, err := s.control.decrypt(encryptedPassword)
	if err != nil || storedPassword != "correct" || encryptedPassword == storedPassword {
		t.Fatal("login password was not encrypted and retained")
	}
	if w := call(foreign, ""); w.Code != 404 {
		t.Fatal("foreign owner could use saved credentials", w.Code)
	}
	if w := call(token, ""); w.Code != 200 {
		t.Fatal("blank password did not reuse saved credentials", w.Code, w.Body.String())
	}
	for _, view := range []string{"", "summary", "accounts"} {
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts", nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		r.Header.Set("X-Console-View", view)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"has_saved_password":true`) || strings.Contains(w.Body.String(), "correct") || strings.Contains(w.Body.String(), encryptedPassword) || strings.Contains(w.Body.String(), "fresh-refresh") {
			t.Fatal("account view must expose only password availability", view, w.Code)
		}
	}
	if _, err = s.control.db.Exec(`UPDATE console_sub_accounts SET state='running' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	calls := logins
	if w := call(token, "correct"); w.Code != 409 || logins != calls {
		t.Fatal("busy account login reached upstream", w.Code)
	}
}

func TestSubBrowserSessionAlsoRetainsPasswordForAutomaticLogin(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	token, _ := s.control.newSession(context.Background(), owner)
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/me" || r.Header.Get("Authorization") != "Bearer browser-access" {
			t.Error("unexpected request", r.URL.Path)
			http.Error(w, "unauthorized", 401)
			return
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]string{"email": "fixture@example.com"}})
	}))
	for _, body := range []string{
		`{"access_token":"browser-access","refresh_token":"browser-refresh","password":"browser-password"}`,
		`{"access_token":"browser-access","refresh_token":"browser-refresh"}`,
	} {
		r := httptest.NewRequest("POST", "/v1/sub2api/accounts/"+account+"/login", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 || strings.Contains(w.Body.String(), "browser-") {
			t.Fatal(w.Code, w.Body.String())
		}
		var encrypted string
		if err := s.control.db.QueryRow(`SELECT encrypted_login_password FROM console_sub_accounts WHERE id=$1`, account).Scan(&encrypted); err != nil {
			t.Fatal(err)
		}
		plain, err := s.control.decrypt(encrypted)
		if err != nil || plain != "browser-password" {
			t.Fatal("browser password was lost or cleared by session-only update")
		}
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

func TestSubPanelAutomaticallyReloginsAfterExpiredSession(t *testing.T) {
	s, account, _ := subUsageTestService(t)
	password, err := s.control.encrypt("correct")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.control.db.Exec(`UPDATE console_sub_accounts SET encrypted_login_password=$2,job_id='panel-job',state='running' WHERE id=$1`, account, password); err != nil {
		t.Fatal(err)
	}
	var groupReads, logins, refreshes int
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/groups/available":
			groupReads++
			if r.Header.Get("Authorization") == "Bearer expired-access" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Authorization") != "Bearer fresh-access" {
				t.Errorf("wrong group token: %q", r.Header.Get("Authorization"))
			}
			writeJSON(w, 200, map[string]any{"code": 0, "data": []subRemoteGroup{}})
		case "/api/v1/auth/refresh":
			refreshes++
			w.WriteHeader(http.StatusUnauthorized)
		case "/api/v1/auth/login":
			logins++
			var in map[string]string
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["email"] != "fixture@example.com" || in["password"] != "correct" {
				t.Errorf("wrong automatic login payload: %#v", in)
			}
			writeJSON(w, 200, map[string]any{"code": 0, "data": subAuth{Access: "fresh-access", Refresh: "fresh-refresh", ExpiresIn: 3600}})
		case "/api/v1/auth/me":
			if r.Header.Get("Authorization") != "Bearer fresh-access" {
				t.Errorf("wrong profile token: %q", r.Header.Get("Authorization"))
			}
			writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]string{"email": "fixture@example.com"}})
		default:
			t.Errorf("unexpected automatic login request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	call, groups, err := s.subPanel(context.Background(), account, "https://usage.example", "panel-job", "")
	if err != nil {
		t.Fatal(err)
	}
	if call == nil || len(groups) != 0 || groupReads != 2 || refreshes != 1 || logins != 1 {
		t.Fatalf("automatic login did not retry exactly once: groups=%d refreshes=%d logins=%d", groupReads, refreshes, logins)
	}
	var encrypted string
	if err = s.control.db.QueryRow(`SELECT encrypted_auth FROM console_sub_accounts WHERE id=$1`, account).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	plain, err := s.control.decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	var auth subAuth
	if err = json.Unmarshal([]byte(plain), &auth); err != nil || auth.Access != "fresh-access" || auth.Refresh != "fresh-refresh" {
		t.Fatalf("renewed session was not persisted: %v %#v", err, auth)
	}
}

func TestSubSavedPasswordRecoversBalanceAndUsageReads(t *testing.T) {
	for _, mode := range []string{"balance", "usage"} {
		t.Run(mode, func(t *testing.T) {
			s, account, owner := subUsageTestService(t)
			password, _ := s.control.encrypt("correct")
			if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET encrypted_login_password=$2 WHERE id=$1`, account, password); err != nil {
				t.Fatal(err)
			}
			var logins, refreshes int
			withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/refresh":
					refreshes++
					w.WriteHeader(401)
				case "/api/v1/auth/login":
					logins++
					var in map[string]string
					if json.NewDecoder(r.Body).Decode(&in) != nil || in["email"] != "fixture@example.com" || in["password"] != "correct" {
						t.Error("wrong saved credentials")
					}
					writeJSON(w, 200, map[string]any{"code": 0, "data": subAuth{Access: "recovered", Refresh: "rotated", ExpiresIn: 3600}})
				default:
					if r.Header.Get("Authorization") != "Bearer recovered" {
						w.WriteHeader(401)
						return
					}
					writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"email": "fixture@example.com", "balance": 12.5}})
				}
			}))
			for range 2 {
				if mode == "balance" {
					token, _ := s.control.newSession(context.Background(), owner)
					r := httptest.NewRequest("GET", "/v1/sub2api/accounts/"+account+"/balance", nil)
					r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
					w := httptest.NewRecorder()
					s.Handler().ServeHTTP(w, r)
					if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"ok"`) || !strings.Contains(w.Body.String(), `"amount":12.5`) {
						t.Fatal(w.Code, w.Body.String())
					}
				} else {
					var out map[string]any
					if err := s.subUsageGET(context.Background(), account, "https://usage.example", "/api/v1/usage", &out); err != nil {
						t.Fatal(err)
					}
				}
			}
			if logins != 1 || refreshes != 1 {
				t.Fatal("did not reuse recovered session", logins, refreshes)
			}
		})
	}
}

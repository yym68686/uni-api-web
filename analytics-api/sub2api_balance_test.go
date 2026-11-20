package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubAccountBalanceCachesScopesAndRefreshesSession(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := "balance-" + randomID()[:8]
	id := owner + "-account"
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE owner=$1`, owner)
	encoded, _ := json.Marshal(subAuth{Access: "expired", Refresh: "old-refresh"})
	enc, _ := store.encrypt(string(encoded))
	_, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'Site','https://site.example','me@example.com',$3)`, id, owner, enc)
	if err != nil {
		t.Fatal(err)
	}
	var calls, refreshes atomic.Int32
	var fail, missing, wrongUser atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			refreshes.Add(1)
			writeJSON(w, 200, map[string]any{"code": 0, "data": subAuth{Access: "fresh", ExpiresIn: 3600}})
		case "/api/v1/auth/me":
			calls.Add(1)
			if r.Header.Get("Authorization") != "Bearer fresh" {
				http.Error(w, "expired", 401)
				return
			}
			if fail.Load() {
				http.Error(w, "private-secret", 503)
				return
			}
			email := "me@example.com"
			if wrongUser.Load() {
				email = "foreign@example.com"
			}
			profile := map[string]any{"email": email}
			if !missing.Load() {
				profile["balance"] = -0.25
			}
			writeJSON(w, 200, map[string]any{"code": 0, "data": profile})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	old := subHTTP
	u, _ := url.Parse(upstream.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}}
	defer func() { subHTTP = old }()
	service := &Service{control: store}
	token, _ := store.newSession(context.Background(), owner)
	foreign, _ := store.newSession(context.Background(), owner+"-other")
	get := func(cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts/"+id+"/balance", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		return w
	}
	if w := get(""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := get(foreign); w.Code != 404 || calls.Load() != 0 {
		t.Fatal("foreign lookup", w.Code, calls.Load())
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := get(token)
			var result subAccountBalance
			if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != 200 || result.Amount == nil || *result.Amount != -0.25 || result.Status != "ok" {
				t.Error(w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "refresh") {
				t.Error("credential exposed")
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 2 || refreshes.Load() != 1 {
		t.Fatal("concurrent refresh duplicated", calls.Load(), refreshes.Load())
	}
	var rotated string
	if err := store.db.QueryRow(`SELECT encrypted_auth FROM console_sub_accounts WHERE id=$1`, id).Scan(&rotated); err != nil {
		t.Fatal(err)
	}
	plain, err := store.decrypt(rotated)
	var current subAuth
	if err != nil || json.Unmarshal([]byte(plain), &current) != nil || current.Access != "fresh" || current.Refresh != "old-refresh" {
		t.Fatal("access-only refresh lost the saved refresh token")
	}
	expire := func() {
		_, err := store.db.Exec(`UPDATE console_sub_accounts SET balance=jsonb_set(balance,'{checked_at}','1') WHERE id=$1`, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	expire()
	fail.Store(true)
	w := get(token)
	var previous subAccountBalance
	json.Unmarshal(w.Body.Bytes(), &previous)
	if previous.Amount == nil || *previous.Amount != -0.25 || previous.Status != "error" || previous.CheckedAt != 1 {
		t.Fatal("lost last balance", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-secret") {
		t.Fatal("error body leaked")
	}
	fail.Store(false)
	wrongUser.Store(true)
	if w = get(token); !strings.Contains(w.Body.String(), `"status":"error"`) {
		t.Fatal("foreign profile accepted", w.Body.String())
	}
	wrongUser.Store(false)
	missing.Store(true)
	w = get(token)
	var absent subAccountBalance
	json.Unmarshal(w.Body.Bytes(), &absent)
	if absent.Amount != nil || absent.Status != "missing" {
		t.Fatal("missing balance became zero", w.Body.String())
	}
}

func TestSubAccountBalanceBrowserRecoveryRefreshesWithoutDetection(t *testing.T) {
	s, id, owner := subUsageTestService(t)
	ctx := context.Background()
	password, err := s.control.encrypt("saved-private-password")
	if err != nil {
		t.Fatal(err)
	}
	oldAmount, oldTime := 48.91262206, time.Now().Unix()-600
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET balance=jsonb_build_object('status','ok','amount',$2::float8,'checked_at',$3::bigint), encrypted_login_password=$4 WHERE id=$1`, id, oldAmount, oldTime, password); err != nil {
		t.Fatal(err)
	}
	var refreshStatus, autoLogins atomic.Int32
	refreshStatus.Store(429)
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			w.WriteHeader(int(refreshStatus.Load()))
		case "/api/v1/auth/login":
			autoLogins.Add(1)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "turnstile_required", "message": "upstream-private-body"})
		case "/api/v1/auth/me":
			if r.Header.Get("Authorization") != "Bearer browser-verified" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"email": "fixture@example.com", "balance": 42.5}})
		default:
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	ownCookie, _ := s.control.newSession(ctx, owner)
	foreignCookie, _ := s.control.newSession(ctx, owner+"-other")
	call := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/sub2api/accounts/"+id+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	lookup := call("GET", "/login", "", ownCookie)
	if lookup.Code != 200 || !strings.Contains(lookup.Body.String(), `"has_saved_password":true`) || strings.Contains(lookup.Body.String(), "saved-private-password") || lookup.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("incorrect login metadata", lookup.Code, lookup.Body.String())
	}
	if call("GET", "/login", "", foreignCookie).Code != 404 || call("GET", "/login", "", "").Code != 401 {
		t.Fatal("login metadata not owner-scoped")
	}
	readBalance := func() subAccountBalance {
		t.Helper()
		w := call("GET", "/balance", "", ownCookie)
		var got subAccountBalance
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || strings.Contains(w.Body.String(), "upstream-private-body") {
			t.Fatal(w.Code, w.Body.String())
		}
		return got
	}
	if got := readBalance(); got.ErrorReason != "rate_limited" || got.Status != "error" || got.Amount == nil || *got.Amount != oldAmount || got.CheckedAt != oldTime || autoLogins.Load() != 0 {
		t.Fatalf("rate-limited refresh did not preserve balance or made another auth request: %+v", got)
	}
	refreshStatus.Store(401)
	if got := readBalance(); got.ErrorReason != "verification_required" || got.Status != "error" || got.Amount == nil || *got.Amount != oldAmount || got.CheckedAt != oldTime || autoLogins.Load() != 1 {
		t.Fatalf("browser challenge lost on login: %+v", got)
	}
	// Saving a browser session makes even a recently checked balance stale.
	recent := time.Now().Unix() - 15
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET balance=jsonb_set(balance,'{checked_at}',to_jsonb($2::bigint)) WHERE id=$1`, id, recent); err != nil {
		t.Fatal(err)
	}
	w := call("POST", "/login", `{"access_token":"browser-verified","refresh_token":"browser-refresh"}`, ownCookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"queued":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if got := readBalance(); got.Status != "ok" || got.ErrorReason != "" || got.RefreshRequired || got.Amount == nil || *got.Amount != 42.5 || got.CheckedAt <= recent {
		t.Fatalf("balance not refreshed after browser login: %+v", got)
	}
}

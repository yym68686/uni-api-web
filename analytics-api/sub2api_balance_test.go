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
			writeJSON(w, 200, map[string]any{"code": 0, "data": subAuth{Access: "fresh", Refresh: "next-refresh", ExpiresIn: 3600}})
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

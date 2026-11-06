package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAPIAccountBalanceAnnotatesLegacyCacheAndUsesWalletQuota(t *testing.T) {
	s, account, owner := newAPITestService(t)
	token, err := s.control.newSession(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	foreign, _ := s.control.newSession(context.Background(), owner+"-foreign")
	calls := 0
	quota := any(14125219) // USD 28.250438, irrespective of the site's display currency.
	fail := false
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/user/self" || r.Method != "GET" || r.Header.Get("New-Api-User") != "7" {
			t.Error("unexpected billing, credential or mutation path", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if fail {
			http.Error(w, "private-upstream-error", 503)
			return
		}
		newAPIOK(w, map[string]any{"id": 7, "username": "fixture", "quota": quota})
	}))
	get := func(session string) (*httptest.ResponseRecorder, subAccountBalance) {
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts/"+account+"/balance", nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		var balance subAccountBalance
		if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &balance) != nil {
			t.Fatal("invalid balance JSON")
		}
		for _, private := range []string{"expired-access", "refresh-fixture", "private-upstream-error"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("private data in balance response")
			}
		}
		return w, balance
	}
	if w, _ := get(foreign); w.Code != 404 || calls != 0 {
		t.Fatal("foreign account wallet exposed")
	}
	w, fresh := get(token)
	if w.Code != 200 || fresh.ProviderKind != "newapi" || fresh.Status != "ok" || fresh.Amount == nil || *fresh.Amount != 28.250438 || calls != 1 {
		t.Fatal("New API wallet was not normalized", w.Code, fresh, calls)
	}
	// Existing rows do not contain provider_kind. Annotate them from the owned
	// account metadata, without making a new upstream call or migrating data.
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET balance=balance-'provider_kind' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	_, cached := get(token)
	if cached.ProviderKind != "newapi" || cached.CheckedAt != fresh.CheckedAt || calls != 1 {
		t.Fatal("legacy cache lost adapter identity", cached, calls)
	}
	expire := func() {
		t.Helper()
		if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET balance=jsonb_set(balance,'{checked_at}','1') WHERE id=$1`, account); err != nil {
			t.Fatal(err)
		}
	}
	expire()
	fail = true
	_, failed := get(token)
	if failed.Status != "error" || failed.ProviderKind != "newapi" || failed.Amount == nil || *failed.Amount != *fresh.Amount || failed.CheckedAt != 1 {
		t.Fatal("failed refresh must retain the prior wallet and its error state", failed)
	}
	fail = false
	for _, test := range []struct {
		quota  any
		amount float64
	}{{245483790, 490.96758}, {-50000, -0.1}, {0, 0}} {
		expire()
		quota = test.quota
		_, got := get(token)
		if got.Status != "ok" || got.ProviderKind != "newapi" || got.Amount == nil || *got.Amount != test.amount {
			t.Fatal("wallet conversion", got)
		}
	}
	expire()
	quota = nil
	_, missing := get(token)
	if missing.Status != "missing" || missing.Amount != nil || missing.ProviderKind != "newapi" {
		t.Fatal("missing quota became a zero wallet", missing)
	}
}

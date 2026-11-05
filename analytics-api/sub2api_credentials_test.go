package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubAccountCredentialsAreOwnerScopedAndNeverCached(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	token, _ := s.control.newSession(context.Background(), owner)
	foreign, _ := s.control.newSession(context.Background(), owner+"-other")
	encrypted, err := s.control.encrypt("saved-password-密碼")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.control.db.Exec(`UPDATE console_sub_accounts SET encrypted_login_password=$2 WHERE id=$1`, account, encrypted); err != nil {
		t.Fatal(err)
	}
	read := func(cookie, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts/"+id+"/credentials", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		cookie, id string
		status     int
	}{{"", account, 401}, {foreign, account, 404}, {token, "missing", 404}} {
		w := read(tc.cookie, tc.id)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "saved-password") || strings.Contains(w.Body.String(), encrypted) {
			t.Fatal("credential authorization failed", w.Code)
		}
	}
	w := read(token, account)
	var credentials map[string]string
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &credentials) != nil || len(credentials) != 1 || credentials["password"] != "saved-password-密碼" {
		t.Fatal("owner credential read failed", w.Code)
	}
	for _, stored := range []string{"", "invalid-ciphertext-private"} {
		if _, err = s.control.db.Exec(`UPDATE console_sub_accounts SET encrypted_login_password=$2 WHERE id=$1`, account, stored); err != nil {
			t.Fatal(err)
		}
		w = read(token, account)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("credential response may be cached")
		}
		if stored == "" {
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &credentials) != nil || credentials["password"] != "" {
				t.Fatal("legacy account must return an empty password", w.Code)
			}
		} else if w.Code != 503 || strings.Contains(w.Body.String(), stored) {
			t.Fatal("decryption failure leaked ciphertext or reported success", w.Code)
		}
	}
}

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagementRevalidationRequiresCurrentOwnerAndCompleteEvidence(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	token, err := s.control.newSession(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	counter := 0
	call := func(tag string) *httptest.ResponseRecorder {
		t.Helper()
		counter++
		nonce := fmt.Sprintf("00000000-0000-4000-8000-%012d", counter)
		r := httptest.NewRequest("GET", "/v1/channel-management/query", nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		r.Header.Set("Accept", managementPageV3MediaType)
		r.Header.Set("Accept-Encoding", "br")
		r.Header.Set("X-Console-Current-Validation", "1")
		r.Header.Set("X-Console-Request-ID", nonce)
		if tag != "" {
			r.Header.Set("If-None-Match", tag)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code == 200 || w.Code == 304 {
			if w.Header().Get("X-Console-Validated-Request-ID") != nonce || w.Header().Get("X-Console-Read-At-Ms") == "" || w.Header().Get("Cache-Control") != "private, no-cache, must-revalidate" || !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Cookie") {
				t.Fatal("missing live validation", w.Header())
			}
		} else if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("error allowed cached data")
		}
		return w
	}
	first := call("")
	if first.Code != 200 || first.Header().Get("ETag") == "" {
		t.Fatal(first.Code)
	}
	same := call(first.Header().Get("ETag"))
	if same.Code != 304 || same.Body.Len() != 0 {
		t.Fatal("unchanged complete page redownloaded", same.Code, same.Body.Len())
	}
	if _, err = s.control.db.Exec(`UPDATE console_sub_accounts SET balance='{"amount":987.65,"checked_at":7,"status":"ok"}' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	balance := call(first.Header().Get("ETag"))
	if balance.Code != 200 || balance.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Fatal("fresh balance incorrectly confirmed unchanged")
	}
	if _, err = s.control.db.Exec(`UPDATE console_sub_models SET message='new evidence' WHERE model='extra-model'`); err != nil {
		t.Fatal(err)
	}
	changed := call(balance.Header().Get("ETag"))
	if changed.Code != 200 || changed.Header().Get("ETag") == balance.Header().Get("ETag") {
		t.Fatal("changed complete evidence confirmed unchanged")
	}
	if _, err = s.control.db.Exec(`UPDATE console_sub_usage SET result='{"input_tokens":"broken"}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	broken := call(changed.Header().Get("ETag"))
	if broken.Code != 503 || broken.Header().Get("ETag") != "" {
		t.Fatal("failed read confirmed old data", broken.Code)
	}
	if err = s.control.logout(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	denied := call(changed.Header().Get("ETag"))
	if denied.Code != 401 || denied.Header().Get("ETag") != "" {
		t.Fatal("revoked session accepted validator", denied.Code)
	}
}

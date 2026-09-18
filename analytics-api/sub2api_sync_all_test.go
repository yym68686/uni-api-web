package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSubSyncAllScopesAccountsAndKeepsExistingJobs(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := "sync-all-" + randomID()[:8]
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE owner=$1 OR owner=$2`, owner, owner+"-other")
	states := []string{"idle", "error", "stopped", "interrupted", "queued", "running"}
	for _, state := range states {
		_, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth,state,job_kind,job_id,message,lease_until) VALUES($1,$2,'Account','https://sync.example',$3,'',$3,'quality','existing-job','existing-message',now()+interval '1 hour')`, owner+"-"+state, owner, state)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'Other','https://sync.example','other','')`, owner+"-other", owner+"-other")
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{control: store}
	token, err := store.newSession(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	request := func(cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/sub2api/accounts/sync", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request(""); w.Code != 401 {
		t.Fatal("unauthenticated mutation", w.Code)
	}
	// Concurrent submissions must queue each idle account only once.
	responses := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() { responses <- request(token) }()
	}
	queued := 0
	for i := 0; i < 2; i++ {
		w := <-responses
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result struct {
			Queued int `json:"queued"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		queued += result.Queued
	}
	if queued != 4 {
		t.Fatalf("queued %d accounts, want 4", queued)
	}
	for _, original := range states {
		var state, kind, job, message string
		var leased bool
		err = store.db.QueryRow(`SELECT state,job_kind,job_id,message,lease_until IS NOT NULL FROM console_sub_accounts WHERE id=$1`, owner+"-"+original).Scan(&state, &kind, &job, &message, &leased)
		if err != nil {
			t.Fatal(err)
		}
		if original == "queued" || original == "running" {
			if state != original || kind != "quality" || job != "existing-job" || message != "existing-message" || !leased {
				t.Fatal("existing job changed", original, state, kind, job, message, leased)
			}
		} else if state != "queued" || kind != "sync" || job != "" || message != "" || leased {
			t.Fatal("sync not queued", original, state, kind, job, message, leased)
		}
	}
	var foreignState string
	if err = store.db.QueryRow(`SELECT state FROM console_sub_accounts WHERE id=$1`, owner+"-other").Scan(&foreignState); err != nil {
		t.Fatal(err)
	}
	if foreignState != "idle" {
		t.Fatal("foreign account changed", foreignState)
	}
}

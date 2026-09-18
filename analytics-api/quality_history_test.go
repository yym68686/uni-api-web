package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestQualityHistoryCountsAndPagesEveryAttempt(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("q", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "history-" + randomID()[:10]
	_, err = store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: id, Name: id, Base: "https://example.com"}, Key: "fixture-key"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
	q := qualityScope{Source: id, Provider: "same-provider"}
	for i := 0; i < 34; i++ {
		check := fmt.Sprintf("%s-%d", id, i)
		if err = store.beginQuality(context.Background(), check, q, -time.Second); err != nil {
			t.Fatal(err)
		}
		if i == 33 {
			continue
		} // Simulate a worker lost after recording its attempt.
		verdict := []string{"pass", "fail", "error"}[i%3]
		result := ChannelCheck{Verdict: verdict, CheckedAt: 123, Text: "fixture"}
		if err = store.finishQuality(check, verdict, verdict != "error", 123, result); err != nil {
			t.Fatal(err)
		}
		// A retry of the write must not turn a failure into a second success.
		if err = store.finishQuality(check, "pass", true, 123, result); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := store.qualitySummary(context.Background(), q)
	if err != nil || summary != (qualitySummary{Total: 34, Successful: 22, Passed: 11}) {
		t.Fatalf("bad denominator: %+v %v", summary, err)
	}
	// A similarly named channel in another scope must remain isolated.
	empty, err := store.qualitySummary(context.Background(), qualityScope{Source: id, Provider: "other"})
	if err != nil || empty.Total != 0 {
		t.Fatal(empty, err)
	}
	service, _ := NewService(stateTestEngine(t), Config{Upstream: "https://example.com"})
	service.control = store
	token, _ := store.newSession(context.Background(), "history-user")
	request := func(before, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/sources/"+id+"/channel-checks/history?provider=same-provider&before="+before, nil)
		if auth != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: auth})
		}
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		return w
	}
	if request("", "").Code != 401 {
		t.Fatal("history lacks authentication")
	}
	type page struct {
		Data    []struct{ ID, Verdict string }
		Summary qualitySummary
		Next    string
	}
	w := request("", token)
	var first page
	if json.Unmarshal(w.Body.Bytes(), &first) != nil || w.Code != 200 || len(first.Data) != 30 || first.Next == "" || first.Data[0].Verdict != "interrupted" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(first.Next, token)
	var second page
	if json.Unmarshal(w.Body.Bytes(), &second) != nil || len(second.Data) != 4 || second.Next != "" {
		t.Fatal(w.Code, w.Body.String())
	}
	seen := map[string]bool{}
	for _, entry := range append(first.Data, second.Data...) {
		if seen[entry.ID] {
			t.Fatal("duplicate page entry")
		}
		seen[entry.ID] = true
	}
}

func TestQualityHistoryMigrationSeedsLatestOnlyOnce(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// Isolate the migration in a transaction so this fixture cannot reset real
	// test history or make subsequent tests seed their projections again.
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM console_quality_migrations WHERE name='latest-to-history-v1'; INSERT INTO console_sources(id,name,base,encrypted_key) VALUES('migration-fixture','fixture','https://example.com',''); INSERT INTO console_channel_checks(source_id,provider,result) VALUES('migration-fixture','p','{"verdict":"fail","checked_at":42}'); INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('migration-account','fixture','fixture','https://example.com','me@example.com',''); INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('migration-account',1,'fixture','openai'); INSERT INTO console_sub_models(account_id,group_id,model,result) VALUES('migration-account',1,'gpt-6-astra','{"verdict":"pass","checked_at":42,"quality":{"status":"success"}}')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = tx.Exec(qualityHistorySchema); err != nil {
			t.Fatal(err)
		}
	}
	var total, passed, successful int
	err = tx.QueryRow(`SELECT count(*),count(*) FILTER(WHERE verdict='pass'),count(*) FILTER(WHERE successful) FROM console_quality_history WHERE source_id='migration-fixture' OR account_id='migration-account'`).Scan(&total, &passed, &successful)
	if err != nil || total != 2 || passed != 1 || successful != 2 {
		t.Fatal(total, passed, successful, err)
	}
}

func TestQualityHistorySubAccountOwnership(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("q", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "owner-" + randomID()[:10]
	_, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,'history-owner','fixture','https://example.com','me@example.com','')`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, id)
	_, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES($1,1,'fixture','openai')`, id)
	if err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(stateTestEngine(t), Config{Upstream: "https://example.com"})
	service.control = store
	for _, owner := range []string{"history-owner", "other-owner"} {
		token, _ := store.newSession(context.Background(), owner)
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts/"+id+"/groups/1/quality-history", nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		want := 200
		if owner != "history-owner" {
			want = 404
		}
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func bindingFixture(t *testing.T, upstream string) (*Service, subBindingAccount, controlSource) {
	t.Helper()
	s, id, owner := subUsageTestService(t)
	a := subBindingAccount{ID: id, Owner: owner, Base: upstream, Name: "Account", Email: "account@example.test", Synced: 1}
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET base=$2,name=$3,synced_at=1 WHERE id=$1`, id, upstream, a.Name); err != nil {
		t.Fatal(err)
	}
	source := controlSource{sourceView: sourceView{ID: "bindings-" + randomID()[:10], Name: "Source", Base: "https://gateway.example.test"}, Key: "gateway-secret"}
	if _, err := s.control.saveSource(context.Background(), source, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.control.db.Exec(`DELETE FROM console_configured_channels WHERE source_id=$1`, source.ID)
		s.control.db.Exec(`DELETE FROM console_sources WHERE id=$1`, source.ID)
	})
	return s, a, source
}
func assertConfiguredBinding(t *testing.T, s *Service, a subBindingAccount, source, provider, status string) subInstalledChannel {
	t.Helper()
	bindings, err := s.configuredBindings(context.Background(), a.Owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if binding.SourceID == source && binding.Provider == provider {
			if binding.BindingStatus != status {
				t.Fatalf("want %s: %+v", status, binding)
			}
			return binding
		}
	}
	t.Fatal("configured channel missing")
	return subInstalledChannel{}
}

func TestConfiguredBindingsPersistAcrossRefreshAndReplicasAndInvalidateOnChange(t *testing.T) {
	var requests atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/v1/keys" {
			t.Error("unexpected write or path", r.Method, r.URL.Path)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		id, key := int64(42), "existing-private-key"
		if page == 2 {
			id, key = 43, "second-private-key"
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"id": id, "key": key, "group_id": 7, "created_at": "2025-01-01T00:00:00Z"}}, "page": page, "pages": 2}})
	}))
	defer up.Close()
	previous := subHTTP
	subHTTP = up.Client()
	defer func() { subHTTP = previous }()
	s, a, src := bindingFixture(t, up.URL)
	providers := []configuredProvider{{Provider: "original", Base: up.URL + "/v1/responses", API: []any{"existing-private-key", "existing-private-key", "second-private-key"}}, {Provider: "shared", Base: up.URL + "/v1/messages?token=must-hide", API: "existing-private-key"}, {Provider: "different-site", Base: "https://other-site.example/v1", API: "existing-private-key"}}
	if err := s.saveConfiguredInventory(context.Background(), src, providers); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); s.refreshAccountKeyIndex(context.Background(), a) }()
	}
	wg.Wait()
	if requests.Load() != 2 {
		t.Fatal("duplicate key listing across replicas", requests.Load())
	}
	binding := assertConfiguredBinding(t, s, a, src.ID, "original", "matched")
	if len(binding.BoundKeys) != 2 || binding.AccountID != a.ID || binding.GroupID != 7 || binding.Manageable || binding.Kind != "configured" {
		t.Fatal(binding)
	}
	assertConfiguredBinding(t, s, a, src.ID, "different-site", "no_account")
	shared := assertConfiguredBinding(t, s, a, src.ID, "shared", "matched")
	if strings.Contains(shared.Base, "?") {
		t.Fatal("query credential exposed in site link")
	}
	raw, _ := json.Marshal(binding)
	if strings.Contains(string(raw), "private-key") || strings.Contains(string(raw), tokenHash("existing-private-key")) {
		t.Fatal("credential leaked")
	}
	// Repeated local reads and a fresh service reuse durable matches without I/O.
	fresh := &Service{control: s.control}
	for range 3 {
		fresh.refreshAccountKeyIndex(context.Background(), a)
		assertConfiguredBinding(t, fresh, a, src.ID, "original", "matched")
	}
	if requests.Load() != 2 {
		t.Fatal("unchanged channels rescanned", requests.Load())
	}
	foreign := a
	foreign.Owner = "different-console-user"
	assertConfiguredBinding(t, fresh, foreign, src.ID, "original", "no_account")
	providers[0].API = []any{"existing-private-key", "new-private-key"}
	if err := s.saveConfiguredInventory(context.Background(), src, providers); err != nil {
		t.Fatal(err)
	}
	assertConfiguredBinding(t, s, a, src.ID, "original", "partial")
	fresh.refreshAccountKeyIndex(context.Background(), a)
	if requests.Load() != 4 {
		t.Fatal("new key did not trigger one re-identification", requests.Load())
	}
	fresh.refreshAccountKeyIndex(context.Background(), a)
	if requests.Load() != 4 {
		t.Fatal("negative match rescanned on refresh")
	}
	a.Synced++
	fresh.refreshAccountKeyIndex(context.Background(), a)
	if requests.Load() != 6 {
		t.Fatal("explicit account sync did not refresh index")
	}
}

func TestConfiguredBindingRejectsAmbiguousOwnerAndIncompleteInventory(t *testing.T) {
	var fail atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() && r.URL.Query().Get("page") == "2" {
			http.Error(w, "secret must not leak", 503)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		key := fmt.Sprintf("secret-%d", page)
		writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"id": page, "key": key, "group_id": 7}}, "pages": 2, "page": page}})
	}))
	defer up.Close()
	old := subHTTP
	subHTTP = up.Client()
	defer func() { subHTTP = old }()
	s, a, src := bindingFixture(t, up.URL)
	if err := s.saveConfiguredInventory(context.Background(), src, []configuredProvider{{Provider: "initial", Base: up.URL + "/v1", API: "secret-1"}}); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	s.refreshAccountKeyIndex(context.Background(), a)
	var indexed int
	s.control.db.QueryRow(`SELECT count(*) FROM console_sub_key_index WHERE account_id=$1`, a.ID).Scan(&indexed)
	if indexed != 0 {
		t.Fatal("partial upstream listing published")
	}
	fail.Store(false)
	s.control.db.Exec(`UPDATE console_sub_key_scans SET next_attempt=now() WHERE account_id=$1`, a.ID)
	s.refreshAccountKeyIndex(context.Background(), a)
	assertConfiguredBinding(t, s, a, src.ID, "initial", "matched")
	second := a.ID + "-duplicate"
	_, err := s.control.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) SELECT $2,owner,name,base,'other@example.test',encrypted_auth FROM console_sub_accounts WHERE id=$1`, a.ID, second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.control.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, second)
	_, err = s.control.db.Exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES($1,$2,900,7)`, second, tokenHash("secret-1"))
	if err != nil {
		t.Fatal(err)
	}
	assertConfiguredBinding(t, s, a, src.ID, "initial", "ambiguous")
}

func TestConfiguredSpendUsesActualExistingKeyAndIncludesPreConsoleHistory(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	src := controlSource{sourceView: sourceView{ID: "old-key-" + randomID()[:8], Name: "Source", Base: "https://gateway.example"}, Key: "fixture"}
	s.control.saveSource(context.Background(), src, false)
	defer s.control.db.Exec(`DELETE FROM console_sources WHERE id=$1`, src.ID)
	defer s.control.db.Exec(`DELETE FROM console_configured_channels WHERE source_id=$1`, src.ID)
	var base string
	s.control.db.QueryRow(`SELECT base FROM console_sub_accounts WHERE id=$1`, account).Scan(&base)
	if err := s.saveConfiguredInventory(context.Background(), src, []configuredProvider{{Provider: "old", Base: base + "/v1/responses", API: "old-private-key"}}); err != nil {
		t.Fatal(err)
	}
	_, err := s.control.db.Exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id,key_created_at) VALUES($1,$2,42,7,'2025-01-01T00:00:00Z')`, account, tokenHash("old-private-key"))
	if err != nil {
		t.Fatal(err)
	}
	// The console account was saved today, but this key's receipts are older.
	s.control.db.Exec(`UPDATE console_sub_accounts SET created_at=now() WHERE id=$1`, account)
	token, _ := s.control.newSession(context.Background(), owner)
	foreign, _ := s.control.newSession(context.Background(), owner+"-foreign")
	to := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	request := func(cookie string, key int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", fmt.Sprintf("/v1/sub2api/accounts/%s/keys/%d/spend?range=all&to=%d", account, key, to.Unix()), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request(foreign, 42); w.Code != 404 {
		t.Fatal("foreign key accepted", w.Code)
	}
	if w := request(token, 999); w.Code != 404 {
		t.Fatal("unbound key accepted", w.Code)
	}
	if w := request(token, 42); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var from int64
	s.control.db.QueryRow(`SELECT wanted_from FROM console_sub_spend_cache WHERE account_id=$1 AND key_id=42`, account).Scan(&from)
	if from != time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatal("existing key history truncated", from)
	}
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/usage" || r.URL.Query().Get("api_key_id") != "42" {
			t.Error("wrong account key", r.URL.Path)
		}
		record := spendLog(123, to.Add(-24*time.Hour), "1.25")
		record.GroupID = usageInt(5) // old group before this key was reassigned
		writeJSON(w, 200, map[string]any{"code": 0, "data": subSpendPage{Items: []subSpendLog{record}, Page: 1, Pages: 1}})
	}))
	runSpendBatch(t, s, account)
	w := request(token, 42)
	var total subSpend
	json.Unmarshal(w.Body.Bytes(), &total)
	if w.Code != 200 || total.Status != "complete" || total.Amount == nil || *total.Amount != 1.25 {
		t.Fatal(w.Code, w.Body.String())
	}
}

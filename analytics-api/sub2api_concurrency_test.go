package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSubSyncAllStartsEveryAccountBeforeAnotherFinishes(t *testing.T) {
	t.Run("complete", func(t *testing.T) { testSubConcurrentAccounts(t, false) })
	t.Run("cancel", func(t *testing.T) { testSubConcurrentAccounts(t, true) })
}

func testSubConcurrentAccounts(t *testing.T, interrupt bool) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("c", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := "parallel-" + randomID()[:12]
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE owner=$1`, owner)
	// More accounts than database pool slots must still reach the upstream
	// together; neither slow sites nor auth locks may consume all DB sessions.
	const count = 12
	accounts := make([]string, count)
	for i := range accounts {
		id := fmt.Sprintf("%s-%02d", owner, i)
		accounts[i] = id
		auth, _ := store.encrypt(`{"access_token":"` + id + `"}`)
		if _, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'Fixture',$3,'fixture@example.com',$4)`, id, owner, fmt.Sprintf("https://site-%d.example", i), auth); err != nil {
			t.Fatal(err)
		}
	}
	panelStarted := make(chan string, count*2)
	probesStarted := make(chan string, count*8)
	releasePanel, releaseProbes := make(chan struct{}), make(chan struct{})
	var releasePanelOnce, releaseProbesOnce sync.Once
	unblockPanel := func() { releasePanelOnce.Do(func() { close(releasePanel) }) }
	unblockProbes := func() { releaseProbesOnce.Do(func() { close(releaseProbes) }) }
	var mu sync.Mutex
	active, peak, calls := map[string]int{}, map[string]int{}, map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		ok := func(value any) { writeJSON(w, 200, map[string]any{"code": 0, "data": value}) }
		switch r.URL.Path {
		case "/api/v1/groups/available":
			panelStarted <- id
			select {
			case <-releasePanel:
			case <-r.Context().Done():
				return
			}
			ok([]subRemoteGroup{{ID: 1, Name: "Fixture", Platform: "openai", Rate: 1}})
		case "/api/v1/groups/rates":
			ok(map[string]float64{"1": 1})
		case "/api/v1/channels/available":
			ok([]any{})
		case "/api/v1/keys":
			if r.Method != "GET" {
				t.Error("existing test key unexpectedly recreated")
				http.Error(w, "unexpected mutation", 400)
				return
			}
			ok(map[string]any{"items": []subRemoteKey{{ID: 1, Name: subKeyName(id, 1), Key: id, GroupID: 1, Status: "active"}}, "total": 1, "pages": 1})
		case "/v1/sub2api/billing":
			writeJSON(w, 200, map[string]any{"object": "sub2api.key_billing", "billing_scope": "token", "effective_rate_multiplier": 1})
		case "/v1/responses":
			mu.Lock()
			active[id]++
			peak[id] = max(peak[id], active[id])
			calls[id]++
			mu.Unlock()
			defer func() { mu.Lock(); active[id]--; mu.Unlock() }()
			probesStarted <- id
			select {
			case <-releaseProbes:
			case <-r.Context().Done():
				return
			}
			var payload struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
				return
			}
			subSSE(w, "21", payload.Model)
		default:
			t.Error("unexpected path", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	defer unblockPanel()
	defer unblockProbes()
	oldHTTP := subHTTP
	url, _ := url.Parse(upstream.URL)
	subHTTP = &http.Client{Transport: subTestTransport{url, http.DefaultTransport}}
	defer func() { subHTTP = oldHTTP }()
	service, err := NewService(stateTestEngine(t), Config{Upstream: "https://fixture.example", RequireInitialImport: true})
	if err != nil {
		t.Fatal(err)
	}
	service.control = store
	restoreStarted := blockCheckpointUntilCanceled(t, service)
	token, _ := store.newSession(context.Background(), owner)
	r := httptest.NewRequest("POST", "/v1/sub2api/accounts/sync", nil)
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
	w := httptest.NewRecorder()
	service.Handler().ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	// Competing dispatchers model a rolling deployment: each account still
	// belongs to exactly one worker even though every account can run at once.
	backgroundDone := service.startBackground(ctx)
	workers.Add(1)
	go func() { defer workers.Done(); <-backgroundDone }()
	workers.Add(1)
	go func() { defer workers.Done(); service.subWorkerLoop(ctx) }()
	defer func() { cancel(); unblockPanel(); unblockProbes(); workers.Wait() }()
	select {
	case <-restoreStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("checkpoint restore did not start")
	}
	// The real startup path must serve authenticated control endpoints while
	// blocking historical analytics, even with every detection worker busy.
	for path, code := range map[string]int{"/v1/auth/me": 200, "/v1/sources": 200, "/v1/analytics": 503, "/healthz": 200} {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		if w.Code != code {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	seen := map[string]int{}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for len(seen) < count {
		select {
		case id := <-panelStarted:
			seen[id]++
		case <-deadline.C:
			t.Fatalf("only %d/%d accounts synced concurrently", len(seen), count)
		}
	}
	for _, id := range accounts {
		if seen[id] != 1 {
			t.Fatal("duplicate or missing sync", id, seen[id])
		}
	}
	unblockPanel()
	started := map[string]int{}
	deadline.Reset(5 * time.Second)
	for total := 0; total < count*2; total++ {
		select {
		case id := <-probesStarted:
			started[id]++
		case <-deadline.C:
			t.Fatalf("probes did not start concurrently: %v", started)
		}
	}
	for _, id := range accounts {
		if started[id] != 2 {
			t.Fatal("per-account concurrency changed", id, started[id])
		}
	}
	if interrupt {
		cancel()
		done := make(chan struct{})
		go func() { workers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown did not cancel all active account workers")
		}
		var interrupted int
		if err = store.db.QueryRow(`SELECT count(*) FROM console_sub_accounts WHERE owner=$1 AND state='interrupted' AND job_id=''`, owner).Scan(&interrupted); err != nil || interrupted != count {
			t.Fatal("shutdown lost interrupted account states", interrupted, err)
		}
		if service.subWorkOne(context.Background()) {
			t.Fatal("interrupted paid checks were requeued automatically")
		}
		mu.Lock()
		defer mu.Unlock()
		for _, id := range accounts {
			if calls[id] != 2 {
				t.Fatal("cancelled account dispatched extra probes", id, calls[id])
			}
		}
		return
	}
	unblockProbes()
	finish := time.Now().Add(5 * time.Second)
	for {
		var idle int
		if err = store.db.QueryRow(`SELECT count(*) FROM console_sub_accounts WHERE owner=$1 AND state='idle'`, owner).Scan(&idle); err != nil {
			t.Fatal(err)
		}
		if idle == count {
			break
		}
		if time.Now().After(finish) {
			t.Fatalf("only %d accounts finished", idle)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	workers.Wait()
	mu.Lock()
	defer mu.Unlock()
	for _, id := range accounts {
		if peak[id] != 2 || calls[id] != len(subModels)+1 {
			t.Fatal("per-account probes changed or duplicated", id, peak[id], calls[id])
		}
	}
}

func TestSubAuthLeaseSerializesAccountsWithoutHoldingConnections(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("l", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "lease-" + randomID()[:12]
	if _, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$1,'Fixture','https://lease.example','fixture@example.com','')`, id); err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, id)
	service := &Service{control: store}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := service.subLockAuth(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if store.db.Stats().InUse != 0 {
		t.Fatal("auth lock pins a pool connection")
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer waitCancel()
	if _, err = service.subLockAuth(waitCtx, id); err == nil {
		t.Fatal("same account acquired twice")
	}
	// Simulate recovery after expiry and verify an old release cannot delete
	// the next owner's lock.
	if _, err = store.db.Exec(`UPDATE console_sub_auth_locks SET expires_at=now()-interval '1 second' WHERE account_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	nextUnlock, err := service.subLockAuth(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer nextUnlock()
	unlock()
	var count int
	if err = store.db.QueryRow(`SELECT count(*) FROM console_sub_auth_locks WHERE account_id=$1`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("stale unlock removed new owner", err)
	}
	nextUnlock()
	if err = store.db.QueryRow(`SELECT count(*) FROM console_sub_auth_locks WHERE account_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal("lease not released", err)
	}
}

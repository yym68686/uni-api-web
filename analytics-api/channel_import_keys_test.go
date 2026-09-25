package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestChannelImportKeyDirectorySkipsSlowConfiguration(t *testing.T) {
	s, account, src := bindingFixture(t, "https://unrelated.test")
	var calls, forbidden atomic.Int32
	var allowed atomic.Bool
	allowed.Store(true)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/api-keys" {
			forbidden.Add(1)
			http.Error(w, "slow configuration is unavailable", 503)
			return
		}
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer gateway-secret" {
			t.Error("incorrect source credentials")
		}
		writeJSON(w, 200, map[string]any{"can_inspect_all": allowed.Load(), "data": []any{
			map[string]any{"key_id": "empty-key", "prefix": "masked-empty", "position": 1},
			map[string]any{"key_id": "routed-key", "prefix": "masked-routed", "position": 2},
		}})
	}))
	defer gateway.Close()
	src.Base = gateway.URL
	if _, err := s.control.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	session, _ := s.control.newSession(context.Background(), account.Owner)
	request := func(source, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/sub2api/channel-options?keys_only=true&source_id="+source, nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := request(src.ID, session)
	var got struct {
		Keys []struct {
			ID string `json:"key_id"`
		} `json:"keys"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Keys) != 2 || got.Keys[0].ID != "empty-key" {
		t.Fatal(w.Code, w.Body.String())
	}
	if calls.Load() != 1 || forbidden.Load() != 0 {
		t.Fatal("key selector waited for configuration or omitted unrouted keys", calls.Load(), forbidden.Load())
	}
	restarted := &Service{control: s.control}
	start := time.Now()
	keys, status, err := restarted.channelImportKeys(context.Background(), src)
	if err != nil || status != 200 || len(keys.Keys) != 2 || calls.Load() != 1 || time.Since(start) > time.Second {
		t.Fatal("durable key directory was not immediately available after restart", status, err, calls.Load())
	}
	rotated := src
	rotated.Key = "rotated-key"
	if old, _ := restarted.savedImportKeys(context.Background(), rotated); old != nil {
		t.Fatal("old key directory inherited by changed credentials")
	}
	allowed.Store(false)
	s.keyDirectories.mu.Lock()
	s.keyDirectories.entries[src.ID].checked = time.Now().Add(-keyDirectoryMaxAge)
	s.keyDirectories.mu.Unlock()
	if w = request(src.ID, session); w.Code != 403 {
		t.Fatal("non-admin directory accepted", w.Code)
	}
	if w = request("missing", session); w.Code != 404 {
		t.Fatal("unknown source accepted", w.Code)
	}
	if w = request(src.ID, ""); w.Code != 401 {
		t.Fatal("unauthenticated directory accepted", w.Code)
	}
	if calls.Load() != 2 || forbidden.Load() != 0 {
		t.Fatal("invalid reads reached the gateway", calls.Load(), forbidden.Load())
	}
}

func TestImportKeyDirectoryServesCachedKeysWhileRefreshIsSlow(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		writeJSON(w, 200, map[string]any{"can_inspect_all": true, "data": []any{map[string]any{"key_id": "key", "prefix": "masked", "position": 1, "api": "must-not-cache"}}})
	}))
	defer gateway.Close()
	defer close(release)
	s := &Service{}
	src := controlSource{sourceView: sourceView{ID: "do", Base: gateway.URL}, Key: "fixture"}
	first, status, err := s.channelImportKeys(context.Background(), src)
	if err != nil || status != 200 || len(first.Keys) != 1 {
		t.Fatal(first, status, err)
	}
	if strings.Contains(mustJSON(first), "must-not-cache") {
		t.Fatal("cached secret")
	}
	s.keyDirectories.mu.Lock()
	s.keyDirectories.entries[src.ID].checked = time.Now().Add(-time.Minute)
	s.keyDirectories.mu.Unlock()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			result, status, err := s.channelImportKeys(ctx, src)
			if err != nil || status != 200 || len(result.Keys) != 1 || !result.Stale {
				t.Errorf("cached selector blocked: %d %v", status, err)
			}
		}()
	}
	wg.Wait()
	<-started
	if calls.Load() != 2 {
		t.Fatalf("duplicate refreshes: %d", calls.Load())
	}
}

func TestImportKeyDirectoryColdReadsDeduplicateAndCancellationDoesNotPoisonPeers(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		writeJSON(w, 200, map[string]any{"can_inspect_all": true, "data": []any{map[string]any{"key_id": "key", "prefix": "masked", "position": 1}}})
	}))
	defer gateway.Close()
	s := &Service{}
	src := controlSource{sourceView: sourceView{ID: "do", Base: gateway.URL}, Key: "fixture"}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, _, err := s.channelImportKeys(ctx, src); finished <- err }()
	<-started
	cancel()
	if err := <-finished; err == nil {
		t.Fatal("canceled waiter accepted")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, status, err := s.channelImportKeys(context.Background(), src)
			if err != nil || status != 200 || len(got.Keys) != 1 {
				t.Errorf("shared read failed %d %v", status, err)
			}
		}()
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("expected one shared read, got %d", calls.Load())
	}
}

func TestImportKeyDirectoryExpiresAndIsolatesCredentialsAndFailures(t *testing.T) {
	var mode atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 1:
			http.Error(w, "upstream failed", 503)
			return
		case 2:
			http.Error(w, "revoked", 403)
			return
		case 3:
			writeJSON(w, 200, map[string]any{"can_inspect_all": true, "data": []any{}})
			return
		}
		writeJSON(w, 200, map[string]any{"can_inspect_all": true, "data": []any{map[string]any{"key_id": r.Header.Get("Authorization"), "prefix": "masked", "position": 1}}})
	}))
	defer gateway.Close()
	s := &Service{}
	src := controlSource{sourceView: sourceView{ID: "do", Base: gateway.URL}, Key: "first"}
	get := func() importKeyDirectory {
		t.Helper()
		v, code, err := s.channelImportKeys(context.Background(), src)
		if err != nil || code != 200 {
			t.Fatal(code, err)
		}
		return v
	}
	get()
	src.Key = "second"
	if got := get(); got.Keys[0].ID != "Bearer second" {
		t.Fatal("old credential reused")
	}
	other := src
	other.ID = "another"
	if _, _, err := s.channelImportKeys(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	age := func(d time.Duration) {
		s.keyDirectories.mu.Lock()
		e := s.keyDirectories.entries[src.ID]
		e.checked = time.Now().Add(-d)
		e.attempt = time.Time{}
		s.keyDirectories.mu.Unlock()
	}
	wait := func() {
		t.Helper()
		s.keyDirectories.mu.Lock()
		pending := s.keyDirectories.entries[src.ID].inflight
		s.keyDirectories.mu.Unlock()
		if pending != nil {
			select {
			case <-pending:
			case <-time.After(time.Second):
				t.Fatal("refresh did not finish")
			}
		}
	}
	mode.Store(1)
	age(time.Minute)
	get()
	wait()
	if got := get(); !got.Stale || !got.RefreshFailed || len(got.Keys) != 1 {
		t.Fatal("transient error lost directory", got)
	}
	age(keyDirectoryMaxAge)
	if _, code, err := s.channelImportKeys(context.Background(), src); err == nil || code != 502 {
		t.Fatal("expired directory served", code, err)
	}
	mode.Store(0)
	age(keyDirectoryMaxAge)
	get()
	mode.Store(2)
	age(time.Minute)
	get()
	wait()
	if _, code, err := s.channelImportKeys(context.Background(), src); err == nil || code != 403 {
		t.Fatal("revoked key directory served", code, err)
	}
	s.keyDirectories.mu.Lock()
	if s.keyDirectories.entries[other.ID].keys == nil {
		t.Error("revocation affected other source")
	}
	s.keyDirectories.mu.Unlock()
	mode.Store(3)
	age(keyDirectoryMaxAge)
	if got := get(); got.Keys == nil || len(got.Keys) != 0 {
		t.Fatal("empty directory not authoritative", got)
	}
}

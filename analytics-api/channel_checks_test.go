package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestChannelCheckTargetsAndVerdicts(t *testing.T) {
	for _, tc := range []struct {
		name, answer, status, verdict string
		capability                    bool
		httpStatus                    int
	}{
		{"unknown", "未知", "completed", "pass", true, 200},
		{"old", "我的知识截止到2024-06。", "completed", "fail", true, 200},
		{"ambiguous", "未知，或2024-06", "completed", "inconclusive", true, 200},
		{"other", "2025-01", "completed", "inconclusive", true, 200},
		{"incomplete", "未知", "incomplete", "error", true, 200},
		{"error text", "未知", "completed", "error", true, 500},
		{"old gateway", "未知", "completed", "error", false, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Error("missing auth")
				}
				if r.Method == "GET" {
					writeJSON(w, 200, map[string]any{"capabilities": map[string]bool{"targeted_responses": tc.capability}})
					return
				}
				calls++
				if r.URL.Path != "/v1/responses" || r.Header.Get("X-Uni-API-Provider") != "selected-channel" {
					t.Error("incorrect destination")
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if len(body) != 2 || body["model"] != checkModel || body["input"].([]any)[0].(map[string]any)["content"] != checkPrompt {
					t.Error("incorrect fixed payload", body)
				}
				writeJSON(w, tc.httpStatus, map[string]any{"status": tc.status, "output": []any{map[string]any{"type": "reasoning", "content": []any{map[string]any{"type": "output_text", "text": "2024-06"}}}, map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": tc.answer}}}}})
			}))
			defer upstream.Close()
			result := runChannelCheck(context.Background(), controlSource{sourceView: sourceView{ID: "do", Base: upstream.URL}, Key: "test-secret"}, "selected-channel")
			if result.Verdict != tc.verdict {
				t.Fatalf("got %+v", result)
			}
			if !tc.capability && calls != 0 {
				t.Fatal("sent paid request to old gateway")
			}
			if result.CheckedAt == 0 || strings.Contains(result.Message, "test-secret") {
				t.Fatal("bad metadata")
			}
		})
	}
}
func TestChannelCheckPersistenceAndAuthorization(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			writeJSON(w, 200, map[string]any{"capabilities": map[string]bool{"targeted_responses": true}})
			return
		}
		hits.Add(1)
		writeJSON(w, 200, map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "未知"}}}}})
	}))
	defer upstream.Close()
	for _, id := range []string{"check-a", "check-b"} {
		_, err = store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: id, Name: id, Base: upstream.URL}, Key: "secret-for-tests"}, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	service, _ := NewService(stateTestEngine(t), Config{Upstream: upstream.URL})
	service.control = store
	token, err := store.newSession(context.Background(), "checker")
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(`{"provider":"same-name"}`))
		r.Header.Set("Content-Type", "application/json")
		if auth {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		}
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/v1/sources/check-a/channel-checks", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, id := range []string{"check-a", "check-b"} {
		if w := request("POST", "/v1/sources/"+id+"/channel-checks", true); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if hits.Load() != 2 {
		t.Fatal("unexpected retries", hits.Load())
	}
	for _, id := range []string{"check-a", "check-b"} {
		w := request("GET", "/v1/sources/"+id+"/channel-checks", true)
		var result struct {
			Data []ChannelCheck `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &result)
		if w.Code != 200 || len(result.Data) != 1 || result.Data[0].SourceID != id || result.Data[0].Verdict != "pass" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if _, err = store.db.Exec(`INSERT INTO console_channel_check_runs(source_id,provider,run_id,expires_at) VALUES('check-a','same-name','busy',now()+interval '70 seconds')`); err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec(`DELETE FROM console_channel_check_runs WHERE source_id='check-a'`)
	if w := request("POST", "/v1/sources/check-a/channel-checks", true); w.Code != 409 {
		t.Fatal("duplicate check not rejected", w.Code)
	}
}

func TestChannelChecksRunAllConcurrentlyWithoutHoldingDBConnections(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const count = 12
	started := make(chan struct{}, count)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			writeJSON(w, 200, map[string]any{"capabilities": map[string]bool{"targeted_responses": true}})
			return
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		writeJSON(w, 200, map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "未知"}}}}})
	}))
	defer upstream.Close()
	defer unblock()
	_, err = store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: "concurrent", Name: "concurrent", Base: upstream.URL}, Key: "test-secret"}, false)
	if err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(stateTestEngine(t), Config{Upstream: upstream.URL})
	service.control = store
	token, err := store.newSession(context.Background(), "checker")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan int, count)
	for i := 0; i < count; i++ {
		go func(i int) {
			r := httptest.NewRequest("POST", "/v1/sources/concurrent/channel-checks", strings.NewReader(fmt.Sprintf(`{"provider":"channel-%d"}`, i)))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
			w := httptest.NewRecorder()
			service.Handler().ServeHTTP(w, r)
			results <- w.Code
		}(i)
	}
	deadline := time.After(10 * time.Second)
	for i := 0; i < count; i++ {
		select {
		case <-started:
		case <-deadline:
			t.Fatalf("only %d of %d channels started together", i, count)
		}
	}
	if store.db.Stats().InUse != 0 {
		t.Error("database connections held during upstream checks")
	}
	unblock()
	for i := 0; i < count; i++ {
		if status := <-results; status != 200 {
			t.Errorf("status %d", status)
		}
	}
}

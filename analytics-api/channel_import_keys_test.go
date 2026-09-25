package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
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
	allowed.Store(false)
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

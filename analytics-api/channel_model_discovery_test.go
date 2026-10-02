package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type draftModelsRoundTrip func(*http.Request) (*http.Response, error)

func (f draftModelsRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDraftModelsURL(t *testing.T) {
	for _, path := range []string{"", "/v1", "/v1/", "/v1/models", "/v1/models/", "/v1/responses", "/v1/responses/compact", "/v1/chat/completions", "/v1/messages", "/v1/images/generations"} {
		for _, prefix := range []string{"", "/gateway"} {
			got, err := draftModelsURL("https://models.example" + prefix + path)
			if err != nil || got != "https://models.example"+prefix+"/v1/models" {
				t.Fatal(path, got, err)
			}
		}
	}
	for _, raw := range []string{"ftp://example.com", "https://key@example.com", "https://example.com?key=secret", "https://example.com#fragment", "http://127.0.0.1:8080", "http://[::1]", "https://169.254.169.254", "https://100.100.100.200"} {
		if _, err := draftModelsURL(raw); err == nil {
			t.Fatal("accepted unsafe URL", raw)
		}
	}
}

func TestDraftModelsDiscoveryOnlyReadsAndNormalizes(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: draftModelsRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.String() != "https://catalog.example/gateway/v1/models" || r.Header.Get("Authorization") != "Bearer fixture-key" || r.Body != nil {
			t.Fatal("wrong catalog request")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"model-b"},{"id":"model-a"},{"id":"model-b"},{"name":"vendor/model-c"},"custom",{},null]}`))}, nil
	})}
	got, endpoint, err := discoverDraftModels(context.Background(), client, draftModelDiscovery{Base: "https://catalog.example/gateway/v1/responses", API: "fixture-key", Engine: "codex"})
	if err != nil || strings.Join(got, ",") != "model-b,model-a,vendor/model-c,custom" || calls != 1 || endpoint != "https://catalog.example/gateway/v1/models" {
		t.Fatal(got, endpoint, err, calls)
	}
}
func TestDraftModelsErrorsDoNotExposeSecretsAndNeverFollowRedirects(t *testing.T) {
	for _, code := range []int{401, 403, 404, 429, 500, 302, 200} {
		calls := 0
		client := &http.Client{Transport: draftModelsRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			h := make(http.Header)
			h.Set("Location", "https://other.example/steal")
			return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader("fixture-secret echoed by server"))}, nil
		})}
		_, _, err := discoverDraftModels(context.Background(), client, draftModelDiscovery{Base: "https://catalog.example", API: "fixture-secret"})
		if err == nil || strings.Contains(err.Error(), "fixture-secret") || calls != 1 {
			t.Fatal(code, err, calls)
		}
	}
	for engine, header := range map[string]string{"claude": "x-api-key", "azure": "api-key", "gemini": "x-goog-api-key"} {
		client := &http.Client{Transport: draftModelsRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get(header) != "fixture" || r.Header.Get("Authorization") != "" {
				t.Fatal("wrong engine auth", engine)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}, nil
		})}
		models, _, err := discoverDraftModels(context.Background(), client, draftModelDiscovery{Base: "https://catalog.example", API: "fixture", Engine: engine})
		if err != nil || models == nil || len(models) != 0 {
			t.Fatal(models, err)
		}
	}
}
func TestDraftModelsBlocksPrivateNetworkAndRequiresAuthentication(t *testing.T) {
	touched := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { touched = true; w.Write([]byte(`{"data":[]}`)) }))
	defer upstream.Close()
	base := strings.Replace(upstream.URL, "127.0.0.1", "localhost", 1)
	if _, _, err := discoverDraftModels(context.Background(), newSubHTTP(), draftModelDiscovery{Base: base}); err == nil || touched {
		t.Fatal("private host was contacted")
	}
	s := &Service{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/sources/primary/channel-settings/discover-draft", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatal("expected unavailable control rejection", w.Code)
	}
	s.control = &controlStore{}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("unauthenticated model discovery accepted", w.Code)
	}
}

func TestDraftModelsHandlerDoesNotCreateConfiguration(t *testing.T) {
	s, a, src := bindingFixture(t, "https://site.example")
	token, err := s.control.newSession(context.Background(), a.Owner)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	previous := subHTTP
	subHTTP = &http.Client{Transport: draftModelsRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://catalog.example/gateway/v1/models" || r.Header.Get("Authorization") != "Bearer fixture-upstream-key" || r.Method != "GET" {
			t.Fatal("wrong discovery destination or credential")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"model-a"}]}`))}, nil
	})}
	t.Cleanup(func() { subHTTP = previous })
	call := func(id string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/sources/"+id+"/channel-settings/discover-draft", strings.NewReader(`{"base_url":"https://catalog.example/gateway/v1/responses","engine":"codex","api":"fixture-upstream-key"}`))
		r.Header.Set("Content-Type", "application/json")
		if authenticated {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := call(src.ID, false); w.Code != 401 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	if w := call("missing-source", true); w.Code != 404 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	w := call(src.ID, true)
	if w.Code != 200 || calls != 1 || strings.Contains(w.Body.String(), "fixture-upstream-key") || !strings.Contains(w.Body.String(), `"model-a"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, calls, w.Body.String())
	}
	var operations int
	if err = s.control.db.QueryRow(`SELECT count(*) FROM console_channel_settings_operations WHERE source_id=$1`, src.ID).Scan(&operations); err != nil || operations != 0 {
		t.Fatal("discovery wrote configuration", operations, err)
	}
}

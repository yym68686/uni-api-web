package main

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestObservationCorrelatesStagesWithoutSecrets(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	e := &Engine{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		op := e.observeRequestOperation(r.Context(), "test_query")
		op.stage("price_read")
		op.finish(context.DeadlineExceeded)
		http.Error(w, "unavailable", 503)
	})
	r := httptest.NewRequest("GET", "/v1/items/private-account?key=private-key", nil)
	r.Header.Set("X-Console-Request-ID", "safe-request-123")
	w := httptest.NewRecorder()
	observeConsoleRequests(mux).ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("X-Console-Request-ID") != "safe-request-123" {
		t.Fatal(w.Code, w.Header())
	}
	for _, expected := range []string{"request_id=safe-request-123", "price_read_ms=", "class=deadline_exceeded", `route="GET /v1/items/{id}"`} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatal("missing safe timing", expected, logs.String())
		}
	}
	if strings.Contains(logs.String(), "private-") {
		t.Fatal("request secrets reached log")
	}
	logs.Reset()
	r.Header.Set("X-Console-Request-ID", "private-key\nforged")
	w = httptest.NewRecorder()
	observeConsoleRequests(mux).ServeHTTP(w, r)
	if strings.Contains(logs.String(), "private-key") || strings.Contains(logs.String(), "forged") {
		t.Fatal("unvalidated identifier reached log")
	}
}

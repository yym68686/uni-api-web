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

func TestInteractiveReadsLogFastCompletionWithoutSensitivePaths(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	mux := http.NewServeMux()
	for _, route := range []string{"GET /v1/analytics", "GET /v1/channel-management/query", "GET /v1/sub2api/channels", "GET /v1/sub2api/channel-options", "GET /v1/sub2api/accounts/{id}/import-status", "GET /v1/sub2api/accounts/{id}/groups/{group}/access", "GET /v1/sub2api/accounts/{id}/groups/{group}/details", "GET /v1/sources/{id}/channel-checks", "GET /healthz"} {
		mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) })
	}
	for _, path := range []string{"/v1/analytics?key=private-key", "/v1/channel-management/query?filters=private-filter&selection=private-token", "/v1/sources/private-account/channel-checks?token=private-key", "/v1/sub2api/channels?view=bindings&source_id=private-source", "/v1/sub2api/channel-options?source_id=private-source&api_key_id=private-key&account_id=private-account", "/v1/sub2api/accounts/private-account/import-status", "/v1/sub2api/accounts/private-account/groups/49/access", "/v1/sub2api/accounts/private-account/groups/49/details?model=private-model&view=summary"} {
		logs.Reset()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer private-token")
		r.Header.Set("X-Console-Request-ID", "99a277df-a091-4ebb-85e5-cd6f28eb49c5")
		w := httptest.NewRecorder()
		observeConsoleRequests(mux).ServeHTTP(w, r)
		for _, expected := range []string{"console_request_started", "received_at_ms=", "finished_at_ms=", "status=200", "response_bytes=11", "request_id=99a277df-a091-4ebb-85e5-cd6f28eb49c5"} {
			if !strings.Contains(logs.String(), expected) {
				t.Fatal("fast successful read lost correlation", expected, logs.String())
			}
		}
		if strings.Contains(logs.String(), "private-") || strings.Contains(logs.String(), "Bearer") {
			t.Fatal("private request fields were logged")
		}
	}
	logs.Reset()
	observeConsoleRequests(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if logs.Len() != 0 {
		t.Fatal("routine health probes became verbose")
	}
}

func TestObservedReadKindsAreFixedAndUseHandlerViewPrecedence(t *testing.T) {
	for _, tc := range []struct{ path, view, kind string }{
		{"/v1/sub2api/channel-options?source_id=private-source&api_key_id=private-key", "", "import_options"},
		{"/v1/sub2api/channel-options?keys_only=true&source_id=private-source", "", "import_keys"},
		{"/v1/sub2api/channel-options?keys_only=private-value", "", "import_options"},
		{"/v1/sub2api/channels?view=bindings&source_id=private-source", "", "import_bindings"},
		{"/v1/sub2api/channels?view=private-view", "bindings", "import_bindings"},
		{"/v1/sub2api/channels?view=bindings", "private-view", "channel_ledger"},
		{"/v1/sub2api/channels", "", "channel_ledger"},
		{"/v1/sub2api/accounts/private-account/groups/49/access", "", "import_group_access"},
		{"/v1/sub2api/accounts/private-account/import-status", "", "import_account_status"},
		{"/v1/sub2api/accounts//groups/49/access", "", "none"},
		{"/v1/sub2api/accounts/private-account/groups//access", "", "none"},
		{"/v1/sub2api/accounts/private-account/groups/49/access/extra", "", "none"},
		{"/v1/sub2api/accounts/private-account/import-status/extra", "", "none"},
		{"/v1/sub2api/accounts/private-account/groups/49/details?view=summary&include_rates=0", "", "sub_group_summary"},
		{"/v1/sub2api/accounts/private-account/groups/49/details?view=summary", "private-view", "sub_group_detail"},
		{"/v1/sub2api/accounts/private-account/groups/49/details", "summary", "sub_group_summary"},
		{"/v1/sub2api/accounts/private-account/groups/49/details?model=private-model", "", "sub_group_detail"},
		{"/v1/sub2api/accounts//groups/49/details", "summary", "none"},
		{"/v1/sub2api/accounts/private-account/groups//details", "summary", "none"},
		{"/v1/sub2api/accounts/private-account/groups/49/details/extra", "summary", "none"},
		{"/v1/channel-management/query?filters=private-filter", "", "management_first_page"},
		{"/v1/channel-management/query?cursor=private-token", "", "management_page"},
		{"/v1/channel-management/query?cursor=private-token&refresh_cursor=1", "", "management_page_refresh"},
		{"/v1/channel-management/query?selection=private-token&cursor=private-cursor", "", "management_selection"},
		{"/v1/sources/private-source/proxy/private-path?view=bindings", "", "none"},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Header.Set("X-Console-View", tc.view)
		if got := consoleObservedReadKind(r); got != tc.kind {
			t.Fatalf("read classification differs: got=%q want=%q", got, tc.kind)
		}
		r.Method = http.MethodPost
		if got := consoleObservedReadKind(r); got != "none" {
			t.Fatal("write request classified as a read", got)
		}
	}
}

func TestSubGroupSummaryAlwaysRecordsSafeReadStages(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	for _, tc := range []struct {
		path, view string
		observed   bool
	}{
		{"/v1/sub2api/accounts/private-account/groups/49/details?model=private-model", "summary", true},
		{"/v1/sub2api/accounts/private-account/groups/49/details?view=summary", "", true},
		{"/v1/sub2api/accounts/private-account/groups/49/details", "", false},
		{"/v1/sub2api/accounts", "summary", false},
	} {
		logs.Reset()
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Header.Set("X-Console-View", tc.view)
		r = r.WithContext(context.WithValue(r.Context(), consoleRequestIDKey{}, "group-read-safe-123"))
		trace := newSubAccountsReadTrace(r)
		trace.accounts, trace.targets, trace.models, trace.modelBytes = 1, 1, 30, 1234
		trace.next("models")
		trace.next("usage")
		trace.next("serialize")
		trace.finish()
		if (logs.Len() > 0) != tc.observed {
			t.Fatal("fast scoped read lost stages or broadened unrelated account logging", logs.String())
		}
		if tc.observed {
			for _, value := range []string{"sub_accounts_read view=summary", "accounts=1 targets=1 models=30 model_result_bytes=1234", "models_ms=", "usage_ms=", "serialize_ms=", "request_id=group-read-safe-123"} {
				if !strings.Contains(logs.String(), value) {
					t.Fatal("missing scoped read metric", value, logs.String())
				}
			}
		}
		if strings.Contains(logs.String(), "private-") {
			t.Fatal("scoped read logged private values")
		}
	}
}

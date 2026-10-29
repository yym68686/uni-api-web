package main

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestManagementReadTriggerAdmitsOnlyOneFixedDiagnosticLabel(t *testing.T) {
	for _, value := range []string{"mount", "filter_change", "page_change", "accepted_write", "refetch", "", "unspecified", "private-value", " mount", "mount ", "Mount", "mount,refetch", "private\nforged", strings.Repeat("x", 512)} {
		for _, path := range []string{"/v1/channel-management/query?filters=private-filter", "/v1/channel-management/query?cursor=private-cursor&refresh_cursor=1", "/v1/channel-management/query?selection=private-selection", "/v1/sub2api/channels?view=bindings", "/healthz"} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				r := httptest.NewRequest(method, path, nil)
				r.Header.Set("X-Console-Read-Trigger", value)
				want := "none"
				if method == http.MethodGet && r.URL.Path == "/v1/channel-management/query" && r.URL.Query().Get("selection") == "" {
					want = "unspecified"
					if value == "mount" || value == "filter_change" || value == "page_change" || value == "accepted_write" || value == "refetch" {
						want = value
					}
				}
				if got := consoleObservedReadTrigger(r); got != want {
					t.Fatal("untrusted diagnostic changed route scope or escaped the fixed labels")
				}
			}
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query", nil)
	r.Header.Add("X-Console-Read-Trigger", "mount")
	r.Header.Add("X-Console-Read-Trigger", "refetch")
	if consoleObservedReadTrigger(r) != "unspecified" {
		t.Fatal("ambiguous repeated headers were accepted")
	}
}

func TestManagementReadTriggerNeverChangesResponseRequestOrExistingReadKind(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for _, code := range []int{200, 304, 400, 401, 409, 503} {
		for _, canceled := range []bool{false, true} {
			for _, value := range []string{"mount", "filter_change", "page_change", "accepted_write", "refetch", "private\nforged"} {
				output.Reset()
				path := "/v1/channel-management/query?cursor=private-cursor&refresh_cursor=1&filters=private-filter"
				r := httptest.NewRequest(http.MethodGet, path, nil)
				ctx, cancel := context.WithCancel(r.Context())
				if canceled {
					cancel()
				}
				r = r.WithContext(ctx)
				r.Header.Set("X-Console-Read-Trigger", value)
				r.Header.Set("X-Console-Request-ID", "safe-trigger-123")
				called := 0
				mux := http.NewServeMux()
				mux.HandleFunc("GET /v1/channel-management/query", func(w http.ResponseWriter, got *http.Request) {
					called++
					if got.URL.String() != path || got.Context().Err() != ctx.Err() || got.Header.Get("X-Console-Read-Trigger") != value || consoleObservedReadKind(got) != "management_page_refresh" {
						t.Fatal("diagnostic changed request, cancellation or server-derived kind")
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("ETag", `"current"`)
					w.WriteHeader(code)
					if code != 304 {
						_, _ = w.Write([]byte(`{"private_data":"unchanged"}`))
					}
				})
				plain := httptest.NewRecorder()
				mux.ServeHTTP(plain, r)
				observed := httptest.NewRecorder()
				observeConsoleRequests(mux).ServeHTTP(observed, r)
				cancel()
				if called != 2 || observed.Code != plain.Code || observed.Body.String() != plain.Body.String() || observed.Header().Get("ETag") != plain.Header().Get("ETag") || observed.Header().Get("Content-Type") != plain.Header().Get("Content-Type") || !reflect.DeepEqual(r.Header.Values("X-Console-Read-Trigger"), []string{value}) {
					t.Fatal("diagnostic changed response, validators or execution count")
				}
				trigger := consoleObservedReadTrigger(r)
				if strings.Count(output.String(), "read_trigger="+trigger+" read_trigger_scope=client_declared_not_authoritative") != 2 || !strings.Contains(output.String(), "read_kind=management_page_refresh") || !strings.Contains(output.String(), "request_id=safe-trigger-123") {
					t.Fatal("start/completion lost fixed trigger correlation")
				}
				for _, private := range []string{"private", "forged", "X-Console-Read-Trigger", "current\""} {
					if strings.Contains(output.String(), private) {
						t.Fatal("diagnostic exposed private header/query/response data")
					}
				}
			}
		}
	}
}

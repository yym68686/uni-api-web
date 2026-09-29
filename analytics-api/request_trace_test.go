package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestTraceRetainsStagesErrorsAndIsolatesSources(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute).UnixMilli()
	completed := true
	facts := []Fact{
		{EventID: "start", Kind: "trace", Stage: "request_received"},
		{EventID: "dispatch1", Kind: "dispatch", AttemptID: "r-r1", Provider: "first"},
		{EventID: "bill1", Kind: "billing", AttemptID: "r-r1", Provider: "first", Status: 403, TraceDetail: json.RawMessage(`{"error":{"error_code":"INSUFFICIENT_BALANCE","error_message":"Insufficient account balance sk-secret"},"headers_at_ms":123,"api_key":"private"}`)},
		{EventID: "attempt1", Kind: "attempt", AttemptID: "r-r1", Provider: "first", Outcome: "failed", FailureReason: "upstream_http_403", TerminalKind: "http_error", TransportTiming: json.RawMessage(`{"origin":"upstream_http_send","headers_received_ms":123.4,"connection":{"remote_addr":"private"}}`)},
		{EventID: "dispatch2", Kind: "dispatch", AttemptID: "r-r2", Provider: "second"},
		{EventID: "attempt2", Kind: "attempt", AttemptID: "r-r2", Provider: "second", Outcome: "success"},
		{EventID: "request", Kind: "request", Provider: "second", Outcome: "success", Status: 200, TerminalKind: "completed", ResponseCompleted: &completed, TraceID: "trace-alias"},
		{EventID: "end", Kind: "trace", Stage: "response_body_finished", Status: 200},
	}
	for i := range facts {
		facts[i].Schema = 1
		facts[i].AtMS = at + int64(i)
		facts[i].RequestID = "r"
		facts[i].SourceID = "primary"
		facts[i].InstanceID = "instance"
		facts[i].Model = "m"
	}
	other := facts[6]
	other.EventID = "other"
	other.SourceID = "other"
	facts = append(facts, other)
	if err := e.Import(ctx, "trace", "v1", facts); err != nil {
		t.Fatal(err)
	}
	result, err := e.RequestTrace(ctx, "r", []string{"primary"}, "")
	if err != nil || len(result.Data) != 1 || len(result.Data[0].Events) != 8 || result.Data[0].Ambiguous {
		t.Fatalf("%+v %v", result, err)
	}
	first := result.Data[0].Events[3]
	if first.TerminalKind != "http_error" || first.FailureReason != "upstream_http_403" || first.Transport["headers_received_ms"] != 123.4 {
		t.Fatalf("lost diagnostics: %+v", first)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "sk-secret") || !strings.Contains(string(raw), "INSUFFICIENT_BALANCE") {
		t.Fatal("unsafe or missing errors", string(raw))
	}
	result, err = e.RequestTrace(ctx, "trace-alias", []string{"primary"}, "")
	if err != nil || len(result.Data) != 1 || len(result.Data[0].Events) != 8 {
		t.Fatalf("trace alias not found: %+v %v", result, err)
	}
	result, err = e.RequestTrace(ctx, "r", []string{}, "")
	if err != nil || len(result.Data) != 0 {
		t.Fatal("source isolation", result, err)
	}
	result, err = e.RequestTrace(ctx, "r' OR 1=1 --", nil, "")
	if err != nil || len(result.Data) != 0 {
		t.Fatal("unsafe exact matching", result, err)
	}
	stats, err := e.KeyRequestStats(ctx, QueryFilter{Range: "all"})
	if err != nil || stats.Total.Requests != 0 {
		t.Fatal("diagnostic events affected metrics", stats, err)
	}
	aggregate, err := e.Query(ctx, QueryFilter{Range: "all", SourceID: "primary"})
	if err != nil || aggregate.Total["requests"] != float64(1) || aggregate.Total["attempts"] != int64(2) {
		t.Fatalf("trace events affected existing aggregation: %+v %v", aggregate.Total, err)
	}
	duplicate := facts[6]
	duplicate.EventID = "second-request"
	duplicate.AtMS += 100
	if err := e.Import(ctx, "duplicate", "v1", []Fact{duplicate}); err != nil {
		t.Fatal(err)
	}
	result, err = e.RequestTrace(ctx, "r", []string{"primary"}, "")
	if err != nil || !result.Data[0].Ambiguous {
		t.Fatal("reused caller IDs must be explicit", result, err)
	}
}

func TestRequestTraceHTTPRequiresAuthAndValidID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"can_inspect_all": true})
	}))
	defer upstream.Close()
	s, err := NewService(stateTestEngine(t), Config{Upstream: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, token string
		want        int
	}{
		{"/v1/request-trace?request_id=fixture", "", 401},
		{"/v1/request-trace", "admin-secret", 400},
		{"/v1/request-trace?request_id=fixture", "admin-secret", 200},
	} {
		r := httptest.NewRequest("GET", test.path, nil)
		if test.token != "" {
			r.Header.Set("Authorization", "Bearer "+test.token)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("%+v %d %s", test, w.Code, w.Body.String())
		}
	}
}

func TestRequestTraceRestoresLegacyCheckpointWithoutLosingFacts(t *testing.T) {
	ctx := context.Background()
	old := stateTestEngine(t)
	f := Fact{Schema: 1, EventID: "legacy", RequestID: "legacy-request", Kind: "request", AtMS: time.Now().Add(-time.Minute).UnixMilli(), Outcome: "failed", Status: 403}
	if err := old.Import(ctx, "legacy-object", "etag", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	if _, err := old.DB.Exec(`DROP INDEX history.facts_at; DROP INDEX history.facts_request; DROP INDEX history.facts_trace; ALTER TABLE facts DROP COLUMN trace_id; ALTER TABLE facts DROP COLUMN stage; ALTER TABLE facts DROP COLUMN trace_detail; ALTER TABLE facts DROP COLUMN transport_timing; ALTER TABLE facts DROP COLUMN terminal_kind; ALTER TABLE facts DROP COLUMN failure_reason; ALTER TABLE facts DROP COLUMN response_completed; CREATE INDEX facts_at ON facts(at_ms)`); err != nil {
		t.Fatal(err)
	}
	store := newCheckpointStore(&fakeStateObjects{}, Config{StateBucket: "state"})
	if err := store.save(ctx, old); err != nil {
		t.Fatal(err)
	}
	current := stateTestEngine(t)
	if ok, found, err := store.restorePhysical(ctx, current); err != nil || !ok || !found {
		t.Fatal(ok, err)
	}
	result, err := current.RequestTrace(ctx, "legacy-request", nil, "")
	if err != nil || len(result.Data) != 1 || len(result.Data[0].Events) != 1 || result.Data[0].Events[0].Status != 403 {
		t.Fatal(result, err)
	}
}

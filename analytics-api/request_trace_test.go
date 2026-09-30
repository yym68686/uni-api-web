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
	firstOutput := 150.0
	facts := []Fact{
		{EventID: "start", Kind: "trace", Stage: "request_received"},
		{EventID: "dispatch1", Kind: "dispatch", AttemptID: "r-r1", Provider: "first"},
		{EventID: "bill1", Kind: "billing", AttemptID: "r-r1", Provider: "first", Status: 403, TraceDetail: json.RawMessage(`{"error":{"error_code":"INSUFFICIENT_BALANCE","error_message":"Insufficient account balance sk-secret"},"headers_at_ms":123,"api_key":"private"}`)},
		{EventID: "attempt1", Kind: "attempt", AttemptID: "r-r1", Provider: "first", Outcome: "failed", FirstOutputMS: &firstOutput, FailureReason: "upstream_http_403", TerminalKind: "http_error", TransportTiming: json.RawMessage(`{"origin":"upstream_http_send","headers_received_ms":123.4,"raw_stream":{"totals":{"last_chunk_ms":456.7,"private":"private"}},"connection":{"remote_addr":"private"}}`)},
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
	if first.Transport["last_upstream_chunk_ms"] != 456.7 || first.FirstOutputMS == nil || *first.FirstOutputMS != firstOutput {
		t.Fatalf("lost stream milestones: %+v", first)
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

func TestTraceTransportLastChunkValidation(t *testing.T) {
	for _, raw := range []string{`{}`, `{"raw_stream":null}`, `{"raw_stream":{"totals":{"last_chunk_ms":null}}}`, `{"raw_stream":{"totals":{"last_chunk_ms":-1}}}`, `{"raw_stream":{"totals":{"last_chunk_ms":"12"}}}`} {
		if value := traceTransport(raw)["last_upstream_chunk_ms"]; value != nil {
			t.Fatalf("invented last chunk for %s: %v", raw, value)
		}
	}
	if got := traceTransport(`{"raw_stream":{"totals":{"last_chunk_ms":0}}}`)["last_upstream_chunk_ms"]; got != float64(0) {
		t.Fatal("lost zero timestamp", got)
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
		if w.Code == http.StatusOK && !strings.Contains(w.Header().Get("Server-Timing"), "lookup;dur=") {
			t.Fatal("missing trace lookup timing")
		}
	}
}

func TestRequestTraceScopesSharedIDsAndExpandsTraceAliases(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	// The same search string may be a request ID in one instance and a trace
	// alias in another. Expand the alias to its complete request, not only the
	// matching event; never cross an instance or allowed-source boundary.
	facts := []Fact{
		{Schema: 1, EventID: "a1", Kind: "dispatch", SourceID: "one", InstanceID: "a", RequestID: "shared", TraceID: "shared", AtMS: 1000},
		{Schema: 1, EventID: "a2", Kind: "request", SourceID: "one", InstanceID: "a", RequestID: "shared", AtMS: 2000},
		{Schema: 1, EventID: "b1", Kind: "dispatch", SourceID: "one", InstanceID: "b", RequestID: "actual", AtMS: 1000},
		{Schema: 1, EventID: "b2", Kind: "request", SourceID: "one", InstanceID: "b", RequestID: "actual", TraceID: "shared", AtMS: 2000},
		{Schema: 1, EventID: "private", Kind: "request", SourceID: "two", InstanceID: "b", RequestID: "shared", AtMS: 2000},
		{Schema: 1, EventID: "legacy", Kind: "request", SourceID: "one", RequestID: "shared", AtMS: 2000},
	}
	if err := e.Import(ctx, "shared-ids", "v1", facts); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("UPDATE facts SET instance_id=NULL WHERE event_id='legacy'"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, source string
		instance     []string
		runs, events int
	}{
		{"all owned instances", "", nil, 3, 5},
		{"matching request", "one", []string{"a"}, 1, 2},
		{"alias expansion", "one", []string{"b"}, 1, 2},
		{"legacy instance", "one", []string{""}, 1, 1},
		{"unknown instance", "one", []string{"missing"}, 0, 0},
		{"disallowed source", "two", nil, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := e.RequestTrace(ctx, "shared", []string{"one"}, test.source, test.instance...)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, run := range result.Data {
				count += len(run.Events)
				if run.Ambiguous || run.SourceID != "one" {
					t.Fatal("duplicate events or wrong scope", run)
				}
			}
			if len(result.Data) != test.runs || count != test.events {
				t.Fatalf("runs=%d events=%d; want %d/%d", len(result.Data), count, test.runs, test.events)
			}
		})
	}
}

func TestRequestTraceOrdersSameMillisecondByGatewaySequence(t *testing.T) {
	e := stateTestEngine(t)
	at := time.Now().Add(-time.Minute).UnixMilli()
	process := "0123456789abcdef0123456789abcdef"
	facts := []Fact{
		{Schema: 1, EventID: "dispatch-" + process + "-0000000000000002", Kind: "dispatch", RequestID: "sequence", AtMS: at},
		{Schema: 1, EventID: "trace-" + process + "-0000000000000001", Kind: "trace", Stage: "routing_attempt", RequestID: "sequence", AtMS: at},
	}
	if err := e.Import(context.Background(), "sequence", "etag", facts); err != nil {
		t.Fatal(err)
	}
	result, err := e.RequestTrace(context.Background(), "sequence", nil, "")
	if err != nil || result.Data[0].Events[0].Stage != "routing_attempt" {
		t.Fatal(result, err)
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

func TestTraceChannelNamesUseOwnedGroupsAndSourceScopedBindingsWithoutRemoteCalls(t *testing.T) {
	s, a, src := bindingFixture(t, "https://site.example")
	ctx := context.Background()
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,billing) VALUES($1,5,'纯Pro号池','openai','{"rate":0.15}')`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES($1,$2,42,5)`, a.ID, tokenHash("upstream-key")); err != nil {
		t.Fatal(err)
	}
	if err := s.saveConfiguredInventory(ctx, src, []configuredProvider{{Provider: "native", Base: a.Base + "/v1/responses", API: "upstream-key"}}); err != nil {
		t.Fatal(err)
	}
	key := "key-" + strings.Repeat("a", 64)
	imported := subProviderName(a.ID, 5, key)
	copied := configuredImportName("native", key)
	runs := []RequestTrace{
		{SourceID: src.ID, Events: []RequestTraceEvent{{Provider: imported, KeyID: key}, {Provider: "native", KeyID: key}, {Provider: copied, KeyID: key}, {Provider: "deleted", KeyID: key}}},
		{SourceID: "another-source", Events: []RequestTraceEvent{{Provider: "native", KeyID: key}}},
	}
	if err := s.enrichTraceChannels(ctx, a.Owner, runs); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{imported, "native", copied} {
		c := runs[0].Channels[provider]
		if c.SiteName != "Account" || c.GroupName != "纯Pro号池" || c.Name != "Account-0.15" || c.Rate == nil || *c.Rate != .15 || c.DashboardURL != "https://site.example/dashboard" {
			t.Fatal("missing readable metadata", provider, c)
		}
	}
	if _, ok := runs[0].Channels["deleted"]; ok {
		t.Fatal("invented deleted channel association")
	}
	if len(runs[1].Channels) != 0 {
		t.Fatal("channel labels crossed source scope")
	}
	raw, _ := json.Marshal(runs)
	for _, secret := range []string{key, "upstream-key", a.ID} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private binding data exposed")
		}
	}
	if err := s.enrichTraceChannels(ctx, a.Owner+"-foreign", runs); err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		for _, c := range run.Channels {
			if c.SiteName != "" || c.GroupName != "" || c.Rate != nil {
				t.Fatal("foreign account metadata exposed", c)
			}
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestLogsClassifiesRequestMetadataAndLegacyCompact(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	type scenario struct{ id, endpoint, metadata, kind, tier string }
	cases := []scenario{
		{"legacy", "/v1/responses", "", "", ""},
		{"compact", "/v1/responses/compact/", "", "compaction", ""},
		{"combined", "/v1/responses", `{"request_type":"compaction","requested_service_tier":"ultrafast"}`, "compaction", "ultrafast"},
		{"fast", "/v1/responses", `{"request_type":"standard","requested_service_tier":"priority"}`, "standard", "fast"},
		{"unknown", "/v1/responses", `{"request_type":"unrecognized","requested_service_tier":"unrecognized"}`, "", ""},
	}
	var facts []Fact
	for _, c := range cases {
		facts = append(facts, Fact{Schema: 1, EventID: c.id, Kind: "request", SourceID: "a", RequestID: c.id, Endpoint: c.endpoint, Status: 200, Outcome: "success", AtMS: now.Add(-time.Second).UnixMilli(), TraceDetail: json.RawMessage(c.metadata)})
	}
	if err := e.Import(ctx, "request-types", "v1", facts); err != nil {
		t.Fatal(err)
	}
	got, err := e.RequestLogs(ctx, RequestLogFilter{QueryFilter: QueryFilter{SourceIDs: []string{"a"}, Range: "24h", To: now.Unix()}})
	if err != nil || len(got.Data) != len(cases) {
		t.Fatal(got, err)
	}
	for _, c := range cases {
		for _, log := range got.Data {
			if log.EventID == c.id && (log.RequestType != c.kind || log.RequestedTier != c.tier) {
				t.Fatalf("%s: %+v", c.id, log)
			}
		}
	}
}

func TestRequestLogsOnlyFinalRequestsAndScopedFilters(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-time.Minute).UnixMilli()
	f := Fact{Schema: 1, EventID: "success", Kind: "request", RequestID: "same-request", TraceID: "trace-search", SourceID: "a", InstanceID: "i", KeyID: "key", Model: "model", Endpoint: "/v1/responses", Stream: true, Outcome: "success", Status: 200, AtMS: at}
	facts := []Fact{f}
	for i := 0; i < 10; i++ {
		v := f
		v.Kind = "attempt"
		v.Outcome = "failed"
		v.EventID = fmt.Sprint("attempt", i)
		facts = append(facts, v)
	}
	for _, kind := range []string{"dispatch", "billing", "trace"} {
		v := f
		v.Kind = kind
		v.EventID = kind
		facts = append(facts, v)
	}
	add := func(id string, mutate func(*Fact)) { v := f; v.EventID = id; mutate(&v); facts = append(facts, v) }
	add("b", func(v *Fact) { v.SourceID = "b" })
	add("second-final-same-id", func(v *Fact) { v.Outcome = "failed/other"; v.Status = 403; v.FailureReason = "insufficient_balance" })
	add("other-key", func(v *Fact) {
		v.KeyID = "other"
		v.Model = "historical-model"
		v.Stream = false
		v.Endpoint = "/v1/messages"
	})
	add("cancelled", func(v *Fact) { v.Outcome = "client_cancelled"; v.Status = 0 })
	add("unknown", func(v *Fact) { v.Outcome = ""; v.Status = 200 })
	add("old", func(v *Fact) { v.AtMS = now.Add(-48 * time.Hour).UnixMilli() })
	if err := e.Import(ctx, "logs", "v1", facts); err != nil {
		t.Fatal(err)
	}
	if err := e.Import(ctx, "replay", "v1", facts); err != nil {
		t.Fatal(err)
	}
	filter := RequestLogFilter{QueryFilter: QueryFilter{SourceIDs: []string{"a"}, Range: "24h", To: now.Unix()}}
	got, err := e.RequestLogs(ctx, filter)
	if err != nil || len(got.Data) != 5 || len(got.Models) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	filter.Model = "model"
	filter.KeyID = "key"
	filter.Endpoint = "/v1/responses"
	filter.Stream = "true"
	filter.Status = "failed"
	filter.Balance = "low"
	got, err = e.RequestLogs(ctx, filter)
	if err != nil || len(got.Data) != 1 || got.Data[0].EventID != "second-final-same-id" {
		t.Fatal(got, err)
	}
	filter.Status = ""
	filter.Balance = ""
	filter.Search = "trace-search"
	got, err = e.RequestLogs(ctx, filter)
	if err != nil || len(got.Data) != 4 {
		t.Fatal(got, err)
	}
	filter.Search = "' OR 1=1 --"
	got, err = e.RequestLogs(ctx, filter)
	if err != nil || len(got.Data) != 0 {
		t.Fatal(got, err)
	}
	filter.Search = ""
	filter.SourceIDs = []string{}
	got, err = e.RequestLogs(ctx, filter)
	if err != nil || len(got.Data) != 0 || len(got.Models) != 0 {
		t.Fatal("authorization leak", got, err)
	}
}

func TestRequestLogsCursorAndRefresh(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	var facts []Fact
	for i := 0; i < 113; i++ {
		latency := float64(i % 5)
		facts = append(facts, Fact{Schema: 1, Kind: "request", EventID: fmt.Sprintf("e%03d", i), RequestID: "reused-id", AtMS: now.Add(-time.Minute).UnixMilli() + int64(i%3), ResponseCreatedMS: &latency, FirstTextMS: &latency, DurationMS: &latency, DispatchMS: &latency, Outcome: "success"})
	}
	if err := e.Import(ctx, "pages", "v1", facts); err != nil {
		t.Fatal(err)
	}
	for _, sort := range []string{"latest", "oldest", "latency", "wait", "latency-desc", "wait-desc", "created", "created-desc", "text", "text-desc", "duration", "duration-desc", "success"} {
		filter := RequestLogFilter{QueryFilter: QueryFilter{Range: "24h", To: now.Unix()}, Sort: sort}
		seen := map[string]bool{}
		last := math.Inf(1)
		for page := 0; page < 3; page++ {
			got, err := e.RequestLogs(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range got.Data {
				var value *float64
				switch sort {
				case "duration-desc":
					value = v.DurationMS
				case "created-desc", "latency-desc":
					value = v.ResponseCreatedMS
				case "text-desc":
					value = v.FirstTextMS
				case "wait-desc":
					value = v.DispatchMS
				}
				if value != nil {
					if *value > last {
						t.Fatalf("%s not globally descending after cursor: %v > %v", sort, *value, last)
					}
					last = *value
				}
				if seen[v.EventID] {
					t.Fatal("duplicate page row", v.EventID)
				}
				seen[v.EventID] = true
			}
			filter.Cursor = got.NextCursor
			if page < 2 && filter.Cursor == "" {
				t.Fatal("truncated logs")
			}
			if page == 2 && (filter.Cursor != "" || len(seen) != 113) {
				t.Fatal("missing page rows", len(seen))
			}
		}
	}
	filter := RequestLogFilter{QueryFilter: QueryFilter{Range: "24h", To: now.Unix()}}
	first, err := e.RequestLogs(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	newer := Fact{Schema: 1, Kind: "request", EventID: "new", RequestID: "latest", AtMS: now.Add(time.Second).UnixMilli(), Outcome: "success"}
	if err := e.Import(ctx, "new", "v1", []Fact{newer}); err != nil {
		t.Fatal(err)
	}
	filter.To = now.Add(2 * time.Second).Unix()
	filter.Cursor = first.NextCursor
	next, err := e.RequestLogs(ctx, filter)
	if err != nil || next.To != first.To {
		t.Fatal("cursor window drifted", next, err)
	}
	filter.Cursor = ""
	refreshed, err := e.RequestLogs(ctx, filter)
	if err != nil || refreshed.Data[0].EventID != "new" {
		t.Fatal("refresh failed", refreshed, err)
	}
}

func TestRequestLogsExactWindowAndAttemptEvidence(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute).UnixMilli()
	started := at - 1000
	fast, slow := float64(100), float64(300)
	makeFact := func(id, kind, source, trace string, timestamp int64) Fact {
		return Fact{Schema: 1, EventID: id, Kind: kind, SourceID: source, InstanceID: "instance", TraceID: trace,
			RequestID: "reusable", AtMS: timestamp, StartedMS: started, Outcome: "success"}
	}
	first := makeFact("first", "request", "primary", "trace-first", at)
	first.FirstTextMS = &fast
	first.Stream, first.Status, first.Outcome, first.FailureReason = true, 502, "failed/transport_error", "transport_error"
	makeHeaders := func(id, source, trace string, ts int64) Fact {
		v := makeFact(id, "trace", source, trace, ts)
		v.Stage, v.Status = "response_headers", 200
		return v
	}
	second := makeFact("second", "request", "primary", "trace-second", at)
	second.ResponseCreatedMS = &slow
	second.DispatchMS = &fast
	third := first
	third.EventID, third.TraceID, third.FirstTextMS = "no-metric", "trace-third", nil
	otherInstance := makeHeaders("other-instance", "primary", third.TraceID, at-250)
	otherInstance.InstanceID = "another-instance"
	otherRequest := makeHeaders("other-request", "primary", third.TraceID, at-250)
	otherRequest.RequestID = "another-request"
	facts := []Fact{first, second, third,
		makeHeaders("response-headers", "primary", first.TraceID, at-500),
		makeHeaders("other-source", "isolated", third.TraceID, at-250),
		makeHeaders("before-request", "primary", third.TraceID, started-1),
		otherInstance, otherRequest,
		makeFact("before", "request", "primary", "before", at-2500),
		makeFact("attempt-1", "attempt", "primary", "trace-second", at-800),
		makeFact("attempt-2", "attempt", "primary", "trace-second", at-300),
		makeFact("wrong-source", "attempt", "isolated", "trace-second", at-300),
		makeFact("expired", "attempt", "primary", "trace-second", at-1500),
	}
	if err := e.Import(ctx, "request-log-evidence", "v1", facts); err != nil {
		t.Fatal(err)
	}
	filter := RequestLogFilter{QueryFilter: QueryFilter{Range: "15m"}, FromMS: at - 1500, ToMS: at + 500, Sort: "latency-desc"}
	got, err := e.RequestLogs(ctx, filter)
	if err != nil || got.From != filter.FromMS || got.To != filter.ToMS || len(got.Data) != 3 {
		t.Fatalf("window: %+v %v", got, err)
	}
	if got.Data[0].EventID != "second" || got.Data[1].EventID != "first" || got.Data[2].EventID != "no-metric" ||
		got.Data[0].ObservedAttempts == nil || *got.Data[0].ObservedAttempts != 2 || got.Data[1].ObservedAttempts != nil {
		t.Fatalf("ordering or observed attempts: %+v", got.Data)
	}
	if got.Data[1].ResponseHeadersStatus == nil || *got.Data[1].ResponseHeadersStatus != 200 ||
		got.Data[1].Status != 502 || got.Data[1].Result != "failed" || got.Data[2].ResponseHeadersStatus != nil {
		t.Fatalf("response header status: %+v", got.Data)
	}
}

func TestRequestLogsHTTPAuthAndValidation(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"can_inspect_all": r.Header.Get("Authorization") == "Bearer admin-secret"})
	}))
	defer up.Close()
	s, err := NewService(stateTestEngine(t), Config{Upstream: up.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token, path string
		status      int
	}{
		{"", "/v1/request-logs", 401}, {"user-secret", "/v1/request-logs", 403}, {"admin-secret", "/v1/request-logs", 200},
		{"admin-secret", "/v1/request-logs?range=bad", 400}, {"admin-secret", "/v1/request-logs?cursor=bad", 400}, {"admin-secret", "/v1/request-logs?status=bad", 400},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
}

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
		facts = append(facts, Fact{Schema: 1, Kind: "request", EventID: fmt.Sprintf("e%03d", i), RequestID: "reused-id", AtMS: now.Add(-time.Minute).UnixMilli() + int64(i%3), ResponseCreatedMS: &latency, Outcome: "success"})
	}
	if err := e.Import(ctx, "pages", "v1", facts); err != nil {
		t.Fatal(err)
	}
	for _, sort := range []string{"latest", "oldest", "latency", "wait", "success"} {
		filter := RequestLogFilter{QueryFilter: QueryFilter{Range: "24h", To: now.Unix()}, Sort: sort}
		seen := map[string]bool{}
		for page := 0; page < 3; page++ {
			got, err := e.RequestLogs(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range got.Data {
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

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestKeyRequestStatsFinalOutcomesAndFilters(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	var facts []Fact
	add := func(id, source, key, model, kind, outcome string, at time.Time) {
		facts = append(facts, Fact{Schema: 1, EventID: id, InstanceID: "instance", RequestID: "reused-caller-id", SourceID: source, KeyID: key, Model: model, Kind: kind, Outcome: outcome, AtMS: at.UnixMilli(), Endpoint: "/v1/responses", Stream: true})
	}
	recent := now.Add(-2 * time.Minute)
	// Ten failed attempts then success, first-attempt success, and ten attempts
	// then final failure: 2/3 requests succeed, regardless of retry counts.
	for i := 0; i < 20; i++ {
		add(fmt.Sprint("retry-", i), "fugue", "same-key", "model-a", "attempt", "failed", recent)
	}
	for i, outcome := range []string{"success", "success", "failed"} {
		add(fmt.Sprint("final-", i), "fugue", "same-key", "model-a", "request", outcome, recent)
	}
	for _, kind := range []string{"attempt", "dispatch", "billing"} {
		add(kind, "fugue", "same-key", "model-a", kind, "success", recent)
	}
	add("hedge-loser", "fugue", "same-key", "model-a", "attempt", "hedge_cancelled", recent)
	add("ongoing", "fugue", "inflight-key", "model-a", "attempt", "success", recent)
	add("other-model", "fugue", "same-key", "model-b", "request", "completed", recent)
	add("other-source", "digitalocean", "same-key", "model-a", "request", "failed", recent)
	add("other-key", "fugue", "second-key", "model-a", "request", "success", recent)
	add("old", "fugue", "same-key", "model-a", "request", "success", now.Add(-48*time.Hour))
	add("no-key", "fugue", "", "model-a", "request", "failed", recent)
	add("nonstream", "fugue", "same-key", "model-a", "request", "failed", recent)
	facts[len(facts)-1].Stream = false
	facts[len(facts)-1].Endpoint = "/v1/messages"
	if err := e.Import(ctx, "requests", "v1", facts); err != nil {
		t.Fatal(err)
	}
	// Re-exported events must not double count; distinct completed requests
	// sharing a caller request ID above must still each count.
	if err := e.Import(ctx, "replay", "v1", facts); err != nil {
		t.Fatal(err)
	}
	f := QueryFilter{SourceIDs: []string{"fugue"}, Range: "24h", Model: "model-a", Endpoint: "/v1/responses", Stream: "true", To: now.Unix(), KeyID: "same-key", Provider: "ignored"}
	result, err := e.KeyRequestStats(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 2 || result.Total.Requests != 4 || result.Total.Success != 3 || result.Total.Failed != 1 {
		t.Fatalf("wrong request population: %+v", result)
	}
	first := result.Data[0]
	if first.KeyID != "same-key" || first.Requests != 3 || first.Success != 2 || first.SuccessRate == nil || *first.SuccessRate != 2.0/3 {
		t.Fatalf("retries affected success rate: %+v", first)
	}
	f.Model = ""
	result, err = e.KeyRequestStats(ctx, f)
	if err != nil || result.Total.Requests != 5 || result.Total.Success != 4 {
		t.Fatalf("all-model query: %+v %v", result, err)
	}
	f.Endpoint, f.Stream = "all", "all"
	result, err = e.KeyRequestStats(ctx, f)
	if err != nil || result.Total.Requests != 6 || result.Total.Failed != 2 {
		t.Fatalf("all endpoints: %+v %v", result, err)
	}
	f.SourceIDs = []string{"fugue", "digitalocean"}
	result, err = e.KeyRequestStats(ctx, f)
	if err != nil || len(result.Data) != 3 || result.Total.Requests != 7 {
		t.Fatalf("source identities merged: %+v %v", result, err)
	}
	f.SourceID = "digitalocean"
	result, err = e.KeyRequestStats(ctx, f)
	if err != nil || result.Total.Requests != 1 || result.Total.SuccessRate == nil || *result.Total.SuccessRate != 0 {
		t.Fatalf("failed-only requests: %+v %v", result, err)
	}
	f.SourceIDs = []string{}
	result, err = e.KeyRequestStats(ctx, f)
	if err != nil || len(result.Data) != 0 || result.Total.SuccessRate != nil {
		t.Fatalf("empty authorization must not expose stats: %+v %v", result, err)
	}
}

func TestKeyRequestStatsDayAndMinuteBucketsDoNotOverlap(t *testing.T) {
	e := stateTestEngine(t)
	now := time.Now().UTC().Truncate(time.Minute)
	var facts []Fact
	for i := 1; i <= 80; i++ {
		facts = append(facts, Fact{Schema: 1, EventID: fmt.Sprint(i), Kind: "request", KeyID: "key", Model: "m", Outcome: "success", AtMS: now.Add(-time.Duration(i) * time.Hour).UnixMilli()})
	}
	if err := e.Import(context.Background(), "days", "v1", facts); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		rangeName string
		n         int64
	}{{"1h", 1}, {"24h", 24}, {"7d", 80}, {"all", 80}} {
		result, err := e.KeyRequestStats(context.Background(), QueryFilter{Range: test.rangeName, To: now.Unix()})
		if err != nil || result.Total.Requests != test.n {
			t.Fatalf("%s: %+v %v", test.rangeName, result, err)
		}
	}
}

func TestKeyRequestStatsHTTPAuthAndReadiness(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"can_inspect_all": r.Header.Get("Authorization") == "Bearer admin-secret"})
	}))
	defer upstream.Close()
	s, err := NewService(stateTestEngine(t), Config{Upstream: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		token, path string
		loading     bool
		status      int
	}{
		{"", "/v1/key-request-stats", false, 401},
		{"user-secret", "/v1/key-request-stats", false, 403},
		{"admin-secret", "/v1/key-request-stats", false, 200},
		{"admin-secret", "/v1/key-request-stats?range=invalid", false, 400},
		{"admin-secret", "/v1/key-request-stats", true, 503},
	} {
		s.historyLoading.Store(test.loading)
		r := httptest.NewRequest(http.MethodGet, test.path, nil)
		if test.token != "" {
			r.Header.Set("Authorization", "Bearer "+test.token)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("%+v: %d %s", test, w.Code, w.Body.String())
		}
	}
}

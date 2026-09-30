package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestChannelModelStatsScopeAndTimings(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	ptr := func(v float64) *float64 { return &v }
	base := Fact{Schema: 1, Kind: "attempt", AtMS: now.Add(-time.Minute).UnixMilli(), SourceID: "a", Provider: "provider", Model: "m", KeyID: "key", Endpoint: "/v1/responses", Stream: true, Outcome: "success", DurationMS: ptr(200), FirstTextMS: ptr(20), ResponseCreatedMS: ptr(10)}
	facts := []Fact{}
	add := func(id string, change func(*Fact)) { v := base; v.EventID = id; change(&v); facts = append(facts, v) }
	add("success", func(*Fact) {})
	add("failure", func(v *Fact) {
		v.Outcome = "failed/upstream_http_500"
		v.DurationMS = ptr(1000)
		v.FirstTextMS = nil
		v.ResponseCreatedMS = nil
	})
	add("other-endpoint", func(v *Fact) {
		v.Endpoint = "/v1/messages"
		v.Stream = false
		v.UpstreamModel = "alias"
		v.DurationMS = ptr(400)
		v.FirstTextMS = ptr(60)
	})
	add("null-timing", func(v *Fact) { v.DurationMS = nil; v.FirstTextMS = nil; v.ResponseCreatedMS = nil })
	add("cancelled", func(v *Fact) { v.Outcome = "hedge_cancelled"; v.DurationMS = ptr(99999); v.FirstTextMS = ptr(88888) })
	add("skipped", func(v *Fact) { v.Outcome = "skipped" })
	add("final-request", func(v *Fact) { v.Kind = "request"; v.DurationMS = ptr(33333) })
	add("other-source", func(v *Fact) { v.SourceID = "b"; v.DurationMS = ptr(66666) })
	add("other-key", func(v *Fact) { v.KeyID = "else"; v.DurationMS = ptr(77777) })
	add("other-model", func(v *Fact) { v.Model = "other" })
	add("other-provider", func(v *Fact) { v.Provider = "other" })
	add("old", func(v *Fact) { v.AtMS = now.Add(-48 * time.Hour).UnixMilli() })
	add("future", func(v *Fact) { v.AtMS = now.Add(time.Minute).UnixMilli() })
	if err := e.Import(ctx, "model-stats", "1", facts); err != nil {
		t.Fatal(err)
	}
	if err := e.Import(ctx, "replay-stats", "1", facts); err != nil {
		t.Fatal(err)
	}
	f := QueryFilter{SourceIDs: []string{"a"}, SourceID: "a", KeyID: "key", Model: "m", Range: "24h", To: now.Unix()}
	got, err := e.ChannelModelStats(ctx, f)
	if err != nil || len(got.Data) != 2 {
		t.Fatal(got, err)
	}
	var value ChannelModelStats
	for _, v := range got.Data {
		if v.Provider == "provider" {
			value = v
		}
	}
	if value.Success != 3 || value.Failed != 1 || value.Cancelled != 1 || value.SuccessRate == nil || *value.SuccessRate != .75 {
		t.Fatal(value)
	}
	if value.FirstText.Samples != 2 || *value.FirstText.P50 != 40 || *value.FirstText.P95 != 58 || *value.FirstText.Mean != 40 {
		t.Fatal(value.FirstText)
	}
	if value.Duration.Samples != 3 || *value.Duration.P50 != 400 || *value.Duration.Mean != 1600.0/3 {
		t.Fatal(value.Duration)
	}
	if got.From != (now.Add(-24*time.Hour).Unix()/60)*60 || got.To != now.Unix() {
		t.Fatal(got.From, got.To)
	}
	f.SourceIDs = []string{}
	got, err = e.ChannelModelStats(ctx, f)
	if err != nil || len(got.Data) != 0 {
		t.Fatal("source authorization leak", got, err)
	}
	f.SourceIDs = []string{"a"}
	f.SourceID = "b"
	got, err = e.ChannelModelStats(ctx, f)
	if err != nil || len(got.Data) != 0 {
		t.Fatal("cross-source leak", got, err)
	}
	f.SourceID = "a"
	f.Model = "' OR 1=1 --"
	got, err = e.ChannelModelStats(ctx, f)
	if err != nil || len(got.Data) != 0 {
		t.Fatal("unsafe model filter", got, err)
	}
}

func TestChannelModelStatsNoSamplesAndValidation(t *testing.T) {
	e := stateTestEngine(t)
	f := Fact{Schema: 1, EventID: "null", Kind: "attempt", Outcome: "failed/other", AtMS: time.Now().Add(-time.Minute).UnixMilli(), Provider: "p", Model: "m"}
	if err := e.Import(context.Background(), "empty-times", "1", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	got, err := e.ChannelModelStats(context.Background(), QueryFilter{Range: "24h"})
	if err != nil || len(got.Data) != 1 {
		t.Fatal(got, err)
	}
	if got.Data[0].Duration.P50 != nil || got.Data[0].FirstText.Mean != nil || got.Data[0].Duration.Samples != 0 {
		t.Fatal("invented time", got)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"can_inspect_all": r.Header.Get("Authorization") == "Bearer admin-secret"})
	}))
	defer up.Close()
	s, err := NewService(e, Config{Upstream: up.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token, path string
		status      int
	}{
		{"", "/v1/channel-model-stats", 401}, {"user-secret", "/v1/channel-model-stats", 403},
		{"admin-secret", "/v1/channel-model-stats", 200}, {"admin-secret", "/v1/channel-model-stats?range=bad", 400},
		{"admin-secret", "/v1/channel-model-stats?to=bad", 400}, {"admin-secret", "/v1/channel-model-stats?to=0", 400},
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
		if w.Code == 200 {
			var result ChannelModelStatsResult
			if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Data) != 1 {
				t.Fatal(w.Body.String())
			}
		}
	}
}

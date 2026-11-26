package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestChannelTierTPSUsesAllSuccessfulSamplesInScope(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	to := time.Now().Truncate(time.Second).UnixMilli()
	from := to - 300000
	var facts []Fact
	add := func(tier string, out int64, change func(*Fact)) {
		duration := 1000.0
		f := Fact{Schema: 1, EventID: fmt.Sprint(len(facts)), Kind: "request", RequestID: "reused-id",
			SourceID: "a", Provider: "same", KeyID: "key", Model: "alias", UpstreamModel: "wire",
			AtMS: from, Status: 200, Outcome: "success", Endpoint: "/v1/responses", Stream: true,
			OutputTokens: &out, DurationMS: &duration, TraceDetail: json.RawMessage(mustJSON(map[string]string{"requested_service_tier": tier}))}
		if change != nil {
			change(&f)
		}
		facts = append(facts, f)
	}
	add("fast", 10, nil)
	add("priority", 30, func(f *Fact) { f.AtMS = to })
	add("ultrafast", 100, nil)
	add("ultrafast", 500, func(f *Fact) { f.KeyID = "other-key" })
	add("ultrafast", 500, func(f *Fact) { f.SourceID = "other-source" })
	add("ultrafast", 500, func(f *Fact) { f.Model = "other-model" })
	add("fast", 500, func(f *Fact) { f.AtMS = from - 1 })
	add("fast", 500, func(f *Fact) { f.Status = 503 })
	add("fast", 500, func(f *Fact) { f.Kind = "attempt" })
	add("fast", 500, func(f *Fact) { f.Stream = false })
	add("fast", 500, func(f *Fact) { f.Endpoint = "/v1/responses/compact" })
	add("fast", 500, func(f *Fact) { f.DurationMS = nil })
	add("fast", 0, nil)
	add("default", 500, nil)
	if err := e.Import(ctx, "tier-tps", "1", facts); err != nil {
		t.Fatal(err)
	}
	rows, err := e.readChannelTierTPS(ctx, QueryFilter{SourceIDs: []string{"a"}, KeyID: "key", Model: "alias", Stream: "true"}, from, to)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	byTier := map[string]channelTierTPSRow{}
	for _, row := range rows {
		byTier[row.Tier] = row
	}
	if fast, ultra := byTier["fast"], byTier["ultrafast"]; fast.Samples != 2 || fast.P50 != 20 || fast.P95 != 29 ||
		fast.UpstreamModel != "wire" || ultra.Samples != 1 || ultra.P50 != 100 || ultra.P95 != 100 {
		t.Fatal(byTier)
	}
}

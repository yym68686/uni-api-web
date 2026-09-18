package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestResponseEventLatencyRollupsExcludeLegacySamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latencies.duckdb")
	e, err := OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { e.Close() }()
	oldOutput, created, text := 9000.0, 20.0, 70.0
	legacy := Fact{Schema: 1, EventID: "legacy", Kind: "attempt", AtMS: time.Now().Add(-time.Second).UnixMilli(), Provider: "p", Model: "m", Endpoint: "/v1/responses", Stream: true, Outcome: "success", FirstOutputMS: &oldOutput}
	current := legacy
	current.EventID = "current"
	current.ResponseCreatedMS = &created
	current.FirstTextMS = &text
	if err = e.Import(context.Background(), "latencies.jsonl", "etag", []Fact{legacy, current}); err != nil {
		t.Fatal(err)
	}
	// Verify both minute and day aggregates, then reopen the stored database.
	for _, window := range []string{"15m", "all"} {
		result, err := e.Query(context.Background(), QueryFilter{Range: window})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Data) != 1 {
			t.Fatal(result)
		}
		stats := result.Data[0].Stats
		for name, expected := range map[string]float64{"response_created": 20, "first_text": 100} {
			dist := stats[name].(map[string]any)
			if dist["sample_count"] != int64(1) || dist["p50_ms"] != expected {
				t.Fatalf("%s %s: %#v", window, name, dist)
			}
		}
		if stats["first_output"].(map[string]any)["sample_count"] != int64(2) {
			t.Fatal("legacy metric changed")
		}
	}
	e.Close()
	e, err = OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Query(context.Background(), QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data[0].Stats["response_created"].(map[string]any)["sample_count"] != int64(1) {
		t.Fatal("restart lost event timings")
	}
}

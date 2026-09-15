package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestBulkImportLargeBatchPreservesNullsAndEventDeduplication(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	objects := make([]FactObject, 128)
	at := time.Now().Add(-time.Minute).UnixMilli()
	zero := int64(0)
	for i := range objects {
		objects[i] = FactObject{Key: fmt.Sprintf("%d.jsonl", i), ETag: fmt.Sprint(i)}
		for j := 0; j < 16; j++ {
			f := Fact{Schema: 1, EventID: fmt.Sprintf("%d-%d", i, j), RequestID: "repeated-caller-id", Kind: "request", AtMS: at, Provider: "channel", Model: "model", Outcome: "success"}
			if j%2 == 0 {
				f.InputTokens = &zero
				f.CacheReadTokens = &zero
			}
			objects[i].Facts = append(objects[i].Facts, f)
		}
	}
	if err := e.ImportBatch(ctx, objects); err != nil {
		t.Fatal(err)
	}
	if err := e.Import(ctx, "duplicate.jsonl", "another", objects[0].Facts); err != nil {
		t.Fatal(err)
	}
	result, err := e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total["requests"] != float64(2048) || result.Total["usage_samples"] != float64(1024) || result.Total["cache_samples"] != float64(1024) {
		t.Fatalf("changed fact semantics: %v", result.Total)
	}
	var checkpoints int
	if err := e.DB.QueryRow("SELECT count(*) FROM imported_objects").Scan(&checkpoints); err != nil || checkpoints != 129 {
		t.Fatal(checkpoints, err)
	}
}

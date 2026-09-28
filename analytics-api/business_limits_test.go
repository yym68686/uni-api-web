package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestPricePersistenceBeyondFormerSizeAndModelLimits(t *testing.T) {
	objects := &fakeStateObjects{}
	store := &stateStore{client: objects, bucket: "fixture", key: "prices"}
	doc := priceState{Schema: 1}
	for i := 0; i < 16000; i++ {
		doc.Prices = append(doc.Prices, Price{Model: fmt.Sprintf("model-%d-", i) + strings.Repeat("m", 220), Input: 1, Output: 2, Verified: true})
	}
	etag, err := store.put(context.Background(), doc, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(objects.body) <= 4<<20 {
		t.Fatal("fixture does not exceed old byte quota", len(objects.body))
	}
	restored, got, exists, err := store.read(context.Background())
	if err != nil || !exists || etag != got || len(restored.Prices) != len(doc.Prices) {
		t.Fatal("incomplete persistence", len(restored.Prices), err)
	}
}

func TestAutomationUsesCompleteLargeWindowAndExactQuantiles(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute).UnixMilli()
	const count = 200123
	_, err := e.DB.ExecContext(ctx, `INSERT INTO facts(source_id,event_id,kind,provider,upstream_model,key_id,model,endpoint,stream,outcome,at_ms,response_created_ms) SELECT 'fixture','event-'||i,'attempt','p','u','k','m','/v1/responses',true,CASE WHEN i%10=0 THEN 'failed' ELSE 'success' END,?,i::DOUBLE FROM range(?) t(i)`, at, count)
	if err != nil {
		t.Fatal(err)
	}
	p := AutomationPolicy{Endpoint: "/v1/responses", Stream: "true", ConfidenceLevel: .95, LatencyQuantile: .95, MaxAgeSeconds: 3600, Metrics: []string{"latency", "success"}}
	metrics, err := (&Service{engine: e}).automationMetrics(ctx, AutomationTask{SourceID: "fixture", KeyID: "k", Model: "m", Range: "1h", Policy: p}, []automationChannel{{Provider: "p", Upstream: "u", Eligible: true}})
	if err != nil {
		t.Fatal(err)
	}
	m := metrics["p"]
	xs := make([]float64, count)
	for i := range xs {
		xs[i] = float64(i)
	}
	want, ci, valid := quantileInterval(xs, p.LatencyQuantile, confidenceZ(p.ConfidenceLevel))
	if !valid || m.LatencyN != count || m.SuccessN != count || m.Latency != want || m.LatencyCI != ci || math.Abs(m.Success-float64(count-(count+9)/10)/count) > 1e-12 || m.Reason != "" {
		t.Fatalf("incomplete or changed metrics: %+v want=%v ci=%v", m, want, ci)
	}
}

func TestKeyPaginationHasNoPageCeilingAndRejectsNonProgress(t *testing.T) {
	seen := keyPageProgress{}
	for i := int64(1); i <= 10001; i++ {
		if err := seen.advance([]subRemoteKey{{ID: i}}); err != nil {
			t.Fatal(err)
		}
	}
	if seen.advance([]subRemoteKey{{ID: 10001}}) == nil {
		t.Fatal("repeated page accepted")
	}
}

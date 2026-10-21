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
	e := stateTestEngine(t)
	ctx := context.Background()
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 16000; i++ {
		_, err = tx.ExecContext(ctx, "INSERT INTO prices(model,input,output,verified) VALUES(?,?,?,?)", fmt.Sprintf("bulk-%d-", i)+strings.Repeat("m", 220), 1, 2, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	other := replicaTestEngine(t, e)
	var count int
	if err = other.DB.QueryRow("SELECT count(*) FROM prices WHERE model LIKE 'bulk-%'").Scan(&count); err != nil || count != 16000 {
		t.Fatal(count, err)
	}
}

func TestAutomationUsesCompleteLargeWindowAndExactQuantiles(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute).UnixMilli()
	const count = 200123
	tx, _ := e.DB.BeginTx(ctx, nil)
	if err := ensureFactPartition(ctx, tx, at); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, err := e.DB.ExecContext(ctx, `INSERT INTO facts(source_id,event_id,kind,provider,upstream_model,key_id,model,endpoint,stream,outcome,at_ms,response_created_ms) SELECT 'fixture','event-'||i,'attempt','p','u','k','m','/v1/responses',true,CASE WHEN i%10=0 THEN 'failed' ELSE 'success' END,?,i::DOUBLE PRECISION FROM generate_series(0,?::bigint-1) t(i)`, at, count)
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

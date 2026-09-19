package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// Both results use the same server-owned cutoff and arrive in one response.
// Never share an account's receipt payload through the analytics response cache.
func (s *Service) analyticsWithSpend(w http.ResponseWriter, r *http.Request, allowed []string, name string) {
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "login required for account billing", http.StatusUnauthorized)
		return
	}
	began := time.Now()
	cutoff := began.UTC().Truncate(time.Second)
	start, err := rangeStart(name, cutoff, s.engine.Location)
	if err != nil {
		http.Error(w, "invalid time range", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	f := QueryFilter{SourceIDs: allowed, SourceID: q.Get("source_id"), Range: name, Model: q.Get("model"), UpstreamModel: q.Get("upstream_model"), Provider: q.Get("provider"), Endpoint: q.Get("endpoint"), Stream: q.Get("stream"), KeyID: q.Get("key_id"), Timeseries: q.Get("timeseries") == "true", To: cutoff.Unix()}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var result QueryResult
	var spend []attributedSpend
	var metricsErr, spendErr error
	var metricsMS, spendMS float64
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		t := time.Now()
		result, metricsErr = s.engine.Query(ctx, f)
		metricsMS = float64(time.Since(t).Microseconds()) / 1000
	}()
	go func() {
		defer wg.Done()
		t := time.Now()
		spendFilter := f
		if q.Has("spend_model") {
			spendFilter.Model = q.Get("spend_model")
		}
		spend, spendErr = s.attributedChannelSpend(ctx, owner, spendFilter, start.UnixMilli()/60000*60, cutoff.Unix())
		spendMS = float64(time.Since(t).Microseconds()) / 1000
	}()
	wg.Wait()
	w.Header().Set("Server-Timing", fmt.Sprintf("metrics;dur=%.1f, spend;dur=%.1f, total;dur=%.1f", metricsMS, spendMS, float64(time.Since(began).Microseconds())/1000))
	if metricsErr != nil {
		log.Printf("event=analytics_spend_metrics_failed error=%q", metricsErr)
		http.Error(w, "analytics unavailable", 503)
		return
	}
	if spendErr != nil {
		result.ChannelSpendError = "账单核对暂不可用"
		log.Printf("event=analytics_spend_query_failed error=%q", spendErr)
	} else {
		result.ChannelSpend = &spend
	}
	result.Import = s.importStatus(allowed)
	body, err := json.Marshal(result)
	if err != nil {
		http.Error(w, "analytics unavailable", 500)
		return
	}
	s.writeAnalyticsBody(w, r, body)
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ModelTiming struct {
	Samples int64    `json:"sample_count"`
	Mean    *float64 `json:"mean_ms"`
	P50     *float64 `json:"p50_ms"`
	P95     *float64 `json:"p95_ms"`
}

type ChannelModelStats struct {
	SourceID        string      `json:"source_id"`
	Provider        string      `json:"provider"`
	Model           string      `json:"model"`
	Success         int64       `json:"success"`
	Failed          int64       `json:"failed"`
	Cancelled       int64       `json:"cancelled"`
	SuccessRate     *float64    `json:"success_rate"`
	FirstText       ModelTiming `json:"first_text"`
	ResponseCreated ModelTiming `json:"response_created"`
	Duration        ModelTiming `json:"duration"`
}

type ChannelModelStatsResult struct {
	Data   []ChannelModelStats `json:"data"`
	From   int64               `json:"from"`
	To     int64               `json:"to"`
	Import map[string]any      `json:"import"`
}

// One projection for all channel/model pairs. Completed attempt facts contain
// the channel's own timings; final request facts would misattribute previous
// retries and dispatch waiting to the last provider. Import deduplicates events.
func (e *Engine) ChannelModelStats(ctx context.Context, f QueryFilter) (ChannelModelStatsResult, error) {
	now := time.Now().UTC()
	if f.To > 0 {
		now = time.Unix(f.To, 0).UTC()
	}
	start, err := rangeStart(f.Range, now, e.Location)
	if err != nil {
		return ChannelModelStatsResult{}, err
	}
	// Match the existing analytics window's minute boundary.
	from := start.UnixMilli() / 60000 * 60000
	where, args := sourceWhere([]string{"kind='attempt'", "at_ms>=? AND at_ms<=?", "coalesce(outcome,'')<>'skipped'"}, []any{from, now.UnixMilli()}, f)
	for _, field := range [][2]string{{"key_id", f.KeyID}, {"model", f.Model}} {
		if field[1] != "" && field[1] != "all" {
			where = append(where, field[0]+"=?")
			args = append(args, field[1])
		}
	}
	// Cancelled/hedged attempts have no final success/failure and are excluded
	// from both the denominator and timing distributions.
	success := "coalesce(outcome,'') IN ('success','completed')"
	cancelled := "coalesce(outcome,'') IN ('cancelled','client_cancelled','hedge_cancelled')"
	columns := []string{"coalesce(source_id,'')", "coalesce(provider,'')", "coalesce(model,'')", "count(*) FILTER (WHERE " + success + ")", "count(*) FILTER (WHERE NOT (" + success + " OR " + cancelled + "))", "count(*) FILTER (WHERE " + cancelled + ")"}
	for _, field := range []string{"first_text_ms", "response_created_ms", "duration_ms"} {
		value := "CASE WHEN NOT (" + cancelled + ") AND isfinite(" + field + ") AND " + field + ">=0 THEN " + field + " END"
		columns = append(columns, "count("+value+")", "avg("+value+")", "quantile_cont("+value+",0.5)", "quantile_cont("+value+",0.95)")
	}
	rows, err := e.DB.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM facts WHERE "+strings.Join(where, " AND ")+" GROUP BY source_id,provider,model ORDER BY source_id,provider,model", args...)
	if err != nil {
		return ChannelModelStatsResult{}, err
	}
	defer rows.Close()
	out := ChannelModelStatsResult{Data: []ChannelModelStats{}, From: from / 1000, To: now.Unix()}
	for rows.Next() {
		var v ChannelModelStats
		if err := rows.Scan(&v.SourceID, &v.Provider, &v.Model, &v.Success, &v.Failed, &v.Cancelled, &v.FirstText.Samples, &v.FirstText.Mean, &v.FirstText.P50, &v.FirstText.P95, &v.ResponseCreated.Samples, &v.ResponseCreated.Mean, &v.ResponseCreated.P50, &v.ResponseCreated.P95, &v.Duration.Samples, &v.Duration.Mean, &v.Duration.P50, &v.Duration.P95); err != nil {
			return out, err
		}
		if n := v.Success + v.Failed; n > 0 {
			rate := float64(v.Success) / float64(n)
			v.SuccessRate = &rate
		}
		out.Data = append(out.Data, v)
	}
	return out, rows.Err()
}

func (s *Service) channelModelStats(w http.ResponseWriter, r *http.Request) {
	if !s.analyticsReady() {
		s.initializing(w)
		return
	}
	allowed, err := s.analyticsSources(r)
	if err != nil {
		http.Error(w, "source unavailable", 404)
		return
	}
	q := r.URL.Query()
	name := q.Get("range")
	if name == "" {
		name = "15m"
	}
	if _, err := rangeStart(name, time.Now().UTC(), s.engine.Location); err != nil {
		http.Error(w, "invalid range", 400)
		return
	}
	var cutoff int64
	if q.Has("to") {
		cutoff, err = strconv.ParseInt(q.Get("to"), 10, 64)
		if err != nil || cutoff <= 0 || cutoff > time.Now().Unix()+60 {
			http.Error(w, "invalid cutoff", 400)
			return
		}
	}
	key := "channel-model-stats|" + q.Encode() + "|" + strings.Join(allowed, ",")
	revision := s.engine.Revision.Load()
	s.cacheMu.Lock()
	cached, ok := s.cache[key]
	s.cacheMu.Unlock()
	if ok && cached.revision == revision && time.Now().Before(cached.expires) {
		s.writeAnalyticsBody(w, r, cached.body)
		return
	}
	result, err := s.engine.ChannelModelStats(r.Context(), QueryFilter{SourceIDs: allowed, SourceID: q.Get("source_id"), KeyID: q.Get("key_id"), Range: name, Model: q.Get("model"), To: cutoff})
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "channel model statistics unavailable"})
		return
	}
	result.Import = s.importStatus(allowed)
	body, err := json.Marshal(result)
	if err != nil {
		http.Error(w, "channel model statistics unavailable", 500)
		return
	}
	s.cacheMu.Lock()
	if len(s.cache) >= 256 {
		for k := range s.cache {
			delete(s.cache, k)
			break
		}
	}
	s.cache[key] = cachedAnalytics{body: body, revision: revision, expires: time.Now().Add(5 * time.Second)}
	s.cacheMu.Unlock()
	s.writeAnalyticsBody(w, r, body)
}

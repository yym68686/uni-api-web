package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type RequestOutcomes struct {
	Requests    int64    `json:"requests"`
	Success     int64    `json:"success"`
	Failed      int64    `json:"failed"`
	SuccessRate *float64 `json:"success_rate"`
}

func (v *RequestOutcomes) finish() {
	v.Failed = v.Requests - v.Success
	if v.Requests > 0 {
		rate := float64(v.Success) / float64(v.Requests)
		v.SuccessRate = &rate
	}
}

type KeyRequestStats struct {
	SourceID string `json:"source_id"`
	KeyID    string `json:"key_id"`
	RequestOutcomes
}

type KeyRequestStatsResult struct {
	Data   []KeyRequestStats `json:"data"`
	Total  RequestOutcomes   `json:"total"`
	From   int64             `json:"from"`
	To     int64             `json:"to"`
	Import map[string]any    `json:"import"`
}

func (e *Engine) KeyRequestStats(ctx context.Context, f QueryFilter) (KeyRequestStatsResult, error) {
	now, startMS, where, args, err := e.rollupWindow(f)
	if err != nil {
		return KeyRequestStatsResult{}, err
	}
	// The gateway emits one request fact after the final outcome. Attempt,
	// dispatch and billing facts include retries/hedges and must never enter
	// this denominator. Replayed request events are deduplicated on import by
	// event_id; caller-supplied request_id may legitimately be reused.
	where = append(where, "kind='request'", "COALESCE(key_id,'')<>''")
	for _, entry := range [][2]string{{"model", f.Model}, {"endpoint", f.Endpoint}} {
		if entry[1] != "" && entry[1] != "all" {
			where = append(where, entry[0]+"=?")
			args = append(args, entry[1])
		}
	}
	if f.Stream == "true" || f.Stream == "false" {
		where = append(where, "stream=?")
		args = append(args, f.Stream == "true")
	}
	where, args = sourceWhere(where, args, f)
	// Return every key in one aggregation, independent of the selected key or
	// winning provider. A failure before dispatch still counts as one failure.
	rows, err := e.DB.QueryContext(ctx, `SELECT source_id,key_id,sum(n)::BIGINT,
 coalesce(sum(n) FILTER (WHERE outcome IN ('success','completed')),0)::BIGINT
 FROM rollups WHERE `+strings.Join(where, " AND ")+` GROUP BY source_id,key_id ORDER BY source_id,key_id`, args...)
	if err != nil {
		return KeyRequestStatsResult{}, err
	}
	defer rows.Close()
	out := KeyRequestStatsResult{Data: []KeyRequestStats{}, From: startMS / 1000, To: now.Unix()}
	for rows.Next() {
		var v KeyRequestStats
		if err := rows.Scan(&v.SourceID, &v.KeyID, &v.Requests, &v.Success); err != nil {
			return KeyRequestStatsResult{}, err
		}
		v.finish()
		out.Data = append(out.Data, v)
		out.Total.Requests += v.Requests
		out.Total.Success += v.Success
	}
	out.Total.finish()
	return out, rows.Err()
}

func (s *Service) keyRequestStats(w http.ResponseWriter, r *http.Request) {
	if !s.analyticsReady() {
		s.initializing(w)
		return
	}
	allowed, err := s.analyticsSources(r)
	if err != nil {
		http.Error(w, "source unavailable", http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	name := q.Get("range")
	if name == "" {
		name = "15m"
	}
	if _, err := rangeStart(name, time.Now().UTC(), s.engine.Location); err != nil {
		http.Error(w, "invalid time range", http.StatusBadRequest)
		return
	}
	result, err := s.engine.KeyRequestStats(r.Context(), QueryFilter{SourceIDs: allowed, SourceID: q.Get("source_id"), Range: name, Model: q.Get("model"), Endpoint: q.Get("endpoint"), Stream: q.Get("stream")})
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "request statistics unavailable"})
		return
	}
	result.Import = s.importStatus(allowed)
	body, err := json.Marshal(result)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "request statistics unavailable"})
		return
	}
	s.writeAnalyticsBody(w, r, body)
}

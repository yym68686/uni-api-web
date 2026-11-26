package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type channelTierTPSRow struct {
	SourceID      string  `json:"source_id"`
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	UpstreamModel string  `json:"upstream_model"`
	Tier          string  `json:"tier"`
	Samples       int64   `json:"samples"`
	P50           float64 `json:"p50"`
	P95           float64 `json:"p95"`
}

const requestedTierSQL = `NULLIF(trace_detail::text,'')::jsonb->>'requested_service_tier'`

func (e *Engine) readChannelTierTPS(ctx context.Context, f QueryFilter, from, to int64) (_ []channelTierTPSRow, readErr error) {
	release, err := e.acquireAnalytical(ctx, "channel_tier_tps")
	if err != nil {
		return nil, err
	}
	defer release()
	operation := e.observeRequestOperation(ctx, "channel_tier_tps")
	defer func() { operation.finish(readErr) }()
	// Count only complete successful requests, once, on their final provider.
	// Older facts without tier metadata can't identify either tier.
	where, args := sourceWhere([]string{
		"at_ms>=? AND at_ms<=?", "kind='request'", "provider<>''", "model<>''",
		"endpoint IN ('/v1/responses','/v1/responses/')",
		"(" + requestedTierSQL + ") IN ('fast','priority','ultrafast')",
		"(" + requestLogResultSQL + ")='success'", "output_tokens>0", "duration_ms>0",
	}, []any{from, to}, f)
	for _, filter := range [][2]string{{"key_id", f.KeyID}, {"model", f.Model}, {"endpoint", f.Endpoint}} {
		if filter[1] != "" && filter[1] != "all" {
			where = append(where, filter[0]+"=?")
			args = append(args, filter[1])
		}
	}
	if f.Stream == "true" || f.Stream == "false" {
		where = append(where, "stream=?")
		args = append(args, f.Stream == "true")
	}
	rows, err := e.DB.QueryContext(ctx, `SELECT source_id,provider,model,wire_model,tier,samples,quantiles[1],quantiles[2] FROM (
 SELECT source_id,provider,model,coalesce(nullif(upstream_model,''),model) AS wire_model,
 CASE WHEN (`+requestedTierSQL+`)='ultrafast' THEN 'ultrafast' ELSE 'fast' END AS tier,
 count(*) AS samples, percentile_cont(ARRAY[0.5,0.95]::double precision[])
 WITHIN GROUP (ORDER BY (1000.0*output_tokens)/duration_ms) AS quantiles
 FROM facts WHERE `+strings.Join(where, " AND ")+` GROUP BY 1,2,3,4,5) tps`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	data := []channelTierTPSRow{}
	for rows.Next() {
		var row channelTierTPSRow
		if err := rows.Scan(&row.SourceID, &row.Provider, &row.Model, &row.UpstreamModel, &row.Tier, &row.Samples, &row.P50, &row.P95); err != nil {
			return nil, err
		}
		data = append(data, row)
	}
	return data, rows.Err()
}

func (s *Service) channelTierTPS(w http.ResponseWriter, r *http.Request) {
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
	now := time.Now()
	cutoff, err := strconv.ParseInt(q.Get("to"), 10, 64)
	if err != nil || cutoff <= 0 || cutoff > now.Add(time.Minute).Unix() {
		http.Error(w, "invalid time range", http.StatusBadRequest)
		return
	}
	end := time.Unix(cutoff, 0)
	start, err := rangeStart(q.Get("range"), end, s.engine.Location)
	if err != nil {
		http.Error(w, "invalid time range", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	filter := QueryFilter{SourceIDs: allowed, SourceID: q.Get("source_id"), KeyID: q.Get("key_id"), Model: q.Get("model"), Endpoint: q.Get("endpoint"), Stream: q.Get("stream")}
	data, err := s.engine.readChannelTierTPS(ctx, filter, start.UnixMilli(), end.UnixMilli())
	if err != nil {
		http.Error(w, "channel tier statistics unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, 200, map[string]any{"data": data})
}

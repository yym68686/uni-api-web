package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// A final request fact is emitted once after all retries. Do not group by the
// caller's request ID: it can be reused, including across instances/sources.
type RequestLog struct {
	EventID           string   `json:"event_id"`
	SourceID          string   `json:"source_id"`
	InstanceID        string   `json:"instance_id"`
	RequestID         string   `json:"request_id"`
	TraceID           string   `json:"trace_id"`
	KeyID             string   `json:"key_id"`
	Model             string   `json:"model"`
	Endpoint          string   `json:"endpoint"`
	Stream            bool     `json:"stream"`
	AtMS              int64    `json:"at_ms"`
	StartedMS         *int64   `json:"started_ms"`
	Status            int      `json:"status"`
	Outcome           string   `json:"outcome"`
	Result            string   `json:"result"`
	FailureReason     string   `json:"failure_reason"`
	DurationMS        *float64 `json:"duration_ms"`
	DispatchMS        *float64 `json:"dispatch_ms"`
	ResponseCreatedMS *float64 `json:"response_created_ms"`
	FirstTextMS       *float64 `json:"first_text_ms"`
	InputTokens       *int64   `json:"input_tokens"`
	OutputTokens      *int64   `json:"output_tokens"`
	CacheReadTokens   *int64   `json:"cache_read_tokens"`
}
type RequestLogFilter struct {
	QueryFilter
	Search, Status, Balance, Sort, Cursor string
}
type requestLogCursor struct {
	To    int64   `json:"to"`
	Value float64 `json:"value"`
	At    int64   `json:"at"`
	ID    string  `json:"id"`
}
type RequestLogsResult struct {
	Data       []RequestLog    `json:"data"`
	NextCursor string          `json:"next_cursor"`
	Models     []string        `json:"models"`
	Keys       []RequestLogKey `json:"keys"`
	Endpoints  []string        `json:"endpoints"`
	From       int64           `json:"from"`
	To         int64           `json:"to"`
	Import     map[string]any  `json:"import"`
}
type RequestLogKey struct {
	SourceID string `json:"source_id"`
	KeyID    string `json:"key_id"`
}

const requestLogResultSQL = `CASE
 WHEN response_completed=false OR starts_with(coalesce(outcome,''),'failed') OR starts_with(coalesce(outcome,''),'error') OR status>=400 THEN 'failed'
 WHEN contains(coalesce(outcome,''),'cancel') THEN 'cancelled'
 WHEN outcome IN ('success','completed') OR response_completed=true THEN 'success'
 ELSE 'unknown' END`

func (e *Engine) RequestLogs(ctx context.Context, f RequestLogFilter) (RequestLogsResult, error) {
	out := RequestLogsResult{Data: []RequestLog{}, Models: []string{}}
	now := time.Now().UTC()
	if f.To > 0 {
		now = time.Unix(f.To, 0).UTC()
	}
	var cursor requestLogCursor
	if f.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.To <= 0 || cursor.At < 0 || cursor.ID == "" || math.IsNaN(cursor.Value) || math.IsInf(cursor.Value, 0) {
			return out, errors.New("invalid cursor")
		}
		now = time.UnixMilli(cursor.To).UTC()
	}
	start, err := rangeStart(f.Range, now, e.Location)
	if err != nil {
		return out, err
	}
	out.From, out.To = start.UnixMilli(), now.UnixMilli()
	where, args := sourceWhere([]string{"kind='request'", "at_ms>=? AND at_ms<=?"}, []any{out.From, out.To}, f.QueryFilter)
	// Historical facets stay available when keys/models are removed from live routes.
	models, err := e.DB.QueryContext(ctx, `SELECT DISTINCT coalesce(source_id,''),coalesce(key_id,''),coalesce(model,''),coalesce(endpoint,'') FROM facts WHERE `+strings.Join(where, " AND ")+` ORDER BY source_id,key_id,model,endpoint`, args...)
	if err != nil {
		return out, err
	}
	seenKeys, seenModels, seenEndpoints := map[RequestLogKey]bool{}, map[string]bool{}, map[string]bool{}
	for models.Next() {
		var model, endpoint string
		var key RequestLogKey
		if err = models.Scan(&key.SourceID, &key.KeyID, &model, &endpoint); err != nil {
			models.Close()
			return out, err
		}
		if key.KeyID != "" && !seenKeys[key] {
			seenKeys[key] = true
			out.Keys = append(out.Keys, key)
		}
		if f.KeyID == "" || f.KeyID == key.KeyID {
			if model != "" && !seenModels[model] {
				seenModels[model] = true
				out.Models = append(out.Models, model)
			}
			if endpoint != "" && !seenEndpoints[endpoint] {
				seenEndpoints[endpoint] = true
				out.Endpoints = append(out.Endpoints, endpoint)
			}
		}
	}
	err = models.Err()
	models.Close()
	if err != nil {
		return out, err
	}
	if f.KeyID != "" {
		where = append(where, "key_id=?")
		args = append(args, f.KeyID)
	}
	for _, field := range [][2]string{{"model", f.Model}, {"endpoint", f.Endpoint}} {
		if field[1] != "" && field[1] != "all" {
			where = append(where, field[0]+"=?")
			args = append(args, field[1])
		}
	}
	if f.Stream == "true" || f.Stream == "false" {
		where = append(where, "stream=?")
		args = append(args, f.Stream == "true")
	}
	if f.Search != "" {
		where = append(where, `(contains(lower(coalesce(request_id,'')),lower(?)) OR contains(lower(coalesce(trace_id,'')),lower(?)))`)
		args = append(args, f.Search, f.Search)
	}
	if f.Balance == "low" {
		where = append(where, `(contains(lower(coalesce(failure_reason,'')),'balance') OR contains(lower(coalesce(outcome,'')),'balance') OR contains(lower(coalesce(failure_reason,'')),'quota'))`)
	}
	sortExpr := "-at_ms::DOUBLE"
	switch f.Sort {
	case "oldest":
		sortExpr = "at_ms::DOUBLE"
	case "success":
		sortExpr = "CASE result WHEN 'success' THEN 0 WHEN 'failed' THEN 1 ELSE 2 END"
	case "latency":
		sortExpr = "coalesce(response_created_ms,first_text_ms,1e30)"
	case "wait":
		sortExpr = "coalesce(dispatch_ms,1e30)"
	}
	outer := []string{"true"}
	if f.Status != "" {
		outer = append(outer, "result=?")
		args = append(args, f.Status)
	}
	if f.Cursor != "" {
		outer = append(outer, `(sort_value>? OR (sort_value=? AND (at_ms<? OR (at_ms=? AND event_id<?))))`)
		args = append(args, cursor.Value, cursor.Value, cursor.At, cursor.At, cursor.ID)
	}
	// Fixed-size pages bound transfer size, not the number of accessible records.
	rows, err := e.DB.QueryContext(ctx, `WITH scoped AS (SELECT *,`+requestLogResultSQL+` AS result FROM facts WHERE `+strings.Join(where, " AND ")+`), ordered AS (SELECT *,`+sortExpr+` AS sort_value FROM scoped)
 SELECT event_id,coalesce(source_id,''),coalesce(instance_id,''),coalesce(request_id,''),coalesce(trace_id,''),coalesce(key_id,''),coalesce(model,''),coalesce(endpoint,''),coalesce(stream,false),at_ms,started_ms,coalesce(status,0),coalesce(outcome,''),result,coalesce(failure_reason,''),duration_ms,dispatch_ms,response_created_ms,first_text_ms,input_tokens,output_tokens,cache_read_tokens,sort_value
 FROM ordered WHERE `+strings.Join(outer, " AND ")+` ORDER BY sort_value,at_ms DESC,event_id DESC LIMIT 51`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var last requestLogCursor
	for rows.Next() {
		var v RequestLog
		var sortValue float64
		if err = rows.Scan(&v.EventID, &v.SourceID, &v.InstanceID, &v.RequestID, &v.TraceID, &v.KeyID, &v.Model, &v.Endpoint, &v.Stream, &v.AtMS, &v.StartedMS, &v.Status, &v.Outcome, &v.Result, &v.FailureReason, &v.DurationMS, &v.DispatchMS, &v.ResponseCreatedMS, &v.FirstTextMS, &v.InputTokens, &v.OutputTokens, &v.CacheReadTokens, &sortValue); err != nil {
			return out, err
		}
		if len(out.Data) == 50 {
			raw, _ := json.Marshal(last)
			out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		out.Data = append(out.Data, v)
		last = requestLogCursor{To: out.To, Value: sortValue, At: v.AtMS, ID: v.EventID}
	}
	return out, rows.Err()
}

func (s *Service) requestLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := q.Get("range")
	if name == "" {
		name = "15m"
	}
	if _, err := rangeStart(name, time.Now().UTC(), s.engine.Location); err != nil {
		http.Error(w, "invalid range", 400)
		return
	}
	valid := func(value string, values ...string) bool {
		for _, v := range values {
			if value == v {
				return true
			}
		}
		return false
	}
	if !valid(q.Get("status"), "", "success", "failed", "cancelled", "unknown") || !valid(q.Get("sort"), "", "latest", "oldest", "success", "latency", "wait") || !valid(q.Get("stream"), "", "all", "true", "false") || !valid(q.Get("balance"), "", "low") || len(q.Get("search")) > 512 || strings.IndexFunc(q.Get("search"), unicode.IsControl) >= 0 || len(q.Get("cursor")) > 4096 {
		http.Error(w, "invalid request filters", 400)
		return
	}
	if q.Get("cursor") != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		var c requestLogCursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.To <= 0 || c.At < 0 || c.ID == "" {
			http.Error(w, "invalid cursor", 400)
			return
		}
	}
	if !s.analyticsReady() {
		s.initializing(w)
		return
	}
	allowed, err := s.analyticsSources(r)
	if err != nil {
		http.Error(w, "source unavailable", 404)
		return
	}
	result, err := s.engine.RequestLogs(r.Context(), RequestLogFilter{QueryFilter: QueryFilter{SourceIDs: allowed, SourceID: q.Get("source_id"), KeyID: q.Get("key_id"), Range: name, Model: q.Get("model"), Endpoint: q.Get("endpoint"), Stream: q.Get("stream")}, Search: strings.TrimSpace(q.Get("search")), Status: q.Get("status"), Balance: q.Get("balance"), Sort: q.Get("sort"), Cursor: q.Get("cursor")})
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "request logs unavailable"})
		return
	}
	result.Import = s.importStatus(allowed)
	body, err := json.Marshal(result)
	if err != nil {
		http.Error(w, "request logs unavailable", 500)
		return
	}
	s.writeAnalyticsBody(w, r, body)
}

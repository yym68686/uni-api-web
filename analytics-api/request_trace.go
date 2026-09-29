package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode"
)

func requestTraceSchema(prefix string) string {
	return strings.NewReplacer("TABLE facts", "TABLE "+prefix+"facts", "ON facts", "ON "+prefix+"facts").Replace(`
 ALTER TABLE facts ADD COLUMN IF NOT EXISTS trace_id VARCHAR;
 ALTER TABLE facts ADD COLUMN IF NOT EXISTS stage VARCHAR;
 ALTER TABLE facts ADD COLUMN IF NOT EXISTS trace_detail VARCHAR;
 ALTER TABLE facts ADD COLUMN IF NOT EXISTS transport_timing VARCHAR;
 ALTER TABLE facts ADD COLUMN IF NOT EXISTS terminal_kind VARCHAR;
 ALTER TABLE facts ADD COLUMN IF NOT EXISTS failure_reason VARCHAR;
 ALTER TABLE facts ADD COLUMN IF NOT EXISTS response_completed BOOLEAN;
 CREATE INDEX IF NOT EXISTS facts_request ON facts(request_id);
 CREATE INDEX IF NOT EXISTS facts_trace ON facts(trace_id);`)
}

// Diagnostics are separate from metrics: they must not add attempts, cost or
// success samples. Explicit columns avoid exposing upstream credential hashes.
type RequestTraceEvent struct {
	EventID           string         `json:"event_id"`
	Kind              string         `json:"kind"`
	Stage             string         `json:"stage"`
	AtMS              int64          `json:"at_ms"`
	StartedMS         *int64         `json:"started_ms"`
	AttemptID         string         `json:"attempt_id"`
	Provider          string         `json:"provider"`
	Model             string         `json:"model"`
	UpstreamModel     string         `json:"upstream_model"`
	Endpoint          string         `json:"endpoint"`
	Stream            bool           `json:"stream"`
	Outcome           string         `json:"outcome"`
	Status            int            `json:"status"`
	TerminalKind      string         `json:"terminal_kind"`
	FailureReason     string         `json:"failure_reason"`
	ResponseCompleted *bool          `json:"response_completed"`
	DurationMS        *float64       `json:"duration_ms"`
	DispatchMS        *float64       `json:"dispatch_ms"`
	ResponseCreatedMS *float64       `json:"response_created_ms"`
	FirstTextMS       *float64       `json:"first_text_ms"`
	Detail            map[string]any `json:"detail"`
	Transport         map[string]any `json:"transport"`
}
type RequestTrace struct {
	SourceID   string              `json:"source_id"`
	InstanceID string              `json:"instance_id"`
	RequestID  string              `json:"request_id"`
	Events     []RequestTraceEvent `json:"events"`
	Ambiguous  bool                `json:"ambiguous"`
}
type RequestTraceResult struct {
	Data        []RequestTrace `json:"data"`
	RequestID   string         `json:"request_id"`
	Import      map[string]any `json:"import"`
	GeneratedAt int64          `json:"generated_at"`
}

func traceDetails(raw string) map[string]any {
	var in map[string]any
	_ = json.Unmarshal([]byte(raw), &in)
	out := map[string]any{}
	for name, value := range in {
		switch name {
		case "attempt_index", "attempt_status_code", "semantic_status_code", "duration_ms", "body_bytes", "resource_wait_ms", "headers_at_ms", "tool_pairs_converted", "reasoning_items_converted", "heartbeat_items_converted", "headers_received_ms", "first_upstream_chunk_ms", "public_stream_ready_ms", "first_wire_prepared_ms", "preflight_decode_ms", "preflight_read_wait_ms", "preflight_read_calls", "preflight_process_ms", "error_body_read_ms", "network_write_measured":
			switch value.(type) {
			case float64, bool:
				out[name] = value
			}
		case "attempt_outcome", "skip_reason", "status_origin", "failure_resource", "original_attempt_id", "error_code", "error_type", "error_message", "origin":
			if text, ok := value.(string); ok {
				words := strings.Fields(text)
				for i, word := range words {
					if strings.Contains(word, "sk-") || strings.Contains(word, "://") || strings.Contains(word, "@") || strings.Contains(strings.ToLower(word), "bearer") || len(word) > 96 {
						words[i] = "[redacted]"
					}
				}
				text = strings.Join(words, " ")
				runes := []rune(text)
				if len(runes) > 1024 {
					runes = runes[:1024]
				}
				out[name] = string(runes)
			}
		case "error":
			b, _ := json.Marshal(value)
			out[name] = traceDetails(string(b))
		}
	}
	return out
}

func (e *Engine) RequestTrace(ctx context.Context, id string, allowed []string, source string) (RequestTraceResult, error) {
	where, args := sourceWhere([]string{"(request_id=? OR trace_id=?)"}, []any{id, id}, QueryFilter{SourceIDs: allowed, SourceID: source})
	rows, err := e.DB.QueryContext(ctx, `WITH matched AS (SELECT DISTINCT source_id,instance_id,request_id FROM facts WHERE `+strings.Join(where, " AND ")+`) SELECT source_id,coalesce(instance_id,''),coalesce(request_id,''),event_id,kind,coalesce(stage,''),at_ms,started_ms,coalesce(attempt_id,''),coalesce(provider,''),coalesce(model,''),coalesce(upstream_model,''),coalesce(endpoint,''),coalesce(stream,false),coalesce(outcome,''),coalesce(status,0),coalesce(terminal_kind,''),coalesce(failure_reason,''),response_completed,duration_ms,dispatch_ms,response_created_ms,first_text_ms,coalesce(trace_detail,'{}'),coalesce(transport_timing,'{}') FROM facts f WHERE EXISTS(SELECT 1 FROM matched m WHERE m.source_id=f.source_id AND m.instance_id IS NOT DISTINCT FROM f.instance_id AND m.request_id IS NOT DISTINCT FROM f.request_id) ORDER BY source_id,instance_id,request_id,at_ms,event_id`, args...)
	if err != nil {
		return RequestTraceResult{}, err
	}
	defer rows.Close()
	out := RequestTraceResult{Data: []RequestTrace{}, RequestID: id, GeneratedAt: time.Now().Unix()}
	indices := map[string]int{}
	finals, starts := map[string]int{}, map[string]int{}
	for rows.Next() {
		var source, instance, request, detail, transport string
		var v RequestTraceEvent
		if err := rows.Scan(&source, &instance, &request, &v.EventID, &v.Kind, &v.Stage, &v.AtMS, &v.StartedMS, &v.AttemptID, &v.Provider, &v.Model, &v.UpstreamModel, &v.Endpoint, &v.Stream, &v.Outcome, &v.Status, &v.TerminalKind, &v.FailureReason, &v.ResponseCompleted, &v.DurationMS, &v.DispatchMS, &v.ResponseCreatedMS, &v.FirstTextMS, &detail, &transport); err != nil {
			return out, err
		}
		v.Detail, v.Transport = traceDetails(detail), traceDetails(transport)
		key := source + "\x00" + instance + "\x00" + request
		i, ok := indices[key]
		if !ok {
			i = len(out.Data)
			indices[key] = i
			out.Data = append(out.Data, RequestTrace{SourceID: source, InstanceID: instance, RequestID: request, Events: []RequestTraceEvent{}})
		}
		if v.Kind == "request" {
			finals[key]++
		}
		if v.Stage == "request_received" {
			starts[key]++
		}
		out.Data[i].Events = append(out.Data[i].Events, v)
		out.Data[i].Ambiguous = finals[key] > 1 || starts[key] > 1
	}
	return out, rows.Err()
}

func (s *Service) requestTrace(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("request_id"))
	if id == "" || len(id) > 512 || strings.IndexFunc(id, unicode.IsControl) >= 0 {
		http.Error(w, "invalid request id", 400)
		return
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
	result, err := s.engine.RequestTrace(r.Context(), id, allowed, r.URL.Query().Get("source_id"))
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "request trace unavailable"})
		return
	}
	result.Import = s.importStatus(allowed)
	body, err := json.Marshal(result)
	if err != nil {
		http.Error(w, "request trace unavailable", 500)
		return
	}
	s.writeAnalyticsBody(w, r, body)
}

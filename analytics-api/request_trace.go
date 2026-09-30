package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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
	KeyID             string         `json:"-"`
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
	FirstOutputMS     *float64       `json:"first_output_ms"`
	Detail            map[string]any `json:"detail"`
	Transport         map[string]any `json:"transport"`
}
type RequestTrace struct {
	Channels   map[string]TraceChannel `json:"channels,omitempty"`
	SourceID   string                  `json:"source_id"`
	InstanceID string                  `json:"instance_id"`
	RequestID  string                  `json:"request_id"`
	Events     []RequestTraceEvent     `json:"events"`
	Ambiguous  bool                    `json:"ambiguous"`
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

// Expose only the last observed upstream chunk timestamp from the stored raw
// stream diagnostics. Connection addresses and arbitrary nested data stay private.
func traceTransport(raw string) map[string]any {
	out := traceDetails(raw)
	var timing struct {
		RawStream *struct {
			Totals struct {
				LastChunkMS *float64 `json:"last_chunk_ms"`
			} `json:"totals"`
		} `json:"raw_stream"`
	}
	if json.Unmarshal([]byte(raw), &timing) == nil && timing.RawStream != nil {
		if last := timing.RawStream.Totals.LastChunkMS; last != nil && *last >= 0 {
			out["last_upstream_chunk_ms"] = *last
		}
	}
	return out
}

const traceEventColumns = `source_id,coalesce(instance_id,'') AS instance_id,coalesce(request_id,'') AS request_id,event_id,kind,coalesce(stage,''),at_ms,started_ms,coalesce(attempt_id,''),coalesce(provider,''),coalesce(model,''),coalesce(upstream_model,''),coalesce(endpoint,''),coalesce(stream,false),coalesce(outcome,''),coalesce(status,0),coalesce(terminal_kind,''),coalesce(failure_reason,''),response_completed,duration_ms,dispatch_ms,response_created_ms,first_text_ms,first_output_ms,coalesce(trace_detail,'{}'),coalesce(transport_timing,'{}'),coalesce(key_id,'')`

func (e *Engine) RequestTrace(ctx context.Context, id string, allowed []string, source string, instance ...string) (RequestTraceResult, error) {
	out := RequestTraceResult{Data: []RequestTrace{}, RequestID: id, GeneratedAt: time.Now().Unix()}
	// Separate equality probes use facts_request/facts_trace indexes. An OR
	// across both columns followed by a correlated join scans the whole history.
	// Materialize the small ID matches before applying source/instance filters:
	// DuckDB's ART scan only supports a single-column predicate at the scan.
	where, args := sourceWhere(nil, nil, QueryFilter{SourceIDs: allowed, SourceID: source})
	if len(instance) > 0 {
		where = append(where, "coalesce(instance_id,'')=?")
		args = append(args, instance[0])
	}
	scope := ""
	if len(where) > 0 {
		scope = " WHERE " + strings.Join(where, " AND ")
	}
	// DuckDB does not support sql.TxOptions.ReadOnly; all statements below
	// are SELECTs and share one snapshot while ingestion continues.
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	lookupArgs := append([]any{id, id}, args...)
	matched, err := tx.QueryContext(ctx, `WITH candidates AS MATERIALIZED (
 SELECT source_id,instance_id,request_id FROM facts WHERE request_id=?
 UNION ALL SELECT source_id,instance_id,request_id FROM facts WHERE trace_id=?
) SELECT DISTINCT source_id,instance_id,request_id FROM candidates`+scope, lookupArgs...)
	if err != nil {
		return out, err
	}
	queries := []string{}
	ctes := []string{}
	requestArgs := []any{}
	eventArgs := []any{}
	for matched.Next() {
		var sourceID, instanceID, requestID sql.NullString
		if err = matched.Scan(&sourceID, &instanceID, &requestID); err != nil {
			matched.Close()
			return out, err
		}
		name := fmt.Sprintf("events_%d", len(ctes))
		query := name + ` AS MATERIALIZED (SELECT * FROM facts WHERE `
		if requestID.Valid {
			query += "request_id=?"
			requestArgs = append(requestArgs, requestID.String)
		} else {
			query += "request_id IS NULL"
		}
		ctes = append(ctes, query+")")
		eventArgs = append(eventArgs, sourceID, instanceID)
		queries = append(queries, `SELECT `+traceEventColumns+` FROM `+name+` WHERE source_id IS NOT DISTINCT FROM ? AND instance_id IS NOT DISTINCT FROM ?`)
	}
	err = matched.Err()
	matched.Close()
	if err != nil {
		return out, err
	}
	if len(queries) == 0 {
		return out, tx.Commit()
	}
	rows, err := tx.QueryContext(ctx, "WITH "+strings.Join(ctes, ", ")+" "+strings.Join(queries, " UNION ALL ")+` ORDER BY source_id,instance_id,request_id,at_ms,event_id`, append(requestArgs, eventArgs...)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	indices := map[string]int{}
	finals, starts := map[string]int{}, map[string]int{}
	for rows.Next() {
		var source, instance, request, detail, transport string
		var v RequestTraceEvent
		if err := rows.Scan(&source, &instance, &request, &v.EventID, &v.Kind, &v.Stage, &v.AtMS, &v.StartedMS, &v.AttemptID, &v.Provider, &v.Model, &v.UpstreamModel, &v.Endpoint, &v.Stream, &v.Outcome, &v.Status, &v.TerminalKind, &v.FailureReason, &v.ResponseCompleted, &v.DurationMS, &v.DispatchMS, &v.ResponseCreatedMS, &v.FirstTextMS, &v.FirstOutputMS, &detail, &transport, &v.KeyID); err != nil {
			return out, err
		}
		v.Detail, v.Transport = traceDetails(detail), traceTransport(transport)
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
	for i := range out.Data {
		sort.SliceStable(out.Data[i].Events, func(a, b int) bool {
			left, right := out.Data[i].Events[a], out.Data[i].Events[b]
			if left.AtMS != right.AtMS {
				return left.AtMS < right.AtMS
			}
			// Gateway event IDs carry a process-local, zero-padded hex sequence.
			// Sorting by the kind prefix would put dispatch before earlier skips.
			lp, ls := traceEventSequence(left.EventID)
			rp, rs := traceEventSequence(right.EventID)
			if lp != rp {
				return lp < rp
			}
			if lp != "" {
				return ls < rs
			}
			return left.EventID < right.EventID
		})
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	return out, tx.Commit()
}

func traceEventSequence(id string) (string, string) {
	_, tail, ok := strings.Cut(id, "-")
	if !ok {
		return "", ""
	}
	process, sequence, ok := strings.Cut(tail, "-")
	if !ok || (len(process) != 32 && len(process) != 64) || len(sequence) != 16 {
		return "", ""
	}
	for _, c := range process + sequence {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", ""
		}
	}
	return process, sequence
}

func (s *Service) requestTrace(w http.ResponseWriter, r *http.Request) {
	began := time.Now()
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
	scopeMS := float64(time.Since(began).Microseconds()) / 1000
	var instance []string
	if r.URL.Query().Has("instance_id") {
		instance = []string{r.URL.Query().Get("instance_id")}
	}
	lookupStarted := time.Now()
	result, err := s.engine.RequestTrace(r.Context(), id, allowed, r.URL.Query().Get("source_id"), instance...)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "request trace unavailable"})
		return
	}
	result.Import = s.importStatus(allowed)
	lookupMS := float64(time.Since(lookupStarted).Microseconds()) / 1000
	metadataStarted := time.Now()
	// Metadata is optional; its failure must not hide the captured request.
	if s.control != nil {
		if owner, err := s.controlUser(r); err == nil {
			_ = s.enrichTraceChannels(r.Context(), owner, result.Data)
		}
	}
	metadataMS := float64(time.Since(metadataStarted).Microseconds()) / 1000
	body, err := json.Marshal(result)
	if err != nil {
		http.Error(w, "request trace unavailable", 500)
		return
	}
	w.Header().Set("Server-Timing", fmt.Sprintf("scope;dur=%.1f, lookup;dur=%.1f, metadata;dur=%.1f, total;dur=%.1f", scopeMS, lookupMS, metadataMS, float64(time.Since(began).Microseconds())/1000))
	s.writeAnalyticsBody(w, r, body)
}

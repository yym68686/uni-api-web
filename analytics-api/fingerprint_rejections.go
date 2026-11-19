package main

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"time"
)

var legacyFingerprintRequestID = regexp.MustCompile(`^fingerprint-[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// Worker releases before rejection classification kept HTTP statuses but not
// upstream codes. Recover only allowlisted evidence from indexed, exact
// request correlations. This is a bounded, best-effort read: no replay,
// migrations, raw upstream text or dependency on analytics for listing jobs.
func (s *Service) annotateLegacyFingerprintRejections(ctx context.Context, records []fingerprintRecord) {
	if s.engine == nil || s.engine.DB == nil {
		return
	}
	type lookup struct {
		Record   int    `json:"record_index"`
		Sample   int    `json:"sample_index"`
		Source   string `json:"source_id"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Request  string `json:"request_id"`
		Status   int    `json:"status"`
		At       int64  `json:"at_ms"`
	}
	const maxLookups = 384
	cutoff := time.Now().Add(-30 * 24 * time.Hour).Unix()
	var lookups []lookup
	results := map[int]map[string]json.RawMessage{}
	samples := map[int][]map[string]json.RawMessage{}
	var recent []int
	for i, r := range records {
		if r.State == "done" && r.Source != "" && r.Provider != "" &&
			r.UpdatedAtMS >= cutoff*1000 && len(r.Result) > 0 {
			recent = append(recent, i)
		}
	}
	slices.SortFunc(recent, func(i, j int) int {
		if records[i].UpdatedAtMS > records[j].UpdatedAtMS {
			return -1
		}
		if records[i].UpdatedAtMS < records[j].UpdatedAtMS {
			return 1
		}
		return 0
	})
	for _, i := range recent {
		r := records[i]
		if len(lookups) >= maxLookups {
			break
		}
		var result map[string]json.RawMessage
		var fields struct {
			CheckedAt     int64 `json:"checked_at"`
			DetailVersion int   `json:"error_detail_version"`
			Samples       []struct {
				Request string `json:"request_id"`
				State   string `json:"state"`
				Code    string `json:"error_code"`
				Status  int    `json:"http_status"`
			} `json:"samples"`
		}
		if json.Unmarshal(r.Result, &fields) != nil || fields.DetailVersion > 0 || fields.CheckedAt < cutoff || fields.CheckedAt > time.Now().Unix()+60 ||
			len(fields.Samples) > 3 {
			continue
		}
		for j, sample := range fields.Samples {
			if sample.State != "error" || sample.Code != "" ||
				(sample.Status != 403 && sample.Status != 404 && sample.Status != 503) ||
				!legacyFingerprintRequestID.MatchString(sample.Request) || len(lookups) >= maxLookups {
				continue
			}
			if result == nil {
				var rows []map[string]json.RawMessage
				if json.Unmarshal(r.Result, &result) != nil || json.Unmarshal(result["samples"], &rows) != nil {
					break
				}
				results[i], samples[i] = result, rows
			}
			lookups = append(lookups, lookup{i, j, r.Source, r.Provider, r.Model, sample.Request, sample.Status, fields.CheckedAt * 1000})
		}
	}
	if len(lookups) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	// The per-job time fence covers three sequential requests (250s each).
	// Bound the partition range explicitly as well as using the request index.
	minAt, maxAt := lookups[0].At, lookups[0].At
	for _, l := range lookups {
		minAt, maxAt = min(minAt, l.At), max(maxAt, l.At)
	}
	rows, err := s.engine.DB.QueryContext(ctx, `SELECT i.record_index,i.sample_index,e.code
 FROM jsonb_to_recordset(?::jsonb) i(record_index int,sample_index int,source_id text,provider text,model text,request_id text,status int,at_ms bigint)
 CROSS JOIN LATERAL (
   SELECT min(code) AS code FROM (
     SELECT CASE
       WHEN i.status=403 THEN CASE f.trace_detail::jsonb #>> '{error,error_code}'
         WHEN 'GROUP_DELETED' THEN 'group_deleted'
         WHEN 'GROUP_DISABLED' THEN 'group_disabled'
         WHEN 'GROUP_NOT_ALLOWED' THEN 'group_not_allowed'
         WHEN 'INSUFFICIENT_BALANCE' THEN 'insufficient_balance' END
       WHEN i.status=404 AND (f.trace_detail::jsonb #>> '{error,error_type}' = 'model_not_found'
         OR f.trace_detail::jsonb #>> '{error,error_code}' = 'model_not_found') THEN 'model_not_found'
       WHEN i.status=503 AND f.trace_detail::jsonb->>'error_message'='All matching channels are temporarily disabled'
         THEN 'route_temporarily_disabled' END AS code
     FROM facts f
     WHERE f.at_ms BETWEEN ? AND ? AND f.at_ms BETWEEN i.at_ms-900000 AND i.at_ms+5000
       AND f.source_id=i.source_id AND f.request_id=i.request_id AND f.status=i.status
       AND ((i.status IN (403,404) AND f.kind='billing' AND f.provider=i.provider AND f.model=i.model)
         OR (i.status=503 AND f.kind='trace' AND f.stage='gateway_error'))
   ) evidence HAVING count(DISTINCT code)=1
 ) e`, mustJSON(lookups), minAt-900000, maxAt+5000)
	if err != nil {
		return
	}
	defer rows.Close()
	changed := map[int]bool{}
	for rows.Next() {
		var i, j int
		var code string
		if rows.Scan(&i, &j, &code) == nil {
			samples[i][j]["error_code"] = json.RawMessage(mustJSON(code))
			changed[i] = true
		}
	}
	if rows.Err() != nil {
		return
	}
	for i := range changed {
		results[i]["samples"] = json.RawMessage(mustJSON(samples[i]))
		records[i].Result = json.RawMessage(mustJSON(results[i]))
	}
}

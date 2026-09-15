package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type QueryFilter struct {
	Range, Model, Provider, Endpoint, Stream, KeyID string
	Timeseries                                      bool
}
type Summary struct {
	Attempts                  int64    `json:"attempts"`
	Requests                  int64    `json:"requests"`
	Success                   int64    `json:"success"`
	Failed                    int64    `json:"failed"`
	Cancelled                 int64    `json:"cancelled"`
	Skipped                   int64    `json:"skipped"`
	Input                     int64    `json:"input_tokens"`
	Output                    int64    `json:"output_tokens"`
	CacheRead                 int64    `json:"cache_read_tokens"`
	CacheWrite                int64    `json:"cache_write_tokens"`
	CacheWrite1h              int64    `json:"cache_write_1h_tokens"`
	UsageSamples              int64    `json:"usage_samples"`
	CacheSamples              int64    `json:"cache_samples"`
	KnownEstimatedUSD         float64  `json:"known_estimated_usd"`
	PricedSamples             int64    `json:"priced_samples"`
	ActualUSD                 float64  `json:"actual_cost_usd"`
	ActualSamples             int64    `json:"actual_cost_samples"`
	FirstBins                 []int64  `json:"-"`
	DispatchBins              []int64  `json:"-"`
	FirstCount, DispatchCount int64    `json:"-"`
	FirstSum, DispatchSum     float64  `json:"-"`
	LastMS                    int64    `json:"-"`
	LastFirst, LastDispatch   *float64 `json:"-"`
}

func (s *Summary) merge(x Summary) {
	s.Attempts += x.Attempts
	s.Requests += x.Requests
	s.Success += x.Success
	s.Failed += x.Failed
	s.Cancelled += x.Cancelled
	s.Skipped += x.Skipped
	s.Input += x.Input
	s.Output += x.Output
	s.CacheRead += x.CacheRead
	s.CacheWrite += x.CacheWrite
	s.CacheWrite1h += x.CacheWrite1h
	s.UsageSamples += x.UsageSamples
	s.CacheSamples += x.CacheSamples
	s.KnownEstimatedUSD += x.KnownEstimatedUSD
	s.PricedSamples += x.PricedSamples
	s.ActualUSD += x.ActualUSD
	s.ActualSamples += x.ActualSamples
	s.FirstCount += x.FirstCount
	s.DispatchCount += x.DispatchCount
	s.FirstSum += x.FirstSum
	s.DispatchSum += x.DispatchSum
	mergeBins := func(dst *[]int64, src []int64) {
		if len(*dst) == 0 {
			*dst = make([]int64, len(histogramBounds))
		}
		for i, v := range src {
			if i < len(*dst) {
				(*dst)[i] += v
			}
		}
	}
	mergeBins(&s.FirstBins, x.FirstBins)
	mergeBins(&s.DispatchBins, x.DispatchBins)
	if x.LastMS > s.LastMS {
		s.LastMS = x.LastMS
		s.LastFirst = x.LastFirst
		s.LastDispatch = x.LastDispatch
	}
}
func quantile(bins []int64, n int64, q float64) any {
	if n == 0 {
		return nil
	}
	target := int64(math.Ceil(float64(n) * q))
	var sum int64
	for i, v := range bins {
		sum += v
		if sum >= target {
			if i >= len(histogramBounds) || math.IsInf(histogramBounds[i], 1) {
				return nil
			}
			return histogramBounds[i]
		}
	}
	return nil
}
func distribution(bins []int64, n int64, sum float64, last *float64) map[string]any {
	var mean any
	if n > 0 {
		mean = sum / float64(n)
	}
	return map[string]any{"sample_count": n, "mean_ms": mean, "p50_ms": quantile(bins, n, .5), "p95_ms": quantile(bins, n, .95), "last_ms": last}
}
func (s Summary) JSON() map[string]any {
	raw, _ := json.Marshal(s)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	den := s.Success + s.Failed
	var rate, cache, estimate any
	if den > 0 {
		rate = float64(s.Success) / float64(den)
	}
	if s.CacheSamples > 0 && s.Input > 0 {
		cache = float64(s.CacheRead) / float64(s.Input)
	}
	if s.PricedSamples > 0 {
		estimate = s.KnownEstimatedUSD
	}
	out["started"] = s.Attempts
	out["success_rate_denominator"] = den
	out["success_rate"] = rate
	out["cache_rate"] = cache
	out["estimated_cost_usd"] = estimate
	out["cost_basis"] = "current_model_prices"
	out["first_output"] = distribution(s.FirstBins, s.FirstCount, s.FirstSum, s.LastFirst)
	out["request_to_dispatch"] = distribution(s.DispatchBins, s.DispatchCount, s.DispatchSum, s.LastDispatch)
	out["last_success_at"] = nil
	out["inflight"] = nil
	out["client_cancelled"] = s.Cancelled
	out["hedge_cancelled"] = 0
	return out
}

type AnalyticChannel struct {
	Provider      string           `json:"provider"`
	Model         string           `json:"model"`
	UpstreamModel string           `json:"upstream_model"`
	Endpoint      string           `json:"endpoint"`
	Stream        *bool            `json:"stream"`
	Stats         map[string]any   `json:"stats"`
	Points        []map[string]any `json:"points,omitempty"`
}
type QueryResult struct {
	Import        map[string]any    `json:"import"`
	Coverage      string            `json:"coverage"`
	From          int64             `json:"from"`
	To            int64             `json:"to"`
	Range         string            `json:"range"`
	Timezone      string            `json:"timezone"`
	Data          []AnalyticChannel `json:"data"`
	Total         map[string]any    `json:"total"`
	Models        []map[string]any  `json:"models"`
	CollectedFrom *int64            `json:"collection_started_at"`
	GeneratedAt   int64             `json:"generated_at"`
	Revision      uint64            `json:"revision"`
	DurationMS    float64           `json:"query_ms"`
}

func (e *Engine) Query(ctx context.Context, f QueryFilter) (QueryResult, error) {
	began := time.Now()
	now := time.Now().UTC()
	start, err := rangeStart(f.Range, now, e.Location)
	if err != nil {
		return QueryResult{}, err
	}
	// Windows have one-minute resolution. Use full local-day rollups between the
	// boundaries and minute rows on partial days, never both for the same period.
	startMS := start.UnixMilli() / 60000 * 60000
	localStart := time.UnixMilli(startMS).In(e.Location)
	firstDay := time.Date(localStart.Year(), localStart.Month(), localStart.Day(), 0, 0, 0, 0, e.Location)
	if firstDay.UnixMilli() < startMS {
		firstDay = firstDay.AddDate(0, 0, 1)
	}
	localNow := now.In(e.Location)
	lastDay := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, e.Location)
	lo, hi := firstDay.UnixMilli(), lastDay.UnixMilli()
	if hi < lo {
		hi = lo
	}
	where := []string{`((level='day' AND period_ms>=? AND period_ms<?) OR (level='minute' AND period_ms>=? AND period_ms<=? AND NOT(period_ms>=? AND period_ms<?)))`}
	args := []any{lo, hi, startMS, now.UnixMilli(), lo, hi}
	for _, entry := range [][2]string{{"provider", f.Provider}, {"model", f.Model}, {"endpoint", f.Endpoint}, {"key_id", f.KeyID}} {
		if entry[1] != "" && entry[1] != "all" {
			where = append(where, entry[0]+"=?")
			args = append(args, entry[1])
		}
	}
	if f.Stream == "true" || f.Stream == "false" {
		where = append(where, "stream=?")
		args = append(args, f.Stream == "true")
	}
	q := `SELECT kind,provider,model,upstream_model,endpoint,stream,outcome,sum(n)::BIGINT,sum(input_tokens)::BIGINT,sum(output_tokens)::BIGINT,sum(cache_read_tokens)::BIGINT,sum(cache_write_tokens)::BIGINT,sum(cache_write_1h_tokens)::BIGINT,sum(usage_samples)::BIGINT,sum(cache_samples)::BIGINT,sum(actual_cost_usd),sum(actual_cost_samples)::BIGINT,to_json(` + mergeHistogramSQL("first_bins") + `),to_json(` + mergeHistogramSQL("dispatch_bins") + `),sum(first_count)::BIGINT,sum(dispatch_count)::BIGINT,sum(first_sum),sum(dispatch_sum),max(last_ms),arg_max(last_first,last_ms),arg_max(last_dispatch,last_ms) FROM rollups WHERE ` + strings.Join(where, " AND ") + ` GROUP BY kind,provider,model,upstream_model,endpoint,stream,outcome`
	prices, err := e.Prices(ctx)
	if err != nil {
		return QueryResult{}, err
	}
	rows, err := e.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()
	priceMap := map[string]Price{}
	for _, p := range prices {
		priceMap[p.Model] = p
	}
	channels := map[string]*Summary{}
	identities := map[string][3]string{}
	models := map[string]*Summary{}
	var total Summary
	for rows.Next() {
		var kind, provider, model, upstream, endpoint, outcome string
		var stream bool
		var firstValue, dispatchValue any
		var n int64
		var s Summary
		if err = rows.Scan(&kind, &provider, &model, &upstream, &endpoint, &stream, &outcome, &n, &s.Input, &s.Output, &s.CacheRead, &s.CacheWrite, &s.CacheWrite1h, &s.UsageSamples, &s.CacheSamples, &s.ActualUSD, &s.ActualSamples, &firstValue, &dispatchValue, &s.FirstCount, &s.DispatchCount, &s.FirstSum, &s.DispatchSum, &s.LastMS, &s.LastFirst, &s.LastDispatch); err != nil {
			return QueryResult{}, err
		}
		s.FirstBins = histogramValues(firstValue)
		s.DispatchBins = histogramValues(dispatchValue)
		if p, ok := priceMap[model]; ok && p.Verified && s.UsageSamples > 0 {
			ordinary := max(0, s.Input-s.CacheRead-s.CacheWrite)
			write5 := max(0, s.CacheWrite-s.CacheWrite1h)
			s.KnownEstimatedUSD = (float64(ordinary)*p.Input + float64(s.Output)*p.Output + float64(s.CacheRead)*p.CacheRead + float64(write5)*p.CacheWrite + float64(s.CacheWrite1h)*p.CacheWrite1h) / 1e6
			s.PricedSamples = s.UsageSamples
		}
		if kind == "request" {
			s.Requests = n
			s.Success = 0
			s.Failed = 0
			switch outcome {
			case "success", "completed", "incomplete":
				s.Success = n
			default:
				s.Failed = n
			}
			// A request fact is attributed to the winning channel. Keep it on the
			// channel row so token, cache and cost fields remain actionable while
			// attempts continue to count retries independently.
			key := provider + "\x00" + model + "\x00" + upstream
			if channels[key] == nil {
				channels[key] = &Summary{}
				identities[key] = [3]string{provider, model, upstream}
			}
			channelSummary := Summary{Requests: n, Input: s.Input, Output: s.Output, CacheRead: s.CacheRead, CacheWrite: s.CacheWrite, CacheWrite1h: s.CacheWrite1h, UsageSamples: s.UsageSamples, CacheSamples: s.CacheSamples, ActualUSD: s.ActualUSD, ActualSamples: s.ActualSamples, KnownEstimatedUSD: s.KnownEstimatedUSD, PricedSamples: s.PricedSamples}
			channels[key].merge(channelSummary)
			total.merge(s)
			if models[model] == nil {
				models[model] = &Summary{}
			}
			models[model].merge(s)
			continue
		}
		key := provider + "\x00" + model + "\x00" + upstream
		if channels[key] == nil {
			channels[key] = &Summary{}
			identities[key] = [3]string{provider, model, upstream}
		}
		if kind == "attempt" {
			switch outcome {
			case "success", "completed", "incomplete":
				s.Attempts = n
				s.Success = n
			case "skipped":
				s.Skipped = n
			case "cancelled", "client_cancelled", "hedge_cancelled":
				s.Attempts = n
				s.Cancelled = n
			default:
				s.Attempts = n
				s.Failed = n
			}
		}
		channels[key].merge(s)
	}
	if err = rows.Err(); err != nil {
		return QueryResult{}, err
	}
	out := QueryResult{From: startMS / 1000, To: now.Unix(), Range: f.Range, Timezone: e.cfg.Timezone, Data: []AnalyticChannel{}, Models: []map[string]any{}, GeneratedAt: now.Unix(), Revision: e.Revision.Load()}
	keys := make([]string, 0, len(channels))
	for k := range channels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var attempts Summary
	for _, k := range keys {
		id := identities[k]
		var channelStream *bool
		if f.Stream == "true" || f.Stream == "false" {
			value := f.Stream == "true"
			channelStream = &value
		}
		endpoint := f.Endpoint
		if endpoint == "" {
			endpoint = "all"
		}
		out.Data = append(out.Data, AnalyticChannel{Provider: id[0], Model: id[1], UpstreamModel: id[2], Endpoint: endpoint, Stream: channelStream, Stats: channels[k].JSON()})
		attempts.merge(*channels[k])
	}
	// Usage totals represent client requests; channel attempt totals separately
	// include failed/hedged upstream work. Never add the two usage populations.
	out.Total = total.JSON()
	out.Total["attempts"] = attempts.Attempts
	out.Total["attempt_success"] = attempts.Success
	out.Total["attempt_failed"] = attempts.Failed
	names := []string{}
	for m := range models {
		names = append(names, m)
	}
	sort.Strings(names)
	for _, m := range names {
		s := models[m].JSON()
		s["model"] = m
		out.Models = append(out.Models, s)
	}
	var firstMS *int64
	if err = e.DB.QueryRowContext(ctx, "SELECT min(at_ms) FROM facts").Scan(&firstMS); err != nil {
		return QueryResult{}, fmt.Errorf("read coverage: %w", err)
	}
	if firstMS != nil {
		seconds := *firstMS / 1000
		out.CollectedFrom = &seconds
	}
	out.Coverage = "partial"
	if out.CollectedFrom != nil && *out.CollectedFrom <= out.From {
		out.Coverage = "available_history"
	}
	out.DurationMS = float64(time.Since(began).Microseconds()) / 1000
	if f.Timeseries {
		if err := e.attachTimeseries(ctx, &out, f, startMS, now.UnixMilli()); err != nil {
			return QueryResult{}, err
		}
	}
	return out, nil
}

// attachTimeseries reads pre-aggregated rollups only. Short windows use minute
// buckets; longer windows use daily buckets so chart requests remain bounded
// even when the fact table spans a year or more.
func (e *Engine) attachTimeseries(ctx context.Context, result *QueryResult, f QueryFilter, startMS, endMS int64) error {
	level := "minute"
	if endMS-startMS > 48*60*60*1000 {
		level = "day"
	}
	where := []string{"level=?", "period_ms>=?", "period_ms<=?", "kind='attempt'"}
	args := []any{level, startMS / 60000 * 60000, endMS}
	for _, entry := range [][2]string{{"provider", f.Provider}, {"model", f.Model}, {"endpoint", f.Endpoint}, {"key_id", f.KeyID}} {
		if entry[1] != "" && entry[1] != "all" {
			where = append(where, entry[0]+"=?")
			args = append(args, entry[1])
		}
	}
	if f.Stream == "true" || f.Stream == "false" {
		where = append(where, "stream=?")
		args = append(args, f.Stream == "true")
	}
	rows, err := e.DB.QueryContext(ctx, `SELECT period_ms,provider,model,upstream_model,outcome,sum(n)::BIGINT FROM rollups WHERE `+strings.Join(where, " AND ")+` GROUP BY period_ms,provider,model,upstream_model,outcome ORDER BY period_ms`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	points := map[string]map[int64]map[string]any{}
	for rows.Next() {
		var period int64
		var provider, model, upstream, outcome string
		var n int64
		if err := rows.Scan(&period, &provider, &model, &upstream, &outcome, &n); err != nil {
			return err
		}
		key := provider + "\x00" + model + "\x00" + upstream
		if points[key] == nil {
			points[key] = map[int64]map[string]any{}
		}
		point := points[key][period]
		if point == nil {
			point = map[string]any{"timestamp": period / 1000, "success": int64(0), "failed": int64(0), "covered": true}
			points[key][period] = point
		}
		if outcome == "success" || outcome == "completed" || outcome == "incomplete" {
			point["success"] = point["success"].(int64) + n
		} else {
			point["failed"] = point["failed"].(int64) + n
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range result.Data {
		key := result.Data[i].Provider + "\x00" + result.Data[i].Model + "\x00" + result.Data[i].UpstreamModel
		buckets := points[key]
		periods := make([]int64, 0, len(buckets))
		for period := range buckets {
			periods = append(periods, period)
		}
		sort.Slice(periods, func(i, j int) bool { return periods[i] < periods[j] })
		for _, period := range periods {
			result.Data[i].Points = append(result.Data[i].Points, buckets[period])
		}
	}
	return nil
}

func histogramValues(value any) []int64 {
	var out []int64
	switch values := value.(type) {
	case []int64:
		return values
	case []interface{}:
		for _, item := range values {
			switch n := item.(type) {
			case int64:
				out = append(out, n)
			case int32:
				out = append(out, int64(n))
			case float64:
				out = append(out, int64(n))
			}
		}
	case string:
		_ = json.Unmarshal([]byte(values), &out)
	}
	return out
}

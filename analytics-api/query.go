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
	SourceIDs                                                                []string
	Range, Model, UpstreamModel, Provider, Endpoint, Stream, KeyID, SourceID string
	Timeseries                                                               bool
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
	CreatedBins, TextBins     []int64  `json:"-"`
	CreatedCount, TextCount   int64    `json:"-"`
	CreatedSum, TextSum       float64  `json:"-"`
	LastCreated, LastText     *float64 `json:"-"`
	LastCreatedMS, LastTextMS int64    `json:"-"`
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
	s.CreatedCount += x.CreatedCount
	s.TextCount += x.TextCount
	s.CreatedSum += x.CreatedSum
	s.TextSum += x.TextSum
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
	mergeBins(&s.CreatedBins, x.CreatedBins)
	mergeBins(&s.TextBins, x.TextBins)
	if x.LastCreated != nil && x.LastCreatedMS > s.LastCreatedMS {
		s.LastCreated = x.LastCreated
		s.LastCreatedMS = x.LastCreatedMS
	}
	if x.LastText != nil && x.LastTextMS > s.LastTextMS {
		s.LastText = x.LastText
		s.LastTextMS = x.LastTextMS
	}
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
	out["response_created"] = distribution(s.CreatedBins, s.CreatedCount, s.CreatedSum, s.LastCreated)
	out["first_text"] = distribution(s.TextBins, s.TextCount, s.TextSum, s.LastText)
	out["request_to_dispatch"] = distribution(s.DispatchBins, s.DispatchCount, s.DispatchSum, s.LastDispatch)
	out["last_success_at"] = nil
	out["inflight"] = nil
	out["client_cancelled"] = s.Cancelled
	out["hedge_cancelled"] = 0
	return out
}

type AnalyticChannel struct {
	SourceID      string           `json:"source_id,omitempty"`
	Provider      string           `json:"provider"`
	Model         string           `json:"model"`
	UpstreamModel string           `json:"upstream_model"`
	Endpoint      string           `json:"endpoint"`
	Stream        *bool            `json:"stream"`
	Stats         map[string]any   `json:"stats"`
	Points        []map[string]any `json:"points,omitempty"`
}
type SourceFreshness struct {
	SourceID     string `json:"source_id"`
	LatestFactAt int64  `json:"latest_fact_at"`
}

type QueryResult struct {
	SourceFreshness []SourceFreshness `json:"source_freshness"`
	Import          map[string]any    `json:"import"`
	Coverage        string            `json:"coverage"`
	From            int64             `json:"from"`
	To              int64             `json:"to"`
	Range           string            `json:"range"`
	Timezone        string            `json:"timezone"`
	Data            []AnalyticChannel `json:"data"`
	Total           map[string]any    `json:"total"`
	Models          []map[string]any  `json:"models"`
	CollectedFrom   *int64            `json:"collection_started_at"`
	GeneratedAt     int64             `json:"generated_at"`
	Revision        uint64            `json:"revision"`
	DurationMS      float64           `json:"query_ms"`
	BucketSeconds   int64             `json:"bucket_seconds,omitempty"`
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
	for _, entry := range [][2]string{{"provider", f.Provider}, {"model", f.Model}, {"upstream_model", f.UpstreamModel}, {"endpoint", f.Endpoint}, {"key_id", f.KeyID}} {
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
	q := `SELECT source_id,kind,provider,model,upstream_model,endpoint,stream,outcome,sum(n)::BIGINT,sum(input_tokens)::BIGINT,sum(output_tokens)::BIGINT,sum(cache_read_tokens)::BIGINT,sum(cache_write_tokens)::BIGINT,sum(cache_write_1h_tokens)::BIGINT,sum(usage_samples)::BIGINT,sum(cache_samples)::BIGINT,sum(actual_cost_usd),sum(actual_cost_samples)::BIGINT,to_json(` + mergeHistogramSQL("first_bins") + `),to_json(` + mergeHistogramSQL("dispatch_bins") + `),sum(first_count)::BIGINT,sum(dispatch_count)::BIGINT,sum(first_sum),sum(dispatch_sum),max(last_ms),arg_max(last_first,last_ms),arg_max(last_dispatch,last_ms),to_json(` + mergeHistogramSQL("created_bins") + `),coalesce(sum(created_count),0)::BIGINT,coalesce(sum(created_sum),0),arg_max(last_created,last_ms),to_json(` + mergeHistogramSQL("text_bins") + `),coalesce(sum(text_count),0)::BIGINT,coalesce(sum(text_sum),0),arg_max(last_text,last_ms),coalesce(max(last_ms) FILTER (WHERE last_created IS NOT NULL),0),coalesce(max(last_ms) FILTER (WHERE last_text IS NOT NULL),0) FROM rollups WHERE ` + strings.Join(where, " AND ") + ` GROUP BY source_id,kind,provider,model,upstream_model,endpoint,stream,outcome`
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
	identities := map[string][4]string{}
	models := map[string]*Summary{}
	var total Summary
	for rows.Next() {
		var sourceID, kind, provider, model, upstream, endpoint, outcome string
		var stream bool
		var firstValue, dispatchValue, createdValue, textValue any
		var n int64
		var s Summary
		if err = rows.Scan(&sourceID, &kind, &provider, &model, &upstream, &endpoint, &stream, &outcome, &n, &s.Input, &s.Output, &s.CacheRead, &s.CacheWrite, &s.CacheWrite1h, &s.UsageSamples, &s.CacheSamples, &s.ActualUSD, &s.ActualSamples, &firstValue, &dispatchValue, &s.FirstCount, &s.DispatchCount, &s.FirstSum, &s.DispatchSum, &s.LastMS, &s.LastFirst, &s.LastDispatch, &createdValue, &s.CreatedCount, &s.CreatedSum, &s.LastCreated, &textValue, &s.TextCount, &s.TextSum, &s.LastText, &s.LastCreatedMS, &s.LastTextMS); err != nil {
			return QueryResult{}, err
		}
		s.FirstBins = histogramValues(firstValue)
		s.DispatchBins = histogramValues(dispatchValue)
		s.CreatedBins = histogramValues(createdValue)
		s.TextBins = histogramValues(textValue)
		if p, ok := priceMap[canonicalPriceModel(model)]; ok && p.Verified && s.UsageSamples > 0 {
			ordinary := max(0, s.Input-s.CacheRead-s.CacheWrite)
			write5 := max(0, s.CacheWrite-s.CacheWrite1h)
			writeCost := 0.0
			if p.chargesCacheWrite() {
				writeCost = float64(write5)*p.CacheWrite + float64(s.CacheWrite1h)*p.CacheWrite1h
			}
			// Disabled writes contribute no cost and are not charged again as
			// ordinary input. Token counts and cache-read charges are unchanged.
			s.KnownEstimatedUSD = (float64(ordinary)*p.Input + float64(s.Output)*p.Output + float64(s.CacheRead)*p.CacheRead + writeCost) / 1e6
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
			key := sourceID + "\x00" + provider + "\x00" + model + "\x00" + upstream
			if channels[key] == nil {
				channels[key] = &Summary{}
				identities[key] = [4]string{sourceID, provider, model, upstream}
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
		key := sourceID + "\x00" + provider + "\x00" + model + "\x00" + upstream
		if channels[key] == nil {
			channels[key] = &Summary{}
			identities[key] = [4]string{sourceID, provider, model, upstream}
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
		stats := channels[k].JSON()
		price, found := priceMap[canonicalPriceModel(id[2])]
		if !found {
			price = Price{Model: id[2]}
		}
		stats["sale_percent"] = price.salePercent()
		out.Data = append(out.Data, AnalyticChannel{SourceID: id[0], Provider: id[1], Model: id[2], UpstreamModel: id[3], Endpoint: endpoint, Stream: channelStream, Stats: stats})
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
	// A successful S3 scan does not prove that a source is still exporting.
	// Expose actual event freshness separately, including when this window is empty.
	out.SourceFreshness = []SourceFreshness{}
	coverageWhere, coverageArgs := sourceWhere([]string{"true"}, nil, f)
	coverageRows, err := e.DB.QueryContext(ctx, "SELECT source_id,min(at_ms),max(at_ms) FROM facts WHERE "+strings.Join(coverageWhere, " AND ")+" GROUP BY source_id", coverageArgs...)
	if err != nil {
		return QueryResult{}, fmt.Errorf("read source coverage: %w", err)
	}
	for coverageRows.Next() {
		var source string
		var first, last int64
		if err = coverageRows.Scan(&source, &first, &last); err != nil {
			coverageRows.Close()
			return QueryResult{}, err
		}
		seconds := first / 1000
		if out.CollectedFrom == nil || seconds < *out.CollectedFrom {
			out.CollectedFrom = &seconds
		}
		out.SourceFreshness = append(out.SourceFreshness, SourceFreshness{SourceID: source, LatestFactAt: last / 1000})
	}
	err = coverageRows.Err()
	coverageRows.Close()
	if err != nil {
		return QueryResult{}, err
	}
	out.Coverage = "partial"
	if out.CollectedFrom != nil && *out.CollectedFrom <= out.From {
		out.Coverage = "available_history"
	}
	if f.Timeseries {
		if err := e.attachTimeseries(ctx, &out, f, startMS, now.UnixMilli()); err != nil {
			return QueryResult{}, err
		}
	}
	out.DurationMS = float64(time.Since(began).Microseconds()) / 1000
	return out, nil
}

// attachTimeseries uses daily rollups for complete days and minute rollups
// on the boundaries. Usage belongs to request facts, never retried attempts.
func (e *Engine) attachTimeseries(ctx context.Context, result *QueryResult, f QueryFilter, startMS, endMS int64) error {
	// Use observed coverage for an all-history window rather than empty years.
	chartStart := startMS
	if result.CollectedFrom != nil {
		chartStart = max(chartStart, *result.CollectedFrom*1000/60000*60000)
	}
	bucket := int64(60000)
	span := max(int64(1), endMS-chartStart)
	for _, size := range []int64{60000, 300000, 900000, 3600000, 21600000, 86400000, 604800000, 2592000000} {
		bucket = size
		if span/size <= 180 {
			break
		}
	}
	if span/bucket > 180 {
		bucket = ((span/180 + 86399999) / 86400000) * 86400000
	}
	result.BucketSeconds = bucket / 1000
	where := []string{"level='minute'", "period_ms>=?", "period_ms<=?"}
	args := []any{startMS, endMS}
	if endMS-startMS > 48*60*60*1000 {
		localStart := time.UnixMilli(startMS).In(e.Location)
		firstDay := time.Date(localStart.Year(), localStart.Month(), localStart.Day(), 0, 0, 0, 0, e.Location)
		if firstDay.UnixMilli() < startMS {
			firstDay = firstDay.AddDate(0, 0, 1)
		}
		localEnd := time.UnixMilli(endMS).In(e.Location)
		lastDay := time.Date(localEnd.Year(), localEnd.Month(), localEnd.Day(), 0, 0, 0, 0, e.Location)
		lo, hi := firstDay.UnixMilli(), lastDay.UnixMilli()
		if hi < lo {
			hi = lo
		}
		where = []string{`((level='day' AND period_ms>=? AND period_ms<?) OR (level='minute' AND period_ms>=? AND period_ms<=? AND NOT(period_ms>=? AND period_ms<?)))`}
		args = []any{lo, hi, startMS, endMS, lo, hi}
		// Daily rollups cannot be split into sub-day chart buckets.
		bucket = max(bucket, 86400000)
		result.BucketSeconds = bucket / 1000
	}
	for _, entry := range [][2]string{{"provider", f.Provider}, {"model", f.Model}, {"upstream_model", f.UpstreamModel}, {"endpoint", f.Endpoint}, {"key_id", f.KeyID}} {
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
	// Offset the buckets to local midnight, matching the stored daily rollups.
	_, offset := time.UnixMilli(endMS).In(e.Location).Zone()
	bucketSQL := fmt.Sprintf("(floor((period_ms+%d)::DOUBLE/%d)*%d-%d)::BIGINT", int64(offset)*1000, bucket, bucket, int64(offset)*1000)
	rows, err := e.DB.QueryContext(ctx, `SELECT `+bucketSQL+` AS bucket,
      coalesce(sum(CASE WHEN kind='attempt' AND outcome IN ('success','completed','incomplete') THEN n ELSE 0 END),0)::BIGINT,
      coalesce(sum(CASE WHEN kind='attempt' AND outcome NOT IN ('success','completed','incomplete','cancelled','client_cancelled','hedge_cancelled','skipped') THEN n ELSE 0 END),0)::BIGINT,
      coalesce(sum(CASE WHEN kind='request' THEN input_tokens ELSE 0 END),0)::BIGINT,
      coalesce(sum(CASE WHEN kind='request' THEN cache_read_tokens ELSE 0 END),0)::BIGINT,
      coalesce(sum(CASE WHEN kind='request' THEN cache_samples ELSE 0 END),0)::BIGINT
      FROM rollups WHERE `+strings.Join(where, " AND ")+` GROUP BY bucket ORDER BY bucket`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var period, success, failed, input, cached, samples int64
		if err = rows.Scan(&period, &success, &failed, &input, &cached, &samples); err != nil {
			return err
		}
		var rate any
		if samples > 0 && input > 0 {
			rate = float64(cached) / float64(input)
		}
		point := map[string]any{"timestamp": max(period, startMS) / 1000, "bucket_start": period / 1000, "bucket_end": min(period+bucket, endMS) / 1000, "success": success, "failed": failed, "input_tokens": input, "cache_read_tokens": cached, "cache_samples": samples, "cache_rate": rate, "covered": true}
		// The existing overview chart consumes one aggregate series. Drawer
		// queries explicitly scope this same series to the clicked channel.
		if len(result.Data) > 0 {
			result.Data[0].Points = append(result.Data[0].Points, point)
		}
	}
	return rows.Err()
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

func sourceWhere(where []string, args []any, f QueryFilter) ([]string, []any) {
	if f.SourceID != "" && f.SourceID != "all" {
		where = append(where, "source_id=?")
		args = append(args, f.SourceID)
	}
	if f.SourceIDs != nil {
		if len(f.SourceIDs) == 0 {
			where = append(where, "false")
		} else {
			marks := make([]string, len(f.SourceIDs))
			for i, id := range f.SourceIDs {
				marks[i] = "?"
				args = append(args, id)
			}
			where = append(where, "source_id IN ("+strings.Join(marks, ",")+")")
		}
	}
	return where, args
}

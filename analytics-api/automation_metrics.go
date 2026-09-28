package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"
)

type automationMetric struct {
	Provider                                  string
	Upstream                                  string
	Latency, Success, Cache, Quality          float64
	SuccessN, CacheN, LatencyN, QualityN      int
	LatencyCI, SuccessCI, CacheCI, QualityCI  [2]float64
	Latest                                    int64
	QualityLatest                             int64
	SuccessLatest, CacheLatest, LatencyLatest int64
	Eligible                                  bool
	Reason                                    string
}

func confidenceZ(level float64) float64 { return math.Sqrt2 * math.Erfinv(level) }
func wilson(passed float64, n int, z float64) (float64, float64) {
	if n < 1 {
		return 0, 1
	}
	p := passed / float64(n)
	d := 1 + z*z/float64(n)
	c := (p + z*z/(2*float64(n))) / d
	h := z * math.Sqrt((p*(1-p)+z*z/(4*float64(n)))/float64(n)) / d
	return math.Max(0, c-h), math.Min(1, c+h)
}
func interval(passed float64, n int, z float64) [2]float64 {
	lo, hi := wilson(passed, n, z)
	return [2]float64{lo, hi}
}
func sampleQuantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	return xs[max(0, min(len(xs)-1, int(math.Ceil(q*float64(len(xs))))-1))]
}

// A distribution-free quantile interval uses binomial order-statistic ranks.
// An unbounded tail is represented by a missing sample gate rather than by p50.
func quantileInterval(xs []float64, q, z float64) (float64, [2]float64, bool) {
	if len(xs) == 0 {
		return 0, [2]float64{}, false
	}
	sort.Float64s(xs)
	n := float64(len(xs))
	h := z * math.Sqrt(n*q*(1-q))
	lo := int(math.Floor(n*q - h))
	hi := int(math.Ceil(n*q + h))
	v := sampleQuantile(xs, q)
	if lo < 1 || hi > len(xs) {
		return v, [2]float64{}, false
	}
	return v, [2]float64{xs[lo-1], xs[hi-1]}, true
}

type cacheBlock struct{ Input, Cached float64 }

// Resample paired one-minute token totals, not fabricated Bernoulli cache hits.
// This retains request-size weighting and within-minute correlation.
func cacheInterval(blocks []cacheBlock, level float64) (float64, [2]float64, bool) {
	var total, hit float64
	for _, b := range blocks {
		total += b.Input
		hit += b.Cached
	}
	if total <= 0 || len(blocks) < 2 {
		return 0, [2]float64{}, false
	}
	rng := rand.New(rand.NewSource(1))
	samples := make([]float64, 400)
	for i := range samples {
		var a, b float64
		for range blocks {
			x := blocks[rng.Intn(len(blocks))]
			a += x.Input
			b += x.Cached
		}
		samples[i] = b / a
	}
	sort.Float64s(samples)
	tail := (1 - level) / 2
	return hit / total, [2]float64{sampleQuantile(samples, tail), sampleQuantile(samples, 1-tail)}, true
}
func metricEnabled(p AutomationPolicy, name string) bool {
	for _, m := range p.Metrics {
		if m == name {
			return true
		}
	}
	return false
}
func (s *Service) automationMetrics(ctx context.Context, t AutomationTask, catalog []automationChannel) (map[string]automationMetric, error) {
	now := time.Now()
	start, e := rangeStart(t.Range, now, s.engine.Location)
	if e != nil {
		return nil, e
	}
	p := t.Policy

	// Aggregate in DuckDB, which can spill within its configured memory budget.
	// Never copy the full fact window or all latency samples into Go memory.
	type samples struct {
		m            automationMetric
		blocks       map[int64]cacheBlock
		success      int
		latencyValid bool
	}
	all := map[string]*samples{}
	for _, c := range catalog {
		all[c.Provider] = &samples{m: automationMetric{Provider: c.Provider, Upstream: c.Upstream, Eligible: c.Eligible}, blocks: map[int64]cacheBlock{}}
	}
	tx, e := s.engine.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	scope := ` FROM facts WHERE source_id=? AND key_id=? AND model=? AND endpoint=? AND stream=? AND at_ms>=? AND at_ms<=? `
	args := []any{t.SourceID, t.KeyID, t.Model, p.Endpoint, p.Stream == "true", start.UnixMilli(), now.UnixMilli()}
	rows, e := tx.QueryContext(ctx, `SELECT provider,upstream_model,max(at_ms),count(*) FILTER(WHERE kind='attempt' AND outcome NOT IN ('cancelled','client_cancelled','hedge_cancelled','skipped')),count(*) FILTER(WHERE kind='attempt' AND outcome IN ('success','completed')),coalesce(max(at_ms) FILTER(WHERE kind='attempt' AND outcome NOT IN ('cancelled','client_cancelled','hedge_cancelled','skipped')),0)`+scope+`GROUP BY provider,upstream_model`, args...)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var provider, upstream string
		var latest, successLatest int64
		var n, success int
		if e = rows.Scan(&provider, &upstream, &latest, &n, &success, &successLatest); e != nil {
			break
		}
		if a := all[provider]; a != nil && a.m.Upstream == upstream {
			a.m.Latest = latest
			a.m.SuccessN = n
			a.success = success
			a.m.SuccessLatest = successLatest
		}
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	z := confidenceZ(p.ConfidenceLevel)
	// Exact order-statistic ranks, including the original binomial CI endpoints.
	latencyArgs := append(append([]any{}, args...), p.LatencyQuantile, z)
	query := `WITH ranked AS (SELECT provider,upstream_model,at_ms,response_created_ms AS latency,row_number() OVER(PARTITION BY provider,upstream_model ORDER BY response_created_ms) AS rank,count(*) OVER(PARTITION BY provider,upstream_model) AS n` + scope + `AND kind='attempt' AND response_created_ms>=0 AND isfinite(response_created_ms)), parameters AS (SELECT ?::DOUBLE AS q,?::DOUBLE AS z), bounds AS (SELECT *,greatest(1,ceil(q*n)) AS mid,floor(q*n-z*sqrt(n*q*(1-q))) AS lo,ceil(q*n+z*sqrt(n*q*(1-q))) AS hi FROM ranked CROSS JOIN parameters) SELECT provider,upstream_model,max(n),max(at_ms),max(latency) FILTER(WHERE rank=mid),max(latency) FILTER(WHERE rank=lo),max(latency) FILTER(WHERE rank=hi) FROM bounds GROUP BY provider,upstream_model`
	rows, e = tx.QueryContext(ctx, query, latencyArgs...)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var provider, upstream string
		var n int
		var latest int64
		var v, lo, hi sql.NullFloat64
		if e = rows.Scan(&provider, &upstream, &n, &latest, &v, &lo, &hi); e != nil {
			break
		}
		if a := all[provider]; a != nil && a.m.Upstream == upstream {
			a.m.LatencyN = n
			a.m.LatencyLatest = latest
			a.m.Latency = v.Float64
			a.m.LatencyCI = [2]float64{lo.Float64, hi.Float64}
			a.latencyValid = lo.Valid && hi.Valid
			if !a.latencyValid {
				a.m.LatencyCI = [2]float64{}
			}
		}
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	rows, e = tx.QueryContext(ctx, `SELECT provider,upstream_model,at_ms//60000,count(*),max(at_ms),sum(input_tokens::DOUBLE),sum(cache_read_tokens::DOUBLE)`+scope+`AND kind='request' AND input_tokens>0 AND cache_read_tokens>=0 AND cache_read_tokens<=input_tokens GROUP BY provider,upstream_model,at_ms//60000`, args...)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var provider, upstream string
		var minute, latest int64
		var n int
		var block cacheBlock
		if e = rows.Scan(&provider, &upstream, &minute, &n, &latest, &block.Input, &block.Cached); e != nil {
			break
		}
		if a := all[provider]; a != nil && a.m.Upstream == upstream {
			a.m.CacheN += n
			a.m.CacheLatest = max(a.m.CacheLatest, latest)
			a.blocks[minute] = block
		}
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	out := map[string]automationMetric{}
	bindings := map[string]subChannelRef{}
	if t.Model == checkModel && metricEnabled(p, "quality") {
		refs, err := s.control.subChannelRefs(ctx, t.Owner)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			bindings[subProviderName(ref.Account, ref.Group, t.KeyID)] = ref
		}
		configured, err := s.configuredBindings(ctx, t.Owner)
		if err != nil {
			return nil, err
		}
		for _, c := range configured {
			if c.SourceID == t.SourceID && c.AccountID != "" && c.GroupID > 0 {
				bindings[c.Provider] = subChannelRef{Account: c.AccountID, Group: c.GroupID}
			}
		}
	}
	for name, a := range all {
		m := a.m
		valid := a.latencyValid
		if !valid && metricEnabled(p, "latency") {
			m.Reason = "首字分位数的有效样本不足以计算置信区间"
		}
		if m.SuccessN > 0 {
			m.Success = float64(a.success) / float64(m.SuccessN)
		}
		m.SuccessCI = interval(float64(a.success), m.SuccessN, z)
		blocks := make([]cacheBlock, 0, len(a.blocks))
		times := make([]int64, 0, len(a.blocks))
		for at := range a.blocks {
			times = append(times, at)
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		for _, at := range times {
			blocks = append(blocks, a.blocks[at])
		}
		m.Cache, m.CacheCI, valid = cacheInterval(blocks, p.ConfidenceLevel)
		if !valid && metricEnabled(p, "cache") {
			m.Reason = "缓存率至少需要两个有效分钟样本"
		}
		if t.Model == checkModel && metricEnabled(p, "quality") {
			var passed int
			ref := bindings[name]
			e = s.control.db.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE verdict='pass'),COALESCE(max(checked_at),0) FROM console_quality_history WHERE ((source_id=$1 AND provider=$2) OR (account_id=$6 AND group_id=$7)) AND model=$3 AND rule='candy-21' AND successful AND verdict IN ('pass','fail') AND checked_at>=$4 AND checked_at<=$5`, t.SourceID, name, checkModel, start.Unix(), now.Unix(), ref.Account, ref.Group).Scan(&m.QualityN, &passed, &m.QualityLatest)
			if e != nil {
				return nil, errors.New("降智历史读取失败")
			}
			if m.QualityN > 0 {
				m.Quality = float64(passed) / float64(m.QualityN)
			}
			m.QualityCI = interval(float64(passed), m.QualityN, z)
			if now.Unix()-m.QualityLatest > int64(p.MaxAgeSeconds) {
				m.Reason = "最近降智检测已超出数据新鲜度限制"
			}
		}
		for metric, latest := range map[string]int64{"latency": m.LatencyLatest, "success": m.SuccessLatest, "cache": m.CacheLatest} {
			if metricEnabled(p, metric) && now.UnixMilli()-latest > int64(p.MaxAgeSeconds)*1000 {
				m.Reason = metric + " 最近样本已超出数据新鲜度限制"
			}
		}
		if !m.Eligible {
			m.Reason = "渠道当前不可用或已停用"
		}
		out[name] = m
	}
	return out, nil
}
func dominanceReason(a, b automationMetric, p AutomationPolicy) (bool, string) {
	if a.Provider == "" || b.Provider == "" || len(p.Metrics) == 0 {
		return false, "缺少可比较渠道"
	}
	if a.Reason != "" || b.Reason != "" {
		return false, a.Reason + " " + b.Reason
	}
	if !a.Eligible || !b.Eligible {
		return false, "渠道已停用或当前不可用"
	}
	if a.Upstream != b.Upstream {
		return false, "实际模型不同，不能直接比较"
	}
	better := false
	for _, metric := range p.Metrics {
		var av, bv float64
		var ac, bc [2]float64
		var an, bn, minimum int
		var threshold, tolerance float64
		switch metric {
		case "latency":
			av, bv = -a.Latency, -b.Latency
			ac = [2]float64{-a.LatencyCI[1], -a.LatencyCI[0]}
			bc = [2]float64{-b.LatencyCI[1], -b.LatencyCI[0]}
			an, bn, minimum = a.LatencyN, b.LatencyN, p.MinLatencySamples
			// Either a relative or an absolute material improvement is sufficient.
			threshold = math.Min(b.Latency*p.LatencyMinPercent/100, p.LatencyMinMS)
			tolerance = b.Latency * p.LatencyTolerancePercent / 100
		case "success":
			av, bv = a.Success, b.Success
			ac, bc = a.SuccessCI, b.SuccessCI
			an, bn, minimum = a.SuccessN, b.SuccessN, p.MinSuccessSamples
			threshold = p.SuccessMinPP / 100
			tolerance = p.SuccessTolerancePP / 100
		case "cache":
			av, bv = a.Cache, b.Cache
			ac, bc = a.CacheCI, b.CacheCI
			an, bn, minimum = a.CacheN, b.CacheN, p.MinCacheSamples
			threshold = p.CacheMinPP / 100
			tolerance = p.CacheTolerancePP / 100
		case "quality":
			av, bv = a.Quality, b.Quality
			ac, bc = a.QualityCI, b.QualityCI
			an, bn, minimum = a.QualityN, b.QualityN, p.MinQualitySamples
			threshold = p.QualityMinPP / 100
			tolerance = p.QualityTolerancePP / 100
		default:
			return false, "未知指标"
		}
		if an < minimum || bn < minimum {
			return false, fmt.Sprintf("%s 样本不足（%d、%d，要求 %d）", metric, an, bn, minimum)
		}
		if av < bv {
			return false, metric + " 观测值更差"
		}
		if ac[0]+tolerance < bc[1] {
			return false, metric + " 置信区间不足以确认不劣"
		}
		if av > bv && ac[0]-bc[1] >= threshold {
			better = true
		}
	}
	if !better {
		return false, "没有指标达到可信的最小改善幅度"
	}
	return true, "所有指标不劣，且至少一项达到改善门槛"
}
func dominates(a, b automationMetric, p AutomationPolicy, _ float64) bool {
	ok, _ := dominanceReason(a, b, p)
	return ok
}
func dominatesAllHigher(order []string, index int, metrics map[string]automationMetric, p AutomationPolicy, z float64) bool {
	for j := 0; j < index; j++ {
		if !dominates(metrics[order[index]], metrics[order[j]], p, z) {
			return false
		}
	}
	return true
}
func automationPlan(order []string, metrics map[string]automationMetric, p AutomationPolicy) ([]string, []map[string]any, []string) {
	after := append([]string{}, order...)
	changes := []map[string]any{}
	reasons := []string{}
	for move := 0; move < p.MaxMoves; move++ {
		found := false
		for i := len(after) - 1; i > 0; i-- {
			low, high := metrics[after[i]], metrics[after[i-1]]
			ok, reason := dominanceReason(low, high, p)
			if !ok {
				if move == 0 {
					reasons = append(reasons, after[i]+" → "+after[i-1]+"："+reason)
				}
				continue
			}
			if p.RequireAllHigher && !dominatesAllHigher(after, i, metrics, p, 0) {
				continue
			}
			after[i], after[i-1] = after[i-1], after[i]
			changes = append(changes, map[string]any{"provider": low.Provider, "over": high.Provider})
			found = true
			break
		}
		if !found {
			break
		}
	}
	return after, changes, reasons
}

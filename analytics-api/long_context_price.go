package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Rollups cannot distinguish one long request from several short requests.
// Read only the matching long-request token columns, in the same SQL snapshot
// as the rollups. This also prices historical facts without rebuilding or
// changing the checkpoint format. The no-tier path never scans facts.
func (e *Engine) withLongContextUsage(ctx context.Context, q string, args []any, where []string, startMS, endMS int64, prices []Price) (string, []any, error) {
	modelFilters := []string{}
	tierModels := map[int64][]string{}
	tierArgs := map[int64][]any{}
	knownThresholds := true
	const billed = "coalesce(nullif(trim(upstream_model),''),model)"
	minThreshold := int64(1<<63 - 1)
	end := endMS/60000*60000 + 60000
	longArgs := []any{startMS, end}
	// The first predicate contains server-derived window boundaries; args only
	// bind the remaining client filters. Raw usage preserves minute semantics.
	longArgs = append(longArgs, args...)
	for _, p := range prices {
		tier := p.promptPriceTier()
		if !p.Verified || tier == nil || p.Model != canonicalPriceModel(p.Model) {
			continue
		}
		// Match usagePriceModel's upstream-first fallback. The public model
		// filter remains in where, but cannot select the upstream price policy.
		minThreshold = min(minThreshold, tier.ThresholdTokens)
		modelFilters = append(modelFilters, "(input_tokens>? AND ("+billed+"=? OR starts_with("+billed+",?)))")
		longArgs = append(longArgs, tier.ThresholdTokens, p.Model, p.Model+"-")
		tierModels[tier.ThresholdTokens] = append(tierModels[tier.ThresholdTokens], "("+billed+"=? OR starts_with("+billed+",?))")
		tierArgs[tier.ThresholdTokens] = append(tierArgs[tier.ThresholdTokens], p.Model, p.Model+"-")
		knownThresholds = knownThresholds && (tier.ThresholdTokens == 100000 || tier.ThresholdTokens == 272000)
	}
	if len(modelFilters) == 0 {
		return "SELECT b.*,0::BIGINT AS long_input,0::BIGINT AS long_output,0::BIGINT AS long_cache_read,0::BIGINT AS long_cache_write,0::BIGINT AS long_cache_write_1h FROM (" + q + ") b", args, nil
	}
	longWhere := []string{"at_ms>=? AND at_ms<?", "kind NOT IN ('billing','trace')", "input_tokens IS NOT NULL", fmt.Sprintf("input_tokens>%d", minThreshold)}
	longWhere = append(longWhere, where[1:]...)
	longWhere = append(longWhere, "("+strings.Join(modelFilters, " OR ")+")")
	dimensions := []string{"source_id", "kind", "provider", "model", "upstream_model", "endpoint", "stream", "outcome"}
	dims := strings.Join(dimensions, ",")
	rawSource := "usage_facts"
	tierSQL := ""
	var summaryArgs []any
	lo, hi := (startMS+3599999)/3600000*3600000, end/3600000*3600000
	if e.priceTiersReady && knownThresholds && lo < hi {
		gaps, covered, err := e.uncoveredPriceTierRanges(ctx, startMS, end, lo, hi)
		if err != nil {
			return "", nil, err
		}
		if covered && len(gaps) <= 32 {
			// Literals are computed only from server timestamps and let PostgreSQL
			// prune all raw partitions represented by a sealed hour.
			parts := make([]string, 0, len(gaps))
			for _, gap := range gaps {
				parts = append(parts, fmt.Sprintf("(at_ms >= %d AND at_ms < %d)", gap[0], gap[1]))
			}
			if len(parts) == 0 {
				longWhere = append(longWhere, "false")
			} else {
				longWhere = append(longWhere, "("+strings.Join(parts, " OR ")+")")
			}
			// The legacy predicate ORs eligible price rules. Select the lowest
			// matching threshold once, even if an alias matches multiple prefixes.
			thresholds := make([]int64, 0, len(tierModels))
			for t := range tierModels {
				thresholds = append(thresholds, t)
			}
			sort.Slice(thresholds, func(i, j int) bool { return thresholds[i] < thresholds[j] })
			summaryArgs = append([]any{lo, hi}, args...)
			choose := "CASE"
			for _, t := range thresholds {
				choose += " WHEN " + strings.Join(tierModels[t], " OR ") + fmt.Sprintf(" THEN %d", t)
				summaryArgs = append(summaryArgs, tierArgs[t]...)
			}
			predicates := append([]string{"period_ms>=? AND period_ms<?"}, where[1:]...)
			predicates = append(predicates, "threshold=("+choose+" END)")
			valueSums := make([]string, 0, len(strings.Split(priceTierValues, ",")))
			for _, name := range strings.Split(priceTierValues, ",") {
				valueSums = append(valueSums, "sum("+name+")::bigint AS "+name)
			}
			tierSQL = ` UNION ALL SELECT ` + dims + `,` + strings.Join(valueSums, ",") + ` FROM price_tier_rollups WHERE ` + strings.Join(predicates, " AND ") + ` GROUP BY ` + dims
		}
	}
	join := make([]string, len(dimensions))
	for i, dim := range dimensions {
		// Every base dimension is NOT NULL in rollups. Ordinary equality has
		// identical join semantics and permits a merge/hash join in PostgreSQL.
		join[i] = "b." + dim + "=l." + dim
	}
	query := `WITH base AS (` + q + `), tier_input AS (
 SELECT ` + dims + `,greatest(0,input_tokens-coalesce(cache_read_tokens,0)-coalesce(cache_write_tokens,0)) AS ordinary_tokens,
 output_tokens,cache_read_tokens,cache_write_tokens,cache_write_1h_tokens
 FROM ` + rawSource + ` WHERE ` + strings.Join(longWhere, " AND ") + tierSQL + `), long_usage AS (
 SELECT ` + dims + `,sum(ordinary_tokens)::bigint AS long_input,sum(output_tokens)::bigint AS long_output,
 sum(cache_read_tokens)::bigint AS long_cache_read,sum(cache_write_tokens)::bigint AS long_cache_write,
 sum(cache_write_1h_tokens)::bigint AS long_cache_write_1h
 FROM tier_input GROUP BY ` + dims + `)
 SELECT b.*,coalesce(l.long_input,0),coalesce(l.long_output,0),coalesce(l.long_cache_read,0),coalesce(l.long_cache_write,0),coalesce(l.long_cache_write_1h,0)
 FROM base b LEFT JOIN long_usage l ON ` + strings.Join(join, " AND ")
	return query, append(append(args, longArgs...), summaryArgs...), nil
}

func (e *Engine) uncoveredPriceTierRanges(ctx context.Context, start, end, lo, hi int64) ([][2]int64, bool, error) {
	rows, err := e.DB.QueryContext(ctx, `SELECT period_ms FROM price_tier_hours WHERE period_ms>=? AND period_ms<? ORDER BY period_ms`, lo, hi)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var gaps [][2]int64
	pos := start
	for rows.Next() {
		var h int64
		if err = rows.Scan(&h); err != nil {
			return nil, false, err
		}
		if pos < h {
			gaps = append(gaps, [2]int64{pos, h})
		}
		pos = h + 3600000
	}
	if pos < end {
		gaps = append(gaps, [2]int64{pos, end})
	}
	return gaps, pos != start, rows.Err()
}

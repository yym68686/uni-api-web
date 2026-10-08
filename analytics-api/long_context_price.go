package main

import "strings"

// Rollups cannot distinguish one long request from several short requests.
// Read only the matching long-request token columns, in the same SQL snapshot
// as the rollups. This also prices historical facts without rebuilding or
// changing the checkpoint format. The no-tier path never scans facts.
func withLongContextUsage(q string, args []any, where []string, startMS, endMS int64, prices []Price) (string, []any) {
	modelFilters := []string{}
	longArgs := []any{startMS, endMS/60000*60000 + 60000}
	// The first predicate and first six args describe the disjoint day/minute
	// window. Its fact equivalent has the same full-minute boundary semantics.
	longArgs = append(longArgs, args[6:]...)
	for _, p := range prices {
		tier := p.promptPriceTier()
		if !p.Verified || tier == nil || p.Model != canonicalPriceModel(p.Model) {
			continue
		}
		// Match usagePriceModel's upstream-first fallback. The public model
		// filter remains in where, but cannot select the upstream price policy.
		const billed = "coalesce(nullif(trim(upstream_model),''),model)"
		modelFilters = append(modelFilters, "(input_tokens>? AND ("+billed+"=? OR starts_with("+billed+",?)))")
		longArgs = append(longArgs, tier.ThresholdTokens, p.Model, p.Model+"-")
	}
	if len(modelFilters) == 0 {
		return "SELECT b.*,0::BIGINT AS long_input,0::BIGINT AS long_output,0::BIGINT AS long_cache_read,0::BIGINT AS long_cache_write,0::BIGINT AS long_cache_write_1h FROM (" + q + ") b", args
	}
	longWhere := []string{"at_ms>=? AND at_ms<?", "kind NOT IN ('billing','trace')"}
	longWhere = append(longWhere, where[1:]...)
	longWhere = append(longWhere, "("+strings.Join(modelFilters, " OR ")+")")
	dimensions := []string{"source_id", "kind", "provider", "model", "upstream_model", "endpoint", "stream", "outcome"}
	join := make([]string, len(dimensions))
	for i, dim := range dimensions {
		join[i] = "b." + dim + " IS NOT DISTINCT FROM l." + dim
	}
	query := `WITH base AS (` + q + `), long_usage AS (
 SELECT ` + strings.Join(dimensions, ",") + `,
 sum(greatest(0,input_tokens-coalesce(cache_read_tokens,0)-coalesce(cache_write_tokens,0)))::BIGINT AS long_input,
 sum(coalesce(output_tokens,0))::BIGINT AS long_output,
 sum(coalesce(cache_read_tokens,0))::BIGINT AS long_cache_read,
 sum(coalesce(cache_write_tokens,0))::BIGINT AS long_cache_write,
 sum(coalesce(cache_write_1h_tokens,0))::BIGINT AS long_cache_write_1h
 FROM facts WHERE ` + strings.Join(longWhere, " AND ") + ` GROUP BY ` + strings.Join(dimensions, ",") + `)
 SELECT b.*,coalesce(l.long_input,0),coalesce(l.long_output,0),coalesce(l.long_cache_read,0),coalesce(l.long_cache_write,0),coalesce(l.long_cache_write_1h,0)
 FROM base b LEFT JOIN long_usage l ON ` + strings.Join(join, " AND ")
	return query, append(args, longArgs...)
}

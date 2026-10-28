package main

import "math"

// The legacy group result can contain large reply bodies. Project it before
// the generic JSON compactor instead of serializing the bodies only to discard
// them. Keep the same compactor/decoder for metadata, numeric representation,
// nulls and deep cloning, including shared quality-cache objects.
func compactManagementTargetMetadata(target subTarget) any {
	var result *subResultSummary
	if r := target.Result; r != nil {
		// The original JSON marshal rejects nonfinite values, even in omitted
		// receipt fields. Preserve that error behavior rather than hiding them.
		if !managementUsageJSONFinite(r.Availability.Usage) || !managementUsageJSONFinite(r.Quality.Usage) {
			return compactSummary(target)
		}
		result = &subResultSummary{Model: r.Model, CheckedAt: r.CheckedAt, Verdict: r.Verdict,
			Availability: summarizeSubProbe(r.Availability), Quality: summarizeSubProbe(r.Quality)}
	}
	return compactSummary(struct {
		subTarget
		Result *subResultSummary `json:"result"`
	}{target, result})
}

func managementUsageJSONFinite(u *subUsage) bool {
	if u == nil {
		return true
	}
	for _, value := range []*float64{u.ActualCost, u.TotalCost, u.Rate, u.InputPrice, u.OutputPrice,
		u.CacheReadPrice, u.CacheWritePrice, u.PaidInputPrice, u.PaidOutputPrice} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return false
		}
	}
	return true
}

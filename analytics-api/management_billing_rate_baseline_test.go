package main

import "math"

// Independent complete scan, before the exact upstream lookup optimization.
func previousManagementBillingRate(row managementQueryRow, model string) managementRate {
	groups := map[string]bool{}
	if row.Configured != nil {
		for _, member := range row.Configured.Members {
			for _, binding := range managementBoundGroups(member) {
				if binding.AccountID != "" && binding.GroupID > 0 {
					groups[qualityGroupID(binding.AccountID, binding.GroupID)] = true
				}
			}
		}
	}
	if len(groups) > 1 {
		return managementRate{}
	}
	var advertised *float64
	if row.Target.Billing != nil {
		advertised = subValidCost(row.Target.Billing.Rate)
	}
	upstream := model
	allowed := map[string]bool{}
	if row.Configured != nil {
		upstream = managementUpstream(row.Configured.managementChannel, model)
		for _, name := range row.Configured.Models {
			allowed[managementUpstream(row.Configured.managementChannel, name)] = true
		}
	}
	out := managementRate{Rate: advertised}
	observed := false
	for name, rate := range row.Target.ObservedRates {
		if subValidCost(&rate.Rate) == nil || upstream != "" && name != upstream || upstream == "" && row.Configured != nil && !allowed[name] {
			continue
		}
		value := rate.Rate
		if !observed || value > *out.Rate {
			out.Rate = &value
		}
		observed = true
		out.Mismatch = out.Mismatch || advertised != nil && math.Abs(value-*advertised) > math.Max(1e-9, math.Abs(*advertised)*1e-9)
	}
	return out
}

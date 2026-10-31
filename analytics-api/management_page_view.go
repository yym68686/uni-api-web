package main

import "context"

// Explicitly negotiated display projection. Catalogs, predicates, signed
// selections and legacy callers remain complete. A projected target must be
// reread in full before it can supply import defaults or authorize an import.
type managementSelectedReceiptsKey struct{}

func managementSelectedReceipts(ctx context.Context) bool {
	selected, _ := ctx.Value(managementSelectedReceiptsKey{}).(bool)
	return selected
}

func managementPageComputedKey(ctx context.Context, owner string, filters managementQueryFilters, cursor string) string {
	if managementSelectedReceipts(ctx) {
		cursor += "\x00selected-receipts-v1"
	}
	return managementComputedKey(owner, filters, cursor)
}

// Bound members still hydrate every receipt: aliases and merged checks can
// reference several upstream models. Only independent site rows use the
// selected-model projection. The model/status directory stays complete.
func managementPageUsageAccounts(accounts []subAccount, model string, fullGroups map[string]bool) []subAccount {
	out := make([]subAccount, len(accounts))
	for i, account := range accounts {
		out[i] = account
		out[i].Targets = make([]subTarget, len(account.Targets))
		for j, target := range account.Targets {
			out[i].Targets[j] = target
			if fullGroups[qualityGroupID(account.ID, target.GroupID)] {
				continue
			}
			models := make([]subModelResult, 0, 2)
			for _, check := range target.Models {
				if check.Model == model || check.Model == checkModel {
					models = append(models, check)
				} else if check.Result != nil {
					// Summary JSON can contain old embedded usage. Never present
					// that value as a freshly read receipt in this explicit view.
					check.Result.Availability.Usage = nil
					check.Result.Quality.Usage = nil
				}
			}
			out[i].Targets[j].Models = models
		}
	}
	return out
}

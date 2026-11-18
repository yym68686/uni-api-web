package main

import "context"

// Explicitly negotiated display projection. Catalogs, predicates, signed
// selections and legacy callers remain complete. A projected target must be
// reread in full before it can supply import defaults or authorize an import.
type managementSelectedReceiptsKey struct{}
type managementSelectedModelsKey struct{}
type managementListViewKey struct{}

const managementListViewHeader = "X-Console-Management-View"
const managementListViewV1 = "list-v1"

func managementListView(ctx context.Context) bool {
	light, _ := ctx.Value(managementListViewKey{}).(bool)
	return light
}

func managementSelectedModels(ctx context.Context) bool {
	selected, _ := ctx.Value(managementSelectedModelsKey{}).(bool)
	return selected
}

func managementSelectedReceipts(ctx context.Context) bool {
	selected, _ := ctx.Value(managementSelectedReceiptsKey{}).(bool)
	return selected
}

func managementPageComputedKey(ctx context.Context, owner string, filters managementQueryFilters, cursor string) string {
	if managementListView(ctx) {
		cursor += "\x00list-v1"
	}
	if managementSelectedReceipts(ctx) {
		cursor += "\x00selected-receipts-v1"
	}
	if managementSelectedModels(ctx) {
		cursor += "\x00selected-models-v1"
	}
	return managementComputedKey(owner, filters, cursor)
}

// Keep exactly the evidence used by the list (states, quality, match and
// latency). Omitted receipts are not negative pricing evidence: the UI only
// requests this representation when no receipt-based price column is shown.
// Copy before projection: page rows can alias input/predicate/selection data.
func managementListRows(page *managementQueryPage) {
	projected := make(map[*subResult]*subResult)
	result := func(r *subResult) *subResult {
		if r == nil {
			return nil
		}
		if out := projected[r]; out != nil {
			return out
		}
		out := *r
		p := r.Availability
		out.Availability = subProbe{
			Status: p.Status, TerminalStatus: p.TerminalStatus, ModelMatch: p.ModelMatch,
			RequestedModel: p.RequestedModel, ResponseModel: p.ResponseModel,
			Protocol: p.Protocol, TTFT: p.TTFT, ResponseCreatedMS: p.ResponseCreatedMS, FirstResponseMS: p.FirstResponseMS,
		}
		out.Quality = subProbe{Status: r.Quality.Status}
		projected[r] = &out
		return &out
	}
	page.Rows = append([]managementQueryRow{}, page.Rows...)
	for i := range page.Rows {
		r := &page.Rows[i]
		r.Target.ListSummary, r.Target.DetailsOmitted = true, true
		r.Target.Result = result(r.Target.Result)
		if r.Target.Models != nil {
			r.Target.Models = append([]subModelResult{}, r.Target.Models...)
		}
		for j := range r.Target.Models {
			r.Target.Models[j].Result = result(r.Target.Models[j].Result)
			r.Target.Models[j].Message = ""
		}
		r.Checks = append([]managementQueryCheck{}, r.Checks...)
		for j := range r.Checks {
			r.Checks[j].Result = result(r.Checks[j].Result)
			r.Checks[j].Message = ""
		}
		if r.Selected != nil {
			selected := *r.Selected
			selected.Result, selected.Message = result(selected.Result), ""
			r.Selected = &selected
		}
	}
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

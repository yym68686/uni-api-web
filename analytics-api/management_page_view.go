package main

import "context"

// Explicitly negotiated display projection. Catalogs, predicates, signed
// selections and legacy callers remain complete. A projected target must be
// reread in full before it can supply import defaults or authorize an import.
type managementSelectedReceiptsKey struct{}
type managementSelectedModelsKey struct{}
type managementListViewKey struct{}
type managementOverviewKey struct{}

const managementListViewHeader = "X-Console-Management-View"
const managementListViewV1 = "list-v1"
const managementOverviewV1 = "overview-v1"

func managementOverviewView(ctx context.Context) bool {
	overview, _ := ctx.Value(managementOverviewKey{}).(bool)
	return overview
}

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
	if managementOverviewView(ctx) {
		cursor += "\x00overview-v1"
	}
	if managementSelectedReceipts(ctx) {
		cursor += "\x00selected-receipts-v1"
	}
	if managementSelectedModels(ctx) {
		cursor += "\x00selected-models-v1"
	}
	return managementComputedKey(owner, filters, cursor)
}

type managementOverview struct {
	Total       int   `json:"total"`
	Success     int   `json:"success"`
	Warning     int   `json:"warning"`
	Failed      int   `json:"failed"`
	Untested    int   `json:"untested"`
	Pending     int   `json:"pending"`
	Interrupted int   `json:"interrupted"`
	CheckedAt   int64 `json:"checked_at"`
}

// Keep the in-process counting reference used on fallback groups and for the
// legacy Astra result. The equivalent SQL applies only to current projections.
func (o *managementOverview) add(c subModelResult) {
	if c.Result != nil {
		o.CheckedAt = max(o.CheckedAt, c.Result.CheckedAt)
	}
	switch c.State {
	case "queued", "running":
		o.Pending++
	case "interrupted":
		o.Interrupted++
	default:
		status, terminal := "", ""
		if c.Result != nil {
			status, terminal = c.Result.Availability.Status, c.Result.Availability.TerminalStatus
		}
		if status == "success" {
			if c.State != "error" {
				o.Success++
			}
			if terminal != "" && terminal != "complete" {
				o.Warning++
			}
		}
		if status == "error" || c.State == "error" {
			o.Failed++
		} else if c.Result == nil {
			o.Untested++
		}
	}
}

// Only the operations columns are negotiated here. Keep the model status
// names used for busy-button decisions and active sync scopes. Other columns
// and configured aliases can still negotiate the richer list.
func managementApplyOverview(row *managementQueryRow) bool {
	if row.Selected != nil || row.Configured != nil {
		return false
	}
	if row.Target.ListOverview == nil {
		o := &managementOverview{Total: len(row.Checks)}
		for _, c := range row.Checks {
			o.add(c.subModelResult)
		}
		row.Target.ListOverview = o
	}
	// Keep check identities while a sync is in progress for per-model progress
	// if the independent progress feed has yet to provide a group record.
	if row.Account.JobKind == "sync" && managementPending(row.Account.State) {
		return false
	}
	row.Checks = []managementQueryCheck{}
	if row.Target.Models != nil {
		pending := []subModelResult{}
		for _, m := range row.Target.Models {
			if managementPending(m.State) {
				pending = append(pending, subModelResult{Model: m.Model, State: m.State})
			}
		}
		row.Target.Models = pending
	}
	row.Target.Result, row.Target.QualityCheck = nil, nil
	row.Target.Compaction, row.Target.ToolUse = nil, nil
	return true
}

// Keep exactly the evidence used by the list (states, quality, match and
// latency). Omitted receipts are not negative pricing evidence: the UI only
// requests this representation when no receipt-based price column is shown.
// Copy before projection: page rows can alias input/predicate/selection data.
func managementListRows(page *managementQueryPage, overview ...bool) {
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
		if len(overview) > 0 && overview[0] && managementApplyOverview(r) {
			continue
		}
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

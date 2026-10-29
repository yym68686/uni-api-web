package main

import (
	"context"
	"slices"
)

// Public aliases can name different upstream models on different members. A
// request-local superset keeps every such candidate, including the independent
// Astra quality result. Names come from the fenced current gateway, not a cache.
func managementScopeModelNames(model string, facts []managementGatewayFacts, bindings []subInstalledChannel) []string {
	names := []string{model, "gpt-6-astra"}
	for _, source := range facts {
		catalogs := [][]managementModel{source.Catalog}
		for _, routes := range source.Routes {
			catalogs = append(catalogs, routes)
		}
		for _, catalog := range catalogs {
			for _, row := range catalog {
				if row.Model == model && row.Upstream != "" {
					names = append(names, row.Upstream)
				}
			}
		}
	}
	for _, binding := range bindings {
		if upstream := binding.ModelMappings[model]; upstream != "" {
			names = append(names, upstream)
		}
	}
	return managementUnique(names)
}

// Existing filter semantics fall back to all checks when a non-default selected
// model is absent from a site group. Preserve that exact behavior by reading all
// predicates for those groups, rather than manufacturing an untested selection.
func managementScopeFallbackGroups(model string, catalog []managementModelSignals) []subBoundKey {
	if slices.Contains(subModels, model) {
		return []subBoundKey{}
	}
	groups, found := []subBoundKey{}, map[string]bool{}
	present := map[string]bool{}
	for _, item := range catalog {
		key := qualityGroupID(item.Account, item.Group)
		if !found[key] {
			groups = append(groups, subBoundKey{AccountID: item.Account, GroupID: item.Group})
			found[key] = true
		}
		if item.Model.Model == model {
			present[key] = true
		}
	}
	out := []subBoundKey{}
	for _, group := range groups {
		if !present[qualityGroupID(group.AccountID, group.GroupID)] {
			out = append(out, group)
		}
	}
	return out
}

func managementStreamFallbackGroups(model string, models []managementStreamModel) []subBoundKey {
	// The existing fallback contract is independent of the model directory for
	// built-in models. Check that before allocating/copying the complete stream.
	if slices.Contains(subModels, model) {
		return []subBoundKey{}
	}
	catalog := make([]managementModelSignals, len(models))
	for i := range models {
		catalog[i] = models[i].item
	}
	return managementScopeFallbackGroups(model, catalog)
}

// Catalog entries without predicate results are private assembly inputs only.
// Every visible page group and bound alias is fully hydrated before publishing;
// complete action selections continue to read every model's full evidence.
func readManagementScopedSignals(ctx context.Context, db controlQueryDB, owner, model string, names []string) ([]managementModelSignals, string, error) {
	return readManagementScopedSignalsWithReceipts(ctx, db, owner, model, names, nil)
}

func readManagementScopedSignalsWithReceipts(ctx context.Context, db controlQueryDB, owner, model string, names []string, reuse *managementReceiptReuse) ([]managementModelSignals, string, error) {
	endCatalog := managementReadSpan(ctx, "database_model_catalog")
	defer endCatalog()
	return readManagementScopedStreams(ctx, db, owner, true, managementStreamScope{Model: model, Names: names, ReceiptReuse: reuse})
}

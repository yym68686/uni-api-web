package main

import (
	"context"
	"encoding/json"
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

// Catalog entries without predicate results are private assembly inputs only.
// Every visible page group and bound alias is fully hydrated before publishing;
// complete action selections continue to read every model's full evidence.
func readManagementScopedSignals(ctx context.Context, db controlQueryDB, owner, model string, names []string) ([]managementModelSignals, string, error) {
	endCatalog := managementReadSpan(ctx, "database_model_catalog")
	revision, catalog, err := readManagementRevisionCatalogFrom(ctx, db, owner, true)
	endCatalog()
	if err != nil {
		return nil, "", err
	}
	fallback := managementScopeFallbackGroups(model, catalog)
	raw, err := json.Marshal(fallback)
	if err != nil {
		return nil, "", err
	}
	query := `SELECT ` + managementModelSignalsStoredColumns + managementModelSignalsStoredFrom + `
 AND (m.model=ANY($2::text[]) OR EXISTS(SELECT 1 FROM jsonb_to_recordset($3::jsonb) AS g(account_id text,group_id bigint) WHERE g.account_id=m.account_id AND g.group_id=m.group_id))
 ORDER BY m.account_id COLLATE "C",m.group_id,m.model COLLATE "C"`
	endPredicates := managementReadSpan(ctx, "database_scoped_predicates")
	selected, err := readManagementSignalsColumnsArgs(ctx, db, query, owner, names, string(raw))
	endPredicates()
	if err != nil {
		return nil, "", err
	}
	positions := make(map[string]map[string]int)
	for i, item := range catalog {
		key := qualityGroupID(item.Account, item.Group)
		if positions[key] == nil {
			positions[key] = map[string]int{}
		}
		positions[key][item.Model.Model] = i
	}
	for _, item := range selected {
		group := positions[qualityGroupID(item.Account, item.Group)]
		if i, exists := group[item.Model.Model]; exists {
			catalog[i] = item
		}
	}
	return catalog, revision, nil
}

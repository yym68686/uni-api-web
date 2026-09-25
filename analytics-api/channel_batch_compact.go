package main

import (
	"reflect"
	"sort"
)

// A batch may touch dozens of models for every key. Represent compatible
// orders once at key scope instead of exhausting the gateway's scope budget.
// Only orders written by this batch may be removed; every other model keeps
// its effective order and every disabled policy remains intact.
func compactBatchRouteRules(snapshot *retainedSnapshot, catalogs map[string][]batchCatalogRow, touched map[string][]routeMove) {
	for key, moves := range touched {
		models := map[string]bool{}
		for _, m := range moves {
			models[m.Model] = true
		}
		if len(models) < 2 {
			continue
		}
		orders := map[string][]string{}
		for _, row := range catalogs[key] {
			if !containsProvider(orders[row.Model], row.Provider) {
				orders[row.Model] = append(orders[row.Model], row.Provider)
			}
		}
		exact := map[string]retainedRule{}
		wildcard := -1
		constraints := [][]string{}
		for i, r := range snapshot.Rules {
			if r.KeyID != key {
				continue
			}
			if r.Model == "" {
				wildcard = i
				constraints = append(constraints, r.Order)
			} else {
				exact[r.Model] = r
			}
		}
		// Models without a specific order would inherit the new key order.
		// Constrain their existing visible sequence, including global rules.
		for m, order := range orders {
			if !models[m] && len(exact[m].Order) == 0 {
				constraints = append(constraints, order)
			}
		}
		if _, ok := mergeRouteOrders(constraints); !ok {
			continue
		}
		candidates := []string{}
		for m := range models {
			if len(exact[m].Order) > 0 {
				candidates = append(candidates, m)
			}
		}
		sort.Strings(candidates)
		merged := map[string]bool{}
		for _, m := range candidates {
			next := append(append([][]string{}, constraints...), exact[m].Order)
			if _, ok := mergeRouteOrders(next); ok {
				constraints = next
				merged[m] = true
			}
		}
		order, ok := mergeRouteOrders(constraints)
		if !ok || len(order) > 1024 || len(merged) < 2 {
			continue
		}
		// Verify the projection for every affected/inheriting model before
		// replacing rules; a failed check leaves the original snapshot intact.
		valid := true
		for m, current := range orders {
			if !merged[m] && len(exact[m].Order) > 0 {
				continue
			}
			want := current
			if merged[m] {
				want = projectedRouteOrder(current, exact[m].Order)
			}
			if !reflect.DeepEqual(want, projectedRouteOrder(current, order)) {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		if wildcard < 0 {
			snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: key, Order: order, Disabled: []string{}})
		} else {
			snapshot.Rules[wildcard].Order = order
		}
		next := []retainedRule{}
		for _, r := range snapshot.Rules {
			if r.KeyID == key && merged[r.Model] {
				r.Order = []string{}
				if len(r.Disabled) == 0 {
					continue
				}
			}
			next = append(next, r)
		}
		snapshot.Rules = next
	}
}

func containsProvider(values []string, provider string) bool {
	for _, v := range values {
		if v == provider {
			return true
		}
	}
	return false
}

func projectedRouteOrder(values, order []string) []string {
	ranks := map[string]int{}
	for i, p := range order {
		ranks[p] = i + 1
	}
	result := append([]string{}, values...)
	sort.SliceStable(result, func(i, j int) bool {
		a, b := ranks[result[i]], ranks[result[j]]
		if a == 0 {
			a = len(order) + 1
		}
		if b == 0 {
			b = len(order) + 1
		}
		return a < b
	})
	return result
}

// Deterministic topological merge. A conflicting model retains its own rule.
func mergeRouteOrders(orders [][]string) ([]string, bool) {
	edges := map[string]map[string]bool{}
	degree := map[string]int{}
	for _, order := range orders {
		for i, p := range order {
			if _, ok := degree[p]; !ok {
				degree[p] = 0
			}
			if i == 0 || order[i-1] == p {
				continue
			}
			prev := order[i-1]
			if edges[prev] == nil {
				edges[prev] = map[string]bool{}
			}
			if !edges[prev][p] {
				edges[prev][p] = true
				degree[p]++
			}
		}
	}
	result := []string{}
	for len(result) < len(degree) {
		ready := []string{}
		for p, d := range degree {
			if d == 0 {
				ready = append(ready, p)
			}
		}
		if len(ready) == 0 {
			return nil, false
		}
		sort.Strings(ready)
		p := ready[0]
		degree[p] = -1
		result = append(result, p)
		for q := range edges[p] {
			degree[q]--
		}
	}
	return result, true
}

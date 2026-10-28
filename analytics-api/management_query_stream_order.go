package main

import (
	"slices"
	"strings"
)

// Keep the exact byte order used by PostgreSQL COLLATE "C" and the revision
// writer, without repeatedly moving complete model/probe envelopes during
// sorting. The scratch slice contains only references to request-local models.
// Nil means the input is already ordered and needs no allocation or indirection.
func managementOrderedStreamModels(models []managementStreamModel) []*managementStreamModel {
	compare := func(a, b *managementStreamModel) int {
		if c := strings.Compare(a.item.Account, b.item.Account); c != 0 {
			return c
		}
		if a.item.Group < b.item.Group {
			return -1
		}
		if a.item.Group > b.item.Group {
			return 1
		}
		return strings.Compare(a.item.Model.Model, b.item.Model.Model)
	}
	ordered := true
	for i := 1; i < len(models); i++ {
		if compare(&models[i-1], &models[i]) > 0 {
			ordered = false
			break
		}
	}
	if ordered {
		return nil
	}
	order := make([]*managementStreamModel, len(models))
	for i := range models {
		order[i] = &models[i]
	}
	slices.SortFunc(order, compare)
	return order
}

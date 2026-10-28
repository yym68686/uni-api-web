package main

import "sort"

// Each row's complete billing evidence is immutable during this synchronous
// sort. Compute its rate once, not for every comparator invocation. Keep rates
// attached to slice positions: public row IDs can repeat across engines.
func sortManagementRowsByRate(rows []managementQueryRow, model, direction string) {
	if len(rows) < 2 {
		return
	}
	rates := make([]*float64, len(rows))
	for i, row := range rows {
		rates[i] = managementBillingRate(row, model).Rate
	}
	sortManagementRowsByKnownRates(rows, rates, direction)
}

// The facet/filter pipeline already has each accepted row's rate. Reuse only
// those request-local, slot-aligned values under the same stable ordering.
func sortManagementRowsByKnownRates(rows []managementQueryRow, rates []*float64, direction string) {
	if len(rows) < 2 {
		return
	}
	sort.Stable(&managementRowsByRate{rows: rows, rates: rates, ascending: direction == "asc"})
}

type managementRowsByRate struct {
	rows      []managementQueryRow
	rates     []*float64
	ascending bool
}

func (s *managementRowsByRate) Len() int { return len(s.rows) }
func (s *managementRowsByRate) Less(i, j int) bool {
	a, b := s.rates[i], s.rates[j]
	if a == nil {
		return false
	}
	if b == nil {
		return true
	}
	if s.ascending {
		return *a < *b
	}
	return *a > *b
}
func (s *managementRowsByRate) Swap(i, j int) {
	s.rows[i], s.rows[j] = s.rows[j], s.rows[i]
	s.rates[i], s.rates[j] = s.rates[j], s.rates[i]
}

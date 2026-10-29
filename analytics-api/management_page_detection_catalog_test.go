package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"
	"unicode/utf16"
)

// Retain the previous implementation as the output and benchmark baseline.
func previousManagementPageDetectionModels(rows []managementQueryRow) []string {
	extra := []string{}
	for _, row := range rows {
		for _, check := range row.Checks {
			if !slices.Contains(subModels, check.Model) {
				extra = append(extra, check.Model)
			}
		}
	}
	extra = managementUnique(extra)
	sort.Slice(extra, func(i, j int) bool {
		return slices.Compare(utf16.Encode([]rune(extra[i])), utf16.Encode([]rune(extra[j]))) < 0
	})
	return append(append([]string{}, subModels...), extra...)
}

func TestManagementPageDetectionCatalogPreservesNamesOrderAndInputs(t *testing.T) {
	names := append([]string{"", " ", "模型", "A", "a", "é", "e\u0301", "\uE000", "😀", "\xff", "\xfe", "\uFFFD", "a\x00b"}, subModels...)
	allNames := managementQueryRow{}
	for _, name := range names {
		allNames.Checks = append(allNames.Checks, managementQueryCheck{subModelResult: subModelResult{Model: name}})
	}
	cases := [][]managementQueryRow{nil, {}, {{Checks: []managementQueryCheck{}}}, {allNames, allNames}}
	rng := rand.New(rand.NewSource(51))
	for round := 0; round < 128; round++ {
		rows := []managementQueryRow{}
		for i := 0; i < rng.Intn(10); i++ {
			row := managementQueryRow{}
			for j := 0; j < rng.Intn(40); j++ {
				row.Checks = append(row.Checks, managementQueryCheck{subModelResult: subModelResult{Model: names[rng.Intn(len(names))], State: "idle"}})
			}
			rows = append(rows, row)
		}
		cases = append(cases, rows)
	}
	for i, rows := range cases {
		before := mustJSON(rows)
		want, got := previousManagementPageDetectionModels(rows), managementPageDetectionModels(rows)
		if !reflect.DeepEqual(got, want) || mustJSON(got) != mustJSON(want) || mustJSON(rows) != before {
			t.Fatalf("case %d changed catalog values, order, wire bytes, or input", i)
		}
		got[0] = "caller mutation"
		if !reflect.DeepEqual(managementPageDetectionModels(rows), want) {
			t.Fatalf("case %d returned an alias of the catalog or source", i)
		}
	}
}

func TestManagementPageDetectionCatalogIncludesOffPageNamesAndRevision(t *testing.T) {
	rows := make([]managementQueryRow, 61)
	for i := range rows {
		rows[i] = managementQueryRow{ID: fmt.Sprint(i), Checks: []managementQueryCheck{{subModelResult: subModelResult{Model: fmt.Sprintf("model-%03d", i)}}}}
	}
	in := managementQueryInput{ModelsRevision: "fixture"}
	filtered := managementFilteredRows{Rows: rows}
	filters, evidence := managementQueryFilters{}, managementFilterEvidence{}
	page, err := buildManagementQueryPageFromFilteredRowsMode(in, filtered, filters, evidence, "owner", []byte("key"), "", time.Unix(123, 0), false)
	if err != nil || len(page.Rows) != managementPageSize || !reflect.DeepEqual(page.DetectionModels, previousManagementPageDetectionModels(rows)) {
		t.Fatal("page lost off-page detection names", err)
	}
	baseline := page
	baseline.DetectionModels = previousManagementPageDetectionModels(rows)
	wantRevision, err := managementProjectedRevision(in, baseline, evidence)
	if err != nil || page.Revision != wantRevision {
		t.Fatal("catalog changed projected revision", err)
	}
	rows[60].Checks[0].Model = "off-page-change"
	changed, err := buildManagementQueryPageFromFilteredRowsMode(in, filtered, filters, evidence, "owner", []byte("key"), "", time.Unix(123, 0), false)
	if err != nil || changed.Revision == page.Revision {
		t.Fatal("off-page catalog change did not invalidate revision", err)
	}
}

var managementDetectionModelsSink []string

func BenchmarkManagementPageDetectionCatalog(b *testing.B) {
	for _, groups := range []int{1, 850} {
		rows := make([]managementQueryRow, groups)
		for i := range rows {
			for j := 0; j < 225; j++ {
				name := fmt.Sprintf("model-%03d", j)
				if j < len(subModels) {
					name = subModels[j]
				}
				rows[i].Checks = append(rows[i].Checks, managementQueryCheck{subModelResult: subModelResult{Model: name}})
			}
		}
		want := previousManagementPageDetectionModels(rows)
		for _, tc := range []struct {
			name string
			read func([]managementQueryRow) []string
		}{{"previous", previousManagementPageDetectionModels}, {"candidate", managementPageDetectionModels}} {
			b.Run(fmt.Sprintf("%d/%s", groups, tc.name), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					managementDetectionModelsSink = tc.read(rows)
				}
				if !reflect.DeepEqual(managementDetectionModelsSink, want) {
					b.Fatal("catalog changed")
				}
			})
		}
	}
}

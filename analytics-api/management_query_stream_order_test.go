package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func previousManagementStreamOrder(models []managementStreamModel) {
	slices.SortFunc(models, func(a, b managementStreamModel) int {
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
	})
}

func streamOrderFixture(n int) []managementStreamModel {
	models := make([]managementStreamModel, n)
	for i := range models {
		models[i] = managementStreamModel{item: managementModelSignals{Account: fmt.Sprintf("account-%02d", i%49), Group: int64(i % 749), Model: subModelResult{Model: fmt.Sprintf("model-%03d雪🙂", i/749), State: "done", Message: "distinct value"}}, message: "directory diagnostic", probe: fmt.Sprint(i), quality: "quality-id", evidence: []byte{byte(i)}}
	}
	rand.New(rand.NewSource(23)).Shuffle(len(models), func(i, j int) { models[i], models[j] = models[j], models[i] })
	return models
}

func TestManagementStreamOrderMatchesPreviousWithoutChangingInput(t *testing.T) {
	for _, n := range []int{0, 1, 2, 13, 250, 749, 190000} {
		models := streamOrderFixture(n)
		before := slices.Clone(models)
		want := slices.Clone(models)
		previousManagementStreamOrder(want)
		got := managementOrderedStreamModels(models)
		if got != nil && len(got) != len(want) || !reflect.DeepEqual(models, before) {
			t.Fatal("input changed or incomplete order", n)
		}
		for i := range want {
			item := &models[i]
			if got != nil {
				item = got[i]
			}
			if !reflect.DeepEqual(*item, want[i]) {
				t.Fatal("identity, order or evidence differs", n, i)
			}
		}
	}
}

func TestManagementStreamOrderKeepsByteKeysExtremesAndDuplicateEvidence(t *testing.T) {
	models := []managementStreamModel{}
	for _, account := range []string{"", "a", "é", "e\u0301", "雪", "🙂"} {
		for _, group := range []int64{-1 << 63, -1, 0, 1, 1<<63 - 1} {
			for _, name := range []string{"", "<>&", "a", "a\x00", "é", "e\u0301", "雪", "🙂"} {
				for copy := range 2 {
					models = append(models, managementStreamModel{item: managementModelSignals{Account: account, Group: group, Model: subModelResult{Model: name, Result: &subResult{CheckedAt: int64(copy)}}}, probe: fmt.Sprint(copy)})
				}
			}
		}
	}
	rand.New(rand.NewSource(24)).Shuffle(len(models), func(i, j int) { models[i], models[j] = models[j], models[i] })
	want, before := slices.Clone(models), slices.Clone(models)
	previousManagementStreamOrder(want)
	order := managementOrderedStreamModels(models)
	if order == nil || !reflect.DeepEqual(models, before) {
		t.Fatal("unordered fixture was not indexed or was mutated")
	}
	for i := range want {
		if !reflect.DeepEqual(*order[i], want[i]) {
			t.Fatal("equal identity or byte order differs", i)
		}
	}
}

func TestManagementStreamOrderAlreadyOrderedNeedsNoScratch(t *testing.T) {
	models := streamOrderFixture(2000)
	previousManagementStreamOrder(models)
	if managementOrderedStreamModels(models) != nil {
		t.Fatal("already ordered stream allocated an index")
	}
	if allocations := testing.AllocsPerRun(10, func() { managementOrderedStreamModels(models) }); allocations != 0 {
		t.Fatal("already ordered stream allocated scratch", allocations)
	}
}

var streamOrderBenchmarkSink any

func BenchmarkManagementStreamOrdering(b *testing.B) {
	for _, shape := range []string{"random", "sorted", "reversed"} {
		fixture := streamOrderFixture(190000)
		if shape != "random" {
			previousManagementStreamOrder(fixture)
			if shape == "reversed" {
				slices.Reverse(fixture)
			}
		}
		b.Run(shape+"/previous_value_sort", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				models := slices.Clone(fixture)
				previousManagementStreamOrder(models)
				streamOrderBenchmarkSink = models
			}
		})
		b.Run(shape+"/pointer_sort", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				models := slices.Clone(fixture)
				streamOrderBenchmarkSink = managementOrderedStreamModels(models)
			}
		})
	}
}

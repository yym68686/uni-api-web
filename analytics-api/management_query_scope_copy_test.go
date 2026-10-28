package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// Original stream call site, retained as a separate output/benchmark oracle.
func legacyManagementStreamFallbackGroups(model string, models []managementStreamModel) []subBoundKey {
	catalog := make([]managementModelSignals, len(models))
	for i := range models {
		catalog[i] = models[i].item
	}
	return managementScopeFallbackGroups(model, catalog)
}

func TestManagementStreamFallbackGroupsPreserveDirectoryContract(t *testing.T) {
	rng := rand.New(rand.NewSource(92933))
	names := append(append([]string{}, subModels...), "extra", "unknown", "", "雪\n模型")
	for trial := 0; trial < 100; trial++ {
		models := []managementStreamModel{}
		for n := rng.Intn(200); n > 0; n-- {
			models = append(models, managementStreamModel{item: managementModelSignals{
				Account: fmt.Sprint(rng.Intn(3)), Group: int64(rng.Intn(15)),
				Model: subModelResult{Model: names[rng.Intn(len(names))], State: "done"},
			}})
		}
		before := append([]managementStreamModel{}, models...)
		for _, model := range names {
			want := legacyManagementStreamFallbackGroups(model, models)
			got := managementStreamFallbackGroups(model, models)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d model %q groups differ: got %v want %v", trial, model, got, want)
			}
		}
		if !reflect.DeepEqual(models, before) {
			t.Fatal("fallback selection changed input directory")
		}
	}
	for _, model := range names {
		if got, want := managementStreamFallbackGroups(model, nil), legacyManagementStreamFallbackGroups(model, nil); !reflect.DeepEqual(got, want) || got == nil {
			t.Fatal("nil input changed empty-array semantics", model)
		}
	}
}

var managementFallbackBenchmarkSink []subBoundKey

func BenchmarkManagementStreamFallbackGroups(b *testing.B) {
	models := make([]managementStreamModel, 189656)
	for i := range models {
		models[i].item = managementModelSignals{
			Account: fmt.Sprint(i / 4000), Group: int64(i / 288),
			Model: subModelResult{Model: subModels[i%len(subModels)], State: "done"},
		}
	}
	model := subModels[len(subModels)/2]
	b.Run("legacy-built-in-copy", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			managementFallbackBenchmarkSink = legacyManagementStreamFallbackGroups(model, models)
		}
	})
	b.Run("skip-unused-built-in-copy", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			managementFallbackBenchmarkSink = managementStreamFallbackGroups(model, models)
		}
	})
}

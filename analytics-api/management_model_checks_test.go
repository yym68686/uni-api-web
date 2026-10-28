package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// Independent baseline: first name wins its position, last source row supplies
// the complete value. Do not use the production deduplication implementation.
func previousManagementModelChecks(target *subTarget) []managementQueryCheck {
	byModel := make(map[string]subModelResult, len(target.Models))
	names := append([]string{}, subModels...)
	for _, check := range target.Models {
		byModel[check.Model] = check
		names = append(names, check.Model)
	}
	unique, seen := []string{}, map[string]bool{}
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			unique = append(unique, name)
		}
	}
	checks := make([]managementQueryCheck, 0, len(names))
	for _, name := range unique {
		check, exists := byModel[name]
		if !exists {
			check = subModelResult{Model: name, State: "idle"}
			if name == "gpt-6-astra" && target.Result != nil {
				check.State, check.Result = "done", target.Result
			}
		}
		checks = append(checks, managementQueryCheck{subModelResult: check})
	}
	return checks
}

func TestManagementModelChecksPreserveOrderLastValueAndAliases(t *testing.T) {
	rng := rand.New(rand.NewSource(4518))
	names := append([]string{"", "extra", "模型", "a\x00b", "é", "e\u0301", "\xff", "gpt-6-astra"}, subModels...)
	for test := 0; test < 400; test++ {
		target := subTarget{Result: &subResult{Model: "legacy-astra", CheckedAt: 42}}
		if test%3 == 0 {
			target.Result = nil
		}
		if test%4 == 0 {
			target.Models = []subModelResult{}
		}
		for i := 0; i < test%289; i++ {
			name := names[rng.Intn(len(names))]
			var result *subResult
			if i%3 != 0 {
				result = &subResult{Model: name, CheckedAt: int64(i), Availability: subProbe{Message: fmt.Sprint(i), Usage: &subUsage{Status: "matched", InputPrice: usageFloat(float64(i))}}}
			}
			target.Models = append(target.Models, subModelResult{Model: name, State: fmt.Sprint(i % 5), Message: fmt.Sprint(i), Result: result})
		}
		before := mustJSON(target)
		want, got := previousManagementModelChecks(&target), managementModelChecks(&target)
		if !reflect.DeepEqual(got, want) || mustJSON(got) != mustJSON(want) {
			t.Fatalf("case %d changed full checks or wire bytes", test)
		}
		if cap(got) != cap(want) || mustJSON(target) != before {
			t.Fatalf("case %d changed capacity or mutated source", test)
		}
		for i := range got {
			if got[i].Result != want[i].Result {
				t.Fatalf("case %d changed result alias at %d", test, i)
			}
			if got[i].Result != nil {
				got[i].Result.Verdict = "shared mutation"
				if want[i].Result.Verdict != "shared mutation" {
					t.Fatal("result alias behavior changed")
				}
			}
		}
	}
}

var managementModelChecksSink []managementQueryCheck

func BenchmarkManagementModelChecks(b *testing.B) {
	for _, size := range []int{0, 33, 160, 288} {
		target := subTarget{Result: &subResult{Model: "gpt-6-astra", CheckedAt: 42}}
		for i := 0; i < size; i++ {
			name := fmt.Sprintf("model-%03d", i)
			if i < len(subModels) {
				name = subModels[i]
			}
			target.Models = append(target.Models, subModelResult{Model: name, State: "done", Result: &subResult{Model: name}})
		}
		for _, run := range []struct {
			name string
			fn   func(*subTarget) []managementQueryCheck
		}{{"previous", previousManagementModelChecks}, {"indexed", managementModelChecks}} {
			b.Run(fmt.Sprintf("%d/%s", size, run.name), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					managementModelChecksSink = run.fn(&target)
				}
			})
		}
	}
}

package main

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestManagementNormalizeObservedRatesMatchesJSONBaseline(t *testing.T) {
	keys := []string{"gpt-6.1-terra", "", "模型🙂", "<model>&\u2028", "bad\xff", "bad\xfe", "curl_token", "text", "id", "started_at", "request_ids", "request_id", "log_id", "created_at", "attempts", "message", "usage"}
	times := []int64{0, -1, 1, 1<<53 - 1, 1 << 53, 1<<53 + 1, -(1 << 53), -(1<<53 + 1), math.MaxInt64, math.MinInt64}
	rates := []float64{0, math.Copysign(0, -1), .3, -1, math.SmallestNonzeroFloat64, math.MaxFloat64, math.NaN(), math.Inf(1), math.Inf(-1)}
	compare := func(target subTarget) {
		t.Helper()
		before := mustJSON(target)
		want := []subAccount{{Targets: []subTarget{target}}}
		got := []subAccount{{Targets: []subTarget{target}}}
		oldErr, err := previousNormalizeManagementAccounts(want), normalizeManagementAccounts(got)
		if fmt.Sprint(err) != fmt.Sprint(oldErr) || !reflect.DeepEqual(got, want) || mustJSON(got) != mustJSON(want) {
			t.Fatalf("observed rate normalization changed: got=%s err=%v want=%s err=%v", mustJSON(got), err, mustJSON(want), oldErr)
		}
		if mustJSON(target) != before {
			t.Fatal("input metadata was mutated")
		}
	}
	for _, key := range keys {
		for _, at := range times {
			for _, rate := range rates {
				compare(subTarget{GroupID: 7, Rate: .2, ObservedRates: map[string]subObservedRate{key: {Rate: rate, ObservedAt: at}}})
			}
		}
	}
	for _, observed := range []map[string]subObservedRate{nil, {}, {"ordinary": {Rate: .3, ObservedAt: 123}}} {
		for _, mutate := range []func(*subTarget){
			func(*subTarget) {},
			func(v *subTarget) { v.Rate = math.NaN() },
			func(v *subTarget) { v.Billing = &subBilling{Rate: usageFloat(math.Inf(1))} },
			func(v *subTarget) { v.GroupID = math.MaxInt64 },
			func(v *subTarget) {
				v.Result = &subResult{Availability: subProbe{Usage: &subUsage{ActualCost: usageFloat(math.NaN())}}}
			},
		} {
			v := subTarget{GroupID: 7, ObservedRates: observed}
			mutate(&v)
			compare(v)
		}
	}
	rng := rand.New(rand.NewSource(11))
	for range 200 {
		observed := map[string]subObservedRate{}
		for range 20 {
			key := fmt.Sprintf("model-%d-🙂", rng.Intn(30))
			if rng.Intn(3) == 0 {
				key = keys[rng.Intn(len(keys))]
			}
			observed[key] = subObservedRate{Rate: rates[rng.Intn(len(rates))], ObservedAt: times[rng.Intn(len(times))]}
		}
		compare(subTarget{GroupID: 7, ObservedRates: observed})
	}
}

func TestManagementNormalizeObservedRatesCopiesWithoutAliasing(t *testing.T) {
	original := map[string]subObservedRate{"model": {Rate: .3, ObservedAt: 123}}
	accounts := []subAccount{{Targets: []subTarget{{ObservedRates: original}}}}
	if !managementCanCopyObservedRates(original) {
		t.Fatal("ordinary rates did not use the copy path")
	}
	if err := normalizeManagementAccounts(accounts); err != nil {
		t.Fatal(err)
	}
	accounts[0].Targets[0].ObservedRates["model"] = subObservedRate{Rate: 9}
	if original["model"].Rate != .3 {
		t.Fatal("normalized rates alias input")
	}
	for _, key := range []string{"bad\xff", "id", "usage", "attempts"} {
		if managementCanCopyObservedRates(map[string]subObservedRate{key: {Rate: .3}}) {
			t.Fatal("special key skipped the legacy compactor", key)
		}
	}
	// New fields need an explicit preservation decision before bypassing JSON.
	v := reflect.TypeOf(subObservedRate{})
	if v.NumField() != 2 || v.Field(0).Name != "Rate" || v.Field(0).Type.Kind() != reflect.Float64 || v.Field(1).Name != "ObservedAt" || v.Field(1).Type.Kind() != reflect.Int64 {
		t.Fatal("observed rate copy contract needs updating")
	}
}

func BenchmarkManagementNormalizeObservedRates(b *testing.B) {
	for _, entry := range []struct {
		name      string
		normalize func([]subAccount) error
	}{{"previous_json", previousNormalizeManagementAccounts}, {"copied", normalizeManagementAccounts}} {
		b.Run(entry.name, func(b *testing.B) {
			fixture := subTarget{GroupID: 7, Name: "group", State: "done", Rate: .2, Message: strings.Repeat("🙂", 180), ObservedRates: map[string]subObservedRate{}}
			for i := range 80 {
				fixture.ObservedRates[fmt.Sprintf("model-%03d", i)] = subObservedRate{Rate: .3, ObservedAt: 1793196000}
			}
			accounts := []subAccount{{Targets: make([]subTarget, 749)}}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for i := range accounts[0].Targets {
					accounts[0].Targets[i] = fixture
				}
				if err := entry.normalize(accounts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

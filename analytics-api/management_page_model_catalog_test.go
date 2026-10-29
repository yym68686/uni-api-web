package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// Original page implementation retained as an output and allocation baseline.
func previousManagementPageModels(in managementQueryInput, selected string) []string {
	models := append([]string{}, subModels...)
	for _, channel := range in.Channels {
		models = append(models, channel.Models...)
	}
	for _, check := range in.Checks {
		if check.Kind == "model" {
			models = append(models, check.Model)
		}
	}
	for _, account := range in.Accounts {
		for _, target := range account.Targets {
			for _, check := range target.Models {
				models = append(models, check.Model)
			}
		}
	}
	if selected != "" {
		models = append(models, selected)
	}
	return managementUnique(models)
}

func TestManagementPageModelCatalogPreservesAllSourcesAndFirstSeenOrder(t *testing.T) {
	in := managementQueryInput{
		Channels: []managementChannel{{subInstalledChannel: subInstalledChannel{Models: []string{"雪", "", "extra", "雪"}}}},
		Checks:   []configuredCheck{{Kind: "quality", Model: "ignored"}, {Kind: "model", Model: "check"}},
		Accounts: []subAccount{{Targets: []subTarget{{Models: []subModelResult{{Model: "extra"}, {Model: "account"}, {Model: "雪"}}}}}},
	}
	for _, selected := range []string{"", "雪", "selected", "\xff"} {
		want := previousManagementPageModels(in, selected)
		if got := managementPageModelCatalog(in, selected); !reflect.DeepEqual(got, want) {
			t.Fatal("catalog ordering or membership changed", got, want)
		}
	}
	rng := rand.New(rand.NewSource(50))
	names := []string{"", "雪", "extra", "\xff", "a", "A", "gpt-6-astra", "&<>"}
	for round := 0; round < 128; round++ {
		in := managementQueryInput{}
		for i := 0; i < rng.Intn(7); i++ {
			channel := managementChannel{}
			for j := 0; j < rng.Intn(8); j++ {
				channel.Models = append(channel.Models, names[rng.Intn(len(names))])
			}
			in.Channels = append(in.Channels, channel)
		}
		for i := 0; i < rng.Intn(15); i++ {
			in.Checks = append(in.Checks, configuredCheck{Kind: []string{"model", "quality", "tool_use"}[rng.Intn(3)], Model: names[rng.Intn(len(names))]})
		}
		for i := 0; i < rng.Intn(6); i++ {
			account := subAccount{}
			for j := 0; j < rng.Intn(8); j++ {
				target := subTarget{}
				for k := 0; k < rng.Intn(16); k++ {
					target.Models = append(target.Models, subModelResult{Model: names[rng.Intn(len(names))]})
				}
				account.Targets = append(account.Targets, target)
			}
			in.Accounts = append(in.Accounts, account)
		}
		before := mustJSON(in)
		selected := names[rng.Intn(len(names))]
		got, want := managementPageModelCatalog(in, selected), previousManagementPageModels(in, selected)
		if !reflect.DeepEqual(got, want) || mustJSON(in) != before {
			t.Fatal("catalog changed source order, membership, or input", round)
		}
		got[0] = "caller-change"
		if !reflect.DeepEqual(managementPageModelCatalog(in, selected), want) {
			t.Fatal("returned catalog aliases its sources")
		}
	}
}

func BenchmarkManagementPageModelCatalog(b *testing.B) {
	in := managementQueryInput{Accounts: []subAccount{{Targets: make([]subTarget, 850)}}}
	for i := range in.Accounts[0].Targets {
		for j := 0; j < 225; j++ {
			in.Accounts[0].Targets[i].Models = append(in.Accounts[0].Targets[i].Models, subModelResult{Model: fmt.Sprintf("model-%03d", j)})
		}
	}
	for _, tc := range []struct {
		name string
		read func(managementQueryInput, string) []string
	}{{"previous", previousManagementPageModels}, {"candidate", managementPageModelCatalog}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if len(tc.read(in, "selected")) != len(subModels)+226 {
					b.Fatal("catalog incomplete")
				}
			}
		})
	}
}

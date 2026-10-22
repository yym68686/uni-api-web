package main

import (
	"fmt"
	"testing"
)

// Mirrors the observed management cardinality (749 groups, about 120k model
// results, 276 configured channels). This measures row assembly only, never
// substitutes for database/network/browser latency acceptance.
func BenchmarkManagementQueryRowsProductionCardinality(b *testing.B) {
	in := managementQueryInput{}
	names := make([]string, 160)
	for i := range names {
		names[i] = fmt.Sprintf("gpt-fixture-%03d", i)
	}
	for group := 1; group <= 749; group++ {
		account := group / 16
		id := fmt.Sprint(account)
		if len(in.Accounts) == 0 || in.Accounts[len(in.Accounts)-1].ID != id {
			in.Accounts = append(in.Accounts, subAccount{ID: id, Name: id, State: "idle", Targets: []subTarget{}})
		}
		target := subTarget{GroupID: int64(group), Active: true, KeyID: int64(group), State: "done", Models: []subModelResult{}}
		for i, name := range names {
			target.Models = append(target.Models, subModelResult{Model: name, State: "done", Result: &subResult{Model: name, CheckedAt: int64(i), Availability: subProbe{Status: "success"}}})
		}
		last := &in.Accounts[len(in.Accounts)-1]
		last.Targets = append(last.Targets, target)
		if group <= 276 {
			in.Channels = append(in.Channels, managementChannel{subInstalledChannel: subInstalledChannel{SourceID: "source", Provider: fmt.Sprint(group), Models: names, Kind: "configured", BindingStatus: "matched", AccountID: id, GroupID: int64(group)}, Engine: "gpt", AccountIDs: []string{id}})
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rows, err := buildManagementQueryRows(in, names[0], "")
		if err != nil || len(rows) != 1025 {
			b.Fatal("incorrect assembly", len(rows), err)
		}
	}
}

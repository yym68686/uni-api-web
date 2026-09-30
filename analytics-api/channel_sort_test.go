package main

import "testing"

func TestChannelSortMultipliersUseBillingAndExactBindings(t *testing.T) {
	zero, low, high := 0.0, 0.1, 0.8
	refs := []subChannelRef{
		{Account: "a", Group: 1, Rate: &low},
		{Account: "a", Group: 2, Rate: &high},
		{Account: "b", Group: 1, Rate: &zero},
	}
	keys := []subBoundKey{{AccountID: "a", GroupID: 1}, {AccountID: "a", GroupID: 2}}
	channels := []subInstalledChannel{
		{SourceID: "s", Provider: "renamed-999", AccountID: "a", GroupID: 1},
		{SourceID: "other", Provider: "renamed-999", AccountID: "b", GroupID: 1},
		{SourceID: "s", Provider: "unknown-0.01", AccountID: "a", GroupID: 3},
		{SourceID: "s", Provider: "native", Kind: "configured", BindingStatus: "matched", BoundKeys: keys[:1]},
		{SourceID: "s", Provider: "mixed", Kind: "configured", BindingStatus: "matched", BoundKeys: keys},
		{SourceID: "s", Provider: "partial", Kind: "configured", BindingStatus: "partial", BoundKeys: keys[:1]},
		{SourceID: "s", Provider: "unbound", Kind: "configured", BindingStatus: "matched"},
	}
	rates := subChannelMultipliers(channels, refs)
	if rates["s"]["renamed-999"] == nil || *rates["s"]["renamed-999"] != low || rates["s"]["native"] == nil || *rates["s"]["native"] != low {
		t.Fatal("saved billing multiplier missing")
	}
	if rates["other"]["renamed-999"] == nil || *rates["other"]["renamed-999"] != 0 {
		t.Fatal("zero multiplier or source identity lost")
	}
	for _, provider := range []string{"unknown-0.01", "mixed", "partial", "unbound"} {
		if rates["s"][provider] != nil {
			t.Fatalf("%s must have unknown multiplier", provider)
		}
	}
	channels = append(channels, subInstalledChannel{SourceID: "s", Provider: "native", AccountID: "a", GroupID: 2})
	if subChannelMultipliers(channels, refs)["s"]["native"] != nil {
		t.Fatal("conflicting bindings silently picked a multiplier")
	}
}

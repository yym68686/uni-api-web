package main

import (
	"reflect"
	"testing"
)

func TestSubGroupImportBindingsKeepsDialogBindingsAndCompleteMembers(t *testing.T) {
	data := []subInstalledChannel{
		{SourceID: "first", Provider: "imported", AccountID: "owned", GroupID: 7, Models: []string{"a"}, Positions: map[string]int{"a": 2}, Revision: "current", Manageable: true},
		{SourceID: "first", Provider: "other-group", AccountID: "owned", GroupID: 8},
		{SourceID: "first", Provider: "other-account", AccountID: "other", GroupID: 7},
		{SourceID: "second", Provider: "imported", AccountID: "owned", GroupID: 7},
		{SourceID: "first", Provider: "native", Kind: "configured", BindingStatus: "matched", AccountID: "other", GroupID: 8,
			BoundKeys: []subBoundKey{{AccountID: "other", GroupID: 8}, {AccountID: "owned", GroupID: 7}}, Models: []string{"alias"}, ModelMappings: map[string]string{"alias": "upstream"}},
		{SourceID: "first", Provider: "partial", Kind: "configured", BindingStatus: "partial", AccountID: "owned", GroupID: 7, BoundKeys: []subBoundKey{{AccountID: "owned", GroupID: 7}}},
		{SourceID: "first", Provider: "ambiguous", Kind: "configured", BindingStatus: "ambiguous", BoundKeys: []subBoundKey{{AccountID: "owned", GroupID: 7}}},
		{SourceID: "first", Provider: "unmatched", Kind: "configured", BindingStatus: "unmatched", AccountID: "owned", GroupID: 7},
		{SourceID: "first", Provider: "no-account", Kind: "configured", BindingStatus: "no_account", AccountID: "owned", GroupID: 7},
		{SourceID: "first", Provider: "other-native", Kind: "configured", BindingStatus: "matched", BoundKeys: []subBoundKey{{AccountID: "owned", GroupID: 8}}},
	}
	labels := map[string]map[string]string{"first": {"imported": "Imported", "other-group": "Other", "native": "Native", "partial": "Partial"}, "second": {"imported": "Second"}}
	beforeData, beforeLabels := mustJSON(data), mustJSON(labels)
	got, gotLabels := subGroupImportBindings(data, labels, "owned", 7)
	want := []subInstalledChannel{data[0], data[3], data[4]}
	wantLabels := map[string]map[string]string{"first": {"imported": "Imported", "native": "Native"}, "second": {"imported": "Second"}}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotLabels, wantLabels) {
		t.Fatal("dialog's imported/native bindings, order, members or labels changed", got, gotLabels)
	}
	if mustJSON(data) != beforeData || mustJSON(labels) != beforeLabels {
		t.Fatal("scope mutated the complete listing")
	}
	gotLabels["first"]["native"] = "changed"
	if labels["first"]["native"] != "Native" {
		t.Fatal("scoped labels alias the complete listing")
	}
	for _, input := range [][]subInstalledChannel{nil, {}, data} {
		got, gotLabels := subGroupImportBindings(input, labels, "absent", 7)
		if len(got) != 0 || got == nil || len(gotLabels) != 0 || gotLabels == nil {
			t.Fatal("empty group must retain JSON array/object shapes")
		}
	}
}

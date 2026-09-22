package main

import (
	"reflect"
	"testing"
)

func TestRouteMovesPreserveOtherKeysModelsAndDisabledRules(t *testing.T) {
	snapshot := retainedSnapshot{Rules: []retainedRule{
		{KeyID: "one", Model: "astra", Order: []string{"a", "b", "c", "d"}, Disabled: []string{"d"}},
		{KeyID: "one", Model: "sol", Order: []string{"d", "c", "b", "a"}},
		{KeyID: "two", Model: "astra", Order: []string{"a", "b", "c", "d"}},
	}}
	catalog := []routeMove{}
	for _, model := range []string{"astra", "sol"} {
		order := []string{"a", "b", "c", "d"}
		if model == "sol" {
			order = []string{"d", "c", "b", "a"}
		}
		for _, p := range order {
			catalog = append(catalog, routeMove{Provider: p, Model: model})
		}
	}
	err := applyRouteMoves(&snapshot, "one", catalog, []routeMove{{Provider: "c", Model: "astra", Position: 1}, {Provider: "c", Model: "sol", Position: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Rules[0].Order, []string{"c", "a", "b", "d"}) || !reflect.DeepEqual(snapshot.Rules[1].Order, []string{"d", "b", "a", "c"}) || !reflect.DeepEqual(snapshot.Rules[2].Order, []string{"a", "b", "c", "d"}) || !reflect.DeepEqual(snapshot.Rules[0].Disabled, []string{"d"}) {
		t.Fatal(snapshot)
	}
	before := mustJSON(snapshot)
	for _, moves := range [][]routeMove{
		{{Provider: "c", Model: "astra", Position: 1}, {Provider: "a", Model: "astra", Position: 1}},
		{{Provider: "unknown", Model: "astra", Position: 1}},
		{{Provider: "c", Model: "astra", Position: 0}},
		{{Provider: "c", Model: "astra", Position: 5}},
	} {
		if applyRouteMoves(&snapshot, "one", catalog, moves) == nil {
			t.Fatal("invalid move accepted")
		}
		if mustJSON(snapshot) != before {
			t.Fatal("invalid edit changed routes")
		}
	}
}

package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type batchFixture struct {
	Snapshot  retainedSnapshot
	Input     channelBatchInput
	Catalogs  map[string][]batchCatalogRow
	Documents map[string]map[string]any
}

// Captured routing shape only: provider/key identifiers are synthetic, and no
// upstream addresses, credentials, definitions or account data are retained.
func loadBatchFixture(t *testing.T, name string) batchFixture {
	t.Helper()
	var raw struct {
		Rules    [][]json.RawMessage            `json:"rules"`
		Catalogs map[string]map[string][]string `json:"catalogs"`
		Targets  []channelBatchTarget           `json:"targets"`
	}
	data, err := os.ReadFile("testdata/batch-" + name + ".json")
	if err != nil || json.Unmarshal(data, &raw) != nil {
		t.Fatal(err)
	}
	f := batchFixture{Snapshot: retainedSnapshot{Version: 2, Channels: []retainedChannel{}, Settings: map[string]json.RawMessage{}}, Input: channelBatchInput{Part: "models", Targets: raw.Targets}, Catalogs: map[string][]batchCatalogRow{}, Documents: map[string]map[string]any{}}
	for _, values := range raw.Rules {
		var rule retainedRule
		if len(values) != 4 {
			t.Fatal("invalid fixture rule")
		}
		for i, dst := range []any{&rule.KeyID, &rule.Model, &rule.Order, &rule.Disabled} {
			if json.Unmarshal(values[i], dst) != nil {
				t.Fatal("invalid fixture field")
			}
		}
		f.Snapshot.Rules = append(f.Snapshot.Rules, rule)
	}
	for _, target := range raw.Targets {
		f.Documents[target.Key+"\n"+target.Provider] = map[string]any{"provider": target.Provider, "engine": "gpt", "base_url": "https://fixture.test/v1", "api": "fixture", "model": []any{}}
		for model, order := range raw.Catalogs[target.Key] {
			for _, p := range order {
				up := model
				if p == target.Provider && target.Current[model] != "" {
					up = target.Current[model]
				}
				f.Catalogs[target.Key] = append(f.Catalogs[target.Key], batchCatalogRow{Provider: p, Model: model, Upstream: up})
			}
		}
	}
	return f
}

func TestBatchCompactionKeepsModelOrdersAndDisabledPolicies(t *testing.T) {
	for _, name := range []string{"primary", "digitalocean"} {
		t.Run(name, func(t *testing.T) {
			fixture := loadBatchFixture(t, name)
			original := mustJSON(fixture.Snapshot)
			before := len(fixture.Snapshot.Rules)
			changed, err := buildBatchSnapshot(&fixture.Snapshot, fixture.Input, fixture.Catalogs, fixture.Documents)
			if err != nil || !changed {
				t.Fatal(changed, err)
			}
			if len(fixture.Snapshot.Rules) > 128 {
				t.Fatalf("scope budget exceeded: %d", len(fixture.Snapshot.Rules))
			}
			t.Logf("%d keys, rules %d -> %d (limit 128)", len(fixture.Input.Targets), before, len(fixture.Snapshot.Rules))
			var old retainedSnapshot
			_ = json.Unmarshal([]byte(original), &old)
			for _, rule := range old.Rules {
				touched := false
				for _, target := range fixture.Input.Targets {
					if target.Key == rule.KeyID {
						_, touched = target.Positions[rule.Model]
						break
					}
				}
				found := false
				for _, current := range fixture.Snapshot.Rules {
					if current.KeyID == rule.KeyID && current.Model == rule.Model {
						found = true
						for _, p := range rule.Disabled {
							if !containsProvider(current.Disabled, p) {
								t.Fatal("disabled policy lost")
							}
						}
						if !touched && rule.Model != "" && !reflect.DeepEqual(rule.Order, current.Order) {
							t.Fatal("untouched model order changed")
						}
					}
				}
				if !found && (!touched || len(rule.Disabled) > 0) {
					t.Fatal("unrelated rule removed")
				}
			}
			for _, target := range fixture.Input.Targets {
				copy := configuredImportName(target.Origin, target.Key)
				for model, want := range target.Positions {
					providers := []string{}
					for _, row := range fixture.Catalogs[target.Key] {
						if row.Model == model {
							providers = append(providers, row.Provider)
						}
					}
					var order []string
					for _, r := range fixture.Snapshot.Rules {
						if r.KeyID == target.Key && r.Model == "" {
							order = r.Order
						}
					}
					for _, r := range fixture.Snapshot.Rules {
						if r.KeyID == target.Key && r.Model == model && len(r.Order) > 0 {
							order = r.Order
						}
					}
					result := projectedRouteOrder(providers, order)
					if result[want-1] != copy {
						t.Fatalf("%s %s wanted position %d: %v", target.Key, model, want, result)
					}
				}
			}
		})
	}
}

func TestBatchCompactionRetainsConflictingAndDisabledModelRules(t *testing.T) {
	s := retainedSnapshot{Rules: []retainedRule{
		{KeyID: "k", Model: "a", Order: []string{"p", "q"}, Disabled: []string{"q"}},
		{KeyID: "k", Model: "b", Order: []string{"p", "q"}},
		{KeyID: "k", Model: "c", Order: []string{"q", "p"}},
	}}
	cat := map[string][]batchCatalogRow{"k": {}}
	for _, m := range []string{"a", "b", "c", "untouched"} {
		cat["k"] = append(cat["k"], batchCatalogRow{Provider: "p", Model: m}, batchCatalogRow{Provider: "q", Model: m})
	}
	compactBatchRouteRules(&s, cat, map[string][]routeMove{"k": {{Model: "a"}, {Model: "b"}, {Model: "c"}}})
	if len(s.Rules) != 3 {
		t.Fatal(s.Rules)
	}
	for _, r := range s.Rules {
		if r.Model == "a" && (!reflect.DeepEqual(r.Disabled, []string{"q"}) || len(r.Order) != 0) {
			t.Fatal(r)
		}
		if r.Model == "c" && !reflect.DeepEqual(r.Order, []string{"q", "p"}) {
			t.Fatal(r)
		}
		if r.Model == "" && !reflect.DeepEqual(r.Order, []string{"p", "q"}) {
			t.Fatal(r)
		}
	}
}

func TestSingleModelCompactionRequiresSafeExistingKeyScope(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		wildcard, conflicting bool
	}{
		{"existing-key-order", true, false},
		{"no-key-order", false, false},
		{"conflicting-order", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := retainedSnapshot{Rules: []retainedRule{
				{KeyID: "k", Model: "other-model", Disabled: []string{"b"}},
				{KeyID: "other-key", Model: "selected", Order: []string{"b", "a"}},
			}}
			if tc.wildcard {
				snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: "k", Order: []string{"a", "b"}, Disabled: []string{"a"}})
			}
			order := []string{"new", "a", "b"}
			if tc.conflicting {
				order = []string{"new", "b", "a"}
			}
			snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: "k", Model: "selected", Order: order})
			before := mustJSON(snapshot)
			count := len(snapshot.Rules)
			catalog := map[string][]batchCatalogRow{"k": {
				{Provider: "a", Model: "selected"}, {Provider: "b", Model: "selected"}, {Provider: "new", Model: "selected"},
				{Provider: "a", Model: "other-model"}, {Provider: "b", Model: "other-model"},
			}}
			compactBatchRouteRules(&snapshot, catalog, map[string][]routeMove{"k": {{Model: "selected"}}})
			if !tc.wildcard || tc.conflicting {
				if mustJSON(snapshot) != before {
					t.Fatal("unsafe or non-saving compaction changed the snapshot")
				}
				return
			}
			if len(snapshot.Rules) != count-1 {
				t.Fatal("single model did not release a scope")
			}
			if !reflect.DeepEqual(snapshot.Rules[0].Disabled, []string{"b"}) || !reflect.DeepEqual(snapshot.Rules[1].Order, []string{"b", "a"}) || !reflect.DeepEqual(snapshot.Rules[2].Disabled, []string{"a"}) {
				t.Fatal("disabled policy or other key changed")
			}
			if !reflect.DeepEqual(snapshot.Rules[2].Order, []string{"new", "a", "b"}) {
				t.Fatal("requested model order changed")
			}
			if !reflect.DeepEqual(projectedRouteOrder([]string{"a", "b"}, snapshot.Rules[2].Order), []string{"a", "b"}) {
				t.Fatal("other model order changed")
			}
		})
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// Keep the original nested scan independent of the new index as an oracle.
func legacySubChannelPositions(state subGatewayControls, key, provider string, models []string) map[string]int {
	positions := map[string]int{}
	for _, model := range models {
		for _, scope := range [][2]string{{key, model}, {key, ""}, {"", model}, {"", ""}} {
			found := false
			for _, rule := range state.Rules {
				if rule.KeyID == scope[0] && rule.Model == scope[1] && len(rule.Order) > 0 {
					for n, name := range rule.Order {
						if name == provider {
							positions[model] = n + 1
						}
					}
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	return positions
}

func appendSubChannelPositionRule(state *subGatewayControls, key, model string, order []string) {
	state.Rules = append(state.Rules, struct {
		KeyID string   `json:"api_key_id"`
		Model string   `json:"model"`
		Order []string `json:"order"`
	}{key, model, order})
}

func TestSubChannelRuleIndexScopeSemantics(t *testing.T) {
	state := subGatewayControls{}
	appendSubChannelPositionRule(&state, "", "", []string{"provider", "global"})
	appendSubChannelPositionRule(&state, "", "model-global", []string{"peer", "provider"})
	appendSubChannelPositionRule(&state, "key", "", []string{"a", "b", "provider"})
	appendSubChannelPositionRule(&state, "key", "specific", nil)
	appendSubChannelPositionRule(&state, "key", "specific", []string{"provider", "peer", "provider"})
	appendSubChannelPositionRule(&state, "key", "specific", []string{"peer", "provider"})
	appendSubChannelPositionRule(&state, "key", "omitted", []string{"peer"})
	appendSubChannelPositionRule(&state, "key", "omitted", []string{"provider"})
	appendSubChannelPositionRule(&state, "key", "empty", []string{})
	before, _ := json.Marshal(state)
	index := newSubChannelRuleIndex(state)
	for _, tc := range []struct {
		name, key, provider string
		models              []string
		want                map[string]int
	}{
		{"specific-last-provider-first-rule", "key", "provider", []string{"specific"}, map[string]int{"specific": 3}},
		{"scope-omits-provider-stops-fallback", "key", "provider", []string{"omitted"}, map[string]int{}},
		{"empty-rule-falls-back-to-key", "key", "provider", []string{"empty"}, map[string]int{"empty": 3}},
		{"key-precedes-global-model", "key", "provider", []string{"model-global"}, map[string]int{"model-global": 3}},
		{"global-model-precedes-default", "other", "provider", []string{"model-global"}, map[string]int{"model-global": 2}},
		{"default", "other", "provider", []string{"other"}, map[string]int{"other": 1}},
		{"missing-provider", "key", "missing", []string{"specific", "empty"}, map[string]int{}},
		{"duplicate-models-and-empty-scope", "", "provider", []string{"", "model-global", "model-global"}, map[string]int{"": 1, "model-global": 2}},
		{"nil-models-still-nonnull-map", "key", "provider", nil, map[string]int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := legacySubChannelPositions(state, tc.key, tc.provider, tc.models)
			got := index.positions(tc.key, tc.provider, tc.models)
			if !reflect.DeepEqual(got, old) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v legacy %v want %v", got, old, tc.want)
			}
		})
	}
	// Neither construction nor returned maps may mutate the shared response.
	index.positions("key", "provider", []string{"specific"})["specific"] = 999
	if index.positions("key", "provider", []string{"specific"})["specific"] != 3 {
		t.Fatal("caller map mutates index")
	}
	after, _ := json.Marshal(state)
	if string(after) != string(before) {
		t.Fatal("gateway response mutated")
	}
	if got := newSubChannelRuleIndex(subGatewayControls{}).positions("", "", []string{""}); got == nil || len(got) != 0 {
		t.Fatal("empty response must retain an empty, nonnull positions object")
	}
}

func TestSubChannelRuleIndexMatchesLegacyGenerated(t *testing.T) {
	rng := rand.New(rand.NewSource(93021))
	keys := []string{"", "key", "other", "key\nmodel", "中文", "\xff"}
	models := []string{"", "model", "other", "model\nkey", "模型", "\xfe"}
	providers := []string{"", "provider", "peer", "渠道", "\xff"}
	for trial := 0; trial < 1000; trial++ {
		state := subGatewayControls{}
		for n := rng.Intn(75); n > 0; n-- {
			var order []string
			if rng.Intn(2) == 0 {
				order = []string{}
			}
			for m := rng.Intn(9); m > 0; m-- {
				order = append(order, providers[rng.Intn(len(providers))])
			}
			appendSubChannelPositionRule(&state, keys[rng.Intn(len(keys))], models[rng.Intn(len(models))], order)
		}
		index := newSubChannelRuleIndex(state)
		for _, key := range keys {
			for _, provider := range providers {
				want := legacySubChannelPositions(state, key, provider, models)
				got := index.positions(key, provider, models)
				if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(scanSubChannelPositions(state, key, provider, models), want) {
					t.Fatalf("trial %d key %q provider %q got %v want %v", trial, key, provider, got, want)
				}
			}
		}
	}
}

func TestSubChannelPositionsForOwned(t *testing.T) {
	for _, count := range []int{0, 1, 2, 8} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			state := subGatewayControls{}
			owned := map[string]subChannelRef{}
			order := []string{}
			for i := 0; i < count+2; i++ {
				provider := fmt.Sprint(i)
				state.Temporary = append(state.Temporary, struct {
					Provider        string   `json:"provider"`
					IdentityChanged bool     `json:"identity_changed"`
					KeyID           string   `json:"api_key_id"`
					Models          []string `json:"models"`
				}{provider, i == count+1, "key", []string{"model", "", "other"}})
				order = append(order, provider)
				if i != count {
					owned[provider] = subChannelRef{}
				}
			}
			appendSubChannelPositionRule(&state, "key", "", order)
			appendSubChannelPositionRule(&state, "key", "model", []string{"peer"})
			got := subChannelPositionsForOwned(state, owned)
			if len(got) != len(state.Temporary) {
				t.Fatal("channel alignment lost")
			}
			for i, channel := range state.Temporary {
				var want map[string]int
				if i < count {
					want = legacySubChannelPositions(state, channel.KeyID, channel.Provider, channel.Models)
				}
				if !reflect.DeepEqual(got[i], want) {
					t.Fatalf("channel %d got %v want %v", i, got[i], want)
				}
			}
		})
	}
}

var subChannelPositionsBenchmarkSink map[string]int

func BenchmarkSubChannelRulePositions(b *testing.B) {
	for _, channels := range []int{1, 290} {
		b.Run(fmt.Sprintf("channels-%d_models-33_rules-132", channels), func(b *testing.B) {
			state := subGatewayControls{}
			owned := map[string]subChannelRef{}
			providers := make([]string, channels)
			models := make([]string, 33)
			for i := range providers {
				providers[i] = fmt.Sprintf("provider-%03d", i)
				owned[providers[i]] = subChannelRef{}
			}
			for i := range models {
				models[i] = fmt.Sprintf("model-%03d", i)
			}
			for key := 0; key < 4; key++ {
				for _, model := range models {
					appendSubChannelPositionRule(&state, fmt.Sprintf("key-%d", key), model, providers)
				}
			}
			for i, provider := range providers {
				state.Temporary = append(state.Temporary, struct {
					Provider        string   `json:"provider"`
					IdentityChanged bool     `json:"identity_changed"`
					KeyID           string   `json:"api_key_id"`
					Models          []string `json:"models"`
				}{provider, false, fmt.Sprintf("key-%d", i%4), models})
			}
			b.Run("legacy", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for _, channel := range state.Temporary {
						subChannelPositionsBenchmarkSink = legacySubChannelPositions(state, channel.KeyID, channel.Provider, channel.Models)
					}
				}
			})
			b.Run("adaptive-including-build", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					for _, positions := range subChannelPositionsForOwned(state, owned) {
						subChannelPositionsBenchmarkSink = positions
					}
				}
			})
		})
	}
}

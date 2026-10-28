package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// Independent copy of the previous listing's lookup, including key/ref loop
// order, protocol expansion and duplicate overwrites.
func legacyInstalledChannelRefs(refs []subChannelRef, keys []string) map[string]subChannelRef {
	out := map[string]subChannelRef{}
	for _, key := range keys {
		for _, ref := range refs {
			for _, provider := range subProviderNames(ref.Account, ref.Group, key) {
				out[provider] = ref
			}
		}
	}
	return out
}

func TestInstalledChannelRefsPreserveLiveLookupAndPositions(t *testing.T) {
	rng := rand.New(rand.NewSource(92071))
	for iteration := 0; iteration < 100; iteration++ {
		refs := []subChannelRef{}
		keys := []string{"", "key-one", "key-two", "key-one", "中文🔑"}
		for n := 0; n < 30; n++ {
			refs = append(refs, subChannelRef{Account: fmt.Sprintf("a-%d", rng.Intn(8)), Group: int64(rng.Intn(6) - 1), Name: fmt.Sprint(n), Base: "https://fixture.test"})
		}
		// The later duplicate wins even when it has a different display label.
		duplicate := refs[0]
		duplicate.Name = "last duplicate"
		refs = append(refs, duplicate)
		state := subGatewayControls{}
		for n := 0; n < 90; n++ {
			ref, key := refs[rng.Intn(len(refs))], keys[rng.Intn(len(keys))]
			names := subProviderNames(ref.Account, ref.Group, key)
			provider := names[rng.Intn(len(names))]
			if n%11 == 0 {
				provider += "--unknown"
			}
			if n%13 == 0 {
				provider += "--responses"
			}
			channel := subGatewayControls{}.Temporary
			channel = append(channel, struct {
				Provider        string   `json:"provider"`
				IdentityChanged bool     `json:"identity_changed"`
				KeyID           string   `json:"api_key_id"`
				Models          []string `json:"models"`
			}{Provider: provider, KeyID: keys[(n+1)%len(keys)], Models: []string{"m", "", "m"}, IdentityChanged: n%7 == 0})
			state.Temporary = append(state.Temporary, channel...)
		}
		// Exercise all protocol names of an overwritten ref and mismatching
		// channel KeyIDs. Ownership must NOT be narrowed to the channel key.
		for _, provider := range subProviderNames(duplicate.Account, duplicate.Group, keys[1]) {
			channel := state.Temporary[1]
			channel.Provider, channel.KeyID, channel.IdentityChanged = provider, "not-in-keys", false
			state.Temporary = append(state.Temporary, channel)
		}
		for n := 0; n < 20; n++ {
			rule := struct {
				KeyID string   `json:"api_key_id"`
				Model string   `json:"model"`
				Order []string `json:"order"`
			}{KeyID: keys[n%len(keys)], Model: []string{"", "m"}[n%2]}
			for k := 0; k < 15; k++ {
				rule.Order = append(rule.Order, state.Temporary[rng.Intn(len(state.Temporary))].Provider)
			}
			state.Rules = append(state.Rules, rule)
		}
		before := mustJSON(struct {
			Refs  []subChannelRef
			State subGatewayControls
			Keys  []string
		}{refs, state, keys})
		legacy := legacyInstalledChannelRefs(refs, keys)
		want := map[string]subChannelRef{}
		for _, channel := range state.Temporary {
			if ref, found := legacy[channel.Provider]; found && !channel.IdentityChanged {
				want[channel.Provider] = ref
			}
		}
		got := subInstalledChannelRefs(state, refs, keys)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("live provider ownership changed", iteration)
		}
		if !reflect.DeepEqual(subChannelPositionsForOwned(state, legacy), subChannelPositionsForOwned(state, got)) {
			t.Fatal("live positions changed", iteration)
		}
		if after := mustJSON(struct {
			Refs  []subChannelRef
			State subGatewayControls
			Keys  []string
		}{refs, state, keys}); after != before {
			t.Fatal("input changed", iteration)
		}
	}
	if got := subInstalledChannelRefs(subGatewayControls{}, nil, nil); got == nil || len(got) != 0 {
		t.Fatal("empty lookup must remain nonnil and empty")
	}
}

func TestInstalledChannelRefsKeepCrossKeyTupleOverwrite(t *testing.T) {
	refs := []subChannelRef{{Account: "a", Group: 1, Name: "first"}, {Account: "a:1:k", Group: 2, Name: "last"}}
	keys := []string{"k:2:x", "x"}
	// The old delimiter-based identity can collide even without a hash
	// collision. Retain its key-outer/ref-inner last-value behavior.
	provider := subProviderName("a", 1, keys[0])
	if provider != subProviderName(refs[1].Account, 2, keys[1]) {
		t.Fatal("invalid collision fixture")
	}
	var state subGatewayControls
	if err := decodeMap(map[string]any{"temporary_channels": []any{map[string]any{"provider": provider, "api_key_id": "unknown"}}}, &state); err != nil {
		t.Fatal(err)
	}
	got := subInstalledChannelRefs(state, refs, keys)
	if !reflect.DeepEqual(got[provider], legacyInstalledChannelRefs(refs, keys)[provider]) || got[provider].Name != "last" {
		t.Fatal("cross-key overwrite changed")
	}
}

var installedChannelRefsBenchmark map[string]subChannelRef

func BenchmarkInstalledChannelRefs(b *testing.B) {
	refs, channels := providerReferenceFixture()
	keys := []string{}
	state := subGatewayControls{}
	for n := 0; n < 8; n++ {
		keys = append(keys, fmt.Sprintf("caller-%d", n))
	}
	for _, channel := range channels {
		item := struct {
			Provider        string   `json:"provider"`
			IdentityChanged bool     `json:"identity_changed"`
			KeyID           string   `json:"api_key_id"`
			Models          []string `json:"models"`
		}{Provider: channel.Provider, KeyID: channel.KeyID}
		state.Temporary = append(state.Temporary, item)
	}
	for _, name := range []string{"previous_full", "live_only", "no_temporary"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				switch name {
				case "previous_full":
					installedChannelRefsBenchmark = legacyInstalledChannelRefs(refs, keys)
				case "live_only":
					installedChannelRefsBenchmark = subInstalledChannelRefs(state, refs, keys)
				case "no_temporary":
					installedChannelRefsBenchmark = subInstalledChannelRefs(subGatewayControls{}, refs, keys)
				}
			}
		})
	}
}

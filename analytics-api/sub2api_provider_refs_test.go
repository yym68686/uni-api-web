package main

import (
	"fmt"
	"reflect"
	"testing"
)

func providerReferenceFixture() ([]subChannelRef, []retainedChannel) {
	refs := make([]subChannelRef, 749)
	for n := range refs {
		refs[n] = subChannelRef{Account: fmt.Sprintf("account-%d", n/20), Group: int64(n%20 + 1), Base: fmt.Sprintf("https://site%d.example/tenant", n/20)}
	}
	var channels []retainedChannel
	for n := 0; n < 276; n++ {
		ref := refs[n*7%len(refs)]
		key := fmt.Sprintf("caller-%d", n%8)
		names := subProviderNames(ref.Account, ref.Group, key)
		channels = append(channels, retainedChannel{Provider: names[n%len(names)], KeyID: key, Base: ref.Base + "/v1/responses"})
	}
	return refs, channels
}

func scanProviderRefs(refs []subChannelRef, c retainedChannel) []subChannelRef {
	var matches []subChannelRef
	for _, ref := range refs {
		if matchesSubProvider(c.Provider, ref.Account, ref.Group, c.KeyID) && subBindingSite(c.Base) == subBindingSite(ref.Base) {
			matches = append(matches, ref)
		}
	}
	return matches
}

func TestProviderRefIndexPreservesNamesSitesAndCandidateOrder(t *testing.T) {
	refs, channels := providerReferenceFixture()
	// Duplicate identities and distinct tenant paths must retain the original
	// ordered candidate semantics, even though real IDs are ordinarily unique.
	refs = append(refs, refs[0], subChannelRef{Account: refs[0].Account, Group: refs[0].Group, Base: "https://site0.example/other-tenant"})
	for _, name := range append(subProviderNames(refs[0].Account, refs[0].Group, "caller-0"), subProviderName(refs[0].Account, refs[0].Group, "caller-0")+"--unknown") {
		channels = append(channels, retainedChannel{Provider: name, KeyID: "caller-0", Base: refs[0].Base + "/v1/responses"}, retainedChannel{Provider: name, KeyID: "wrong-caller", Base: refs[0].Base}, retainedChannel{Provider: name, KeyID: "caller-0", Base: "https://site0.example/other-tenant"})
	}
	index := newSubProviderRefIndex(refs)
	for _, c := range channels {
		if got, want := index.matches(c.Provider, c.KeyID, c.Base), scanProviderRefs(refs, c); !reflect.DeepEqual(got, want) {
			t.Fatal("binding changed", c.Provider, got, want)
		}
	}
	if got := newSubProviderRefIndex(nil).matches("unknown", "key", "https://example.test"); len(got) != 0 {
		t.Fatal(got)
	}
}

var providerRefBenchmarkCount int

func BenchmarkProviderRefJoin(b *testing.B) {
	refs, channels := providerReferenceFixture()
	for _, indexed := range []bool{false, true} {
		b.Run(fmt.Sprintf("indexed=%t", indexed), func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				index := newSubProviderRefIndex(refs)
				count := 0
				for _, c := range channels {
					if indexed {
						count += len(index.matches(c.Provider, c.KeyID, c.Base))
					} else {
						count += len(scanProviderRefs(refs, c))
					}
				}
				providerRefBenchmarkCount = count
			}
		})
	}
}

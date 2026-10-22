package main

import "strings"

// Build names once per caller key during this read. The old binding join
// rehashed every account/group for every saved channel (and again for each
// account summary). Keep candidate order and all site matches exactly intact.
type subProviderRefIndex struct {
	refs  []subChannelRef
	byKey map[string]map[string][]int
	sites []string
}

func newSubProviderRefIndex(refs []subChannelRef) *subProviderRefIndex {
	sites := make([]string, len(refs))
	for n, ref := range refs {
		sites[n] = subBindingSite(ref.Base)
	}
	return &subProviderRefIndex{refs: refs, byKey: map[string]map[string][]int{}, sites: sites}
}

func (i *subProviderRefIndex) matches(provider, key, base string) []subChannelRef {
	root := provider
	for _, spec := range channelProtocols {
		if strings.HasSuffix(root, "--"+spec.Protocol) {
			root = strings.TrimSuffix(root, "--"+spec.Protocol)
			break
		}
	}
	lookup, ok := i.byKey[key]
	if !ok {
		lookup = map[string][]int{}
		for n, ref := range i.refs {
			name := subProviderName(ref.Account, ref.Group, key)
			lookup[name] = append(lookup[name], n)
		}
		i.byKey[key] = lookup
	}
	var matched []subChannelRef
	site := subBindingSite(base)
	for _, n := range lookup[root] {
		if i.sites[n] == site {
			matched = append(matched, i.refs[n])
		}
	}
	return matched
}

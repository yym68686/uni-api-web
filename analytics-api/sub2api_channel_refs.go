package main

import "strings"

// Only live, unchanged temporary providers are queried by the listing below.
// Hash each known key/ref in the original order, but do not allocate all five
// protocol names (or retain unrelated roots). Keep the last matching ref even
// when the channel's KeyID differs: the legacy lookup used every known key.
func subInstalledChannelRefs(state subGatewayControls, refs []subChannelRef, keys []string) map[string]subChannelRef {
	providers := make(map[string]string, len(state.Temporary))
	roots := make(map[string]subChannelRef)
	wanted := make(map[string]bool, len(state.Temporary))
	for _, channel := range state.Temporary {
		if channel.IdentityChanged {
			continue
		}
		root := channel.Provider
		for _, spec := range channelProtocols {
			if strings.HasSuffix(root, "--"+spec.Protocol) {
				root = strings.TrimSuffix(root, "--"+spec.Protocol)
				break
			}
		}
		providers[channel.Provider] = root
		wanted[root] = true
	}
	if len(wanted) > 0 {
		for _, key := range keys {
			for _, ref := range refs {
				root := subProviderName(ref.Account, ref.Group, key)
				if wanted[root] {
					roots[root] = ref
				}
			}
		}
	}
	out := make(map[string]subChannelRef, len(roots))
	for provider, root := range providers {
		if ref, found := roots[root]; found {
			out[provider] = ref
		}
	}
	return out
}

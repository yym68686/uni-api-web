package main

import (
	"context"
	"maps"
	"slices"
)

// Imported provider IDs bind a site/account/group to the destination caller.
// Neither a display name nor an upstream host alone identifies that group.
func tierImportKeys(state subGatewayControls, providers []configuredProvider) map[string]string {
	eligible := map[string]bool{}
	for _, p := range providers {
		if p.Temporary && !p.IdentityChanged && generatedSubProvider(p.Provider) {
			eligible[p.Provider] = true
		}
	}
	keys := map[string]string{}
	for _, channel := range state.Temporary {
		if eligible[channel.Provider] && !channel.IdentityChanged && channel.KeyID != "" {
			keys[channel.Provider] = channel.KeyID
		}
	}
	return keys
}

func (s *Service) addObservationGroupTiers(ctx context.Context, members []managementChannel, refs []subChannelRef) error {
	type group struct {
		account string
		id      int64
	}
	index := newSubProviderRefIndex(refs)
	wanted := map[group]map[string]bool{}
	attached := make([]group, len(members))
	for i := range members {
		member := &members[i]
		if member.KeyID != "" && subBindingSite(member.Base) != "" {
			if matches := index.matches(member.Provider, member.KeyID, member.Base); len(matches) == 1 {
				g := group{matches[0].Account, matches[0].Group}
				attached[i] = g
				if wanted[g] == nil {
					wanted[g] = map[string]bool{}
				}
				for _, model := range member.Models {
					upstream := member.ModelMappings[model]
					if upstream == "" {
						upstream = model
					}
					wanted[g][upstream] = true
				}
			}
		}
		member.Base, member.KeyID, member.ProbeFingerprint = "", "", ""
	}
	results := make(map[group]map[string]map[string]subTierProbe, len(wanted))
	for g, models := range wanted {
		var err error
		results[g], err = s.groupTierResults(ctx, g.account, g.id, slices.Collect(maps.Keys(models)))
		if err != nil {
			return err
		}
	}
	for i, g := range attached {
		member := &members[i]
		for _, model := range member.Models {
			upstream := member.ModelMappings[model]
			if upstream == "" {
				upstream = model
			}
			if tiers := results[g][upstream]; len(tiers) != 0 {
				if member.GroupTierResults == nil {
					member.GroupTierResults = map[string]map[string]subTierProbe{}
				}
				member.GroupTierResults[model] = tiers
			}
		}
	}
	return nil
}

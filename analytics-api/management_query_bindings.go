package main

import (
	"context"
	"sort"
)

// Resolve live provider credentials against the owner's published key index.
// The periodic inventory is intentionally absent from this read path: its
// previous credential generation cannot establish current account ownership.
func (s *Service) managementBindingsFrom(ctx context.Context, db controlQueryDB, owner string, facts []managementGatewayFacts) ([]subInstalledChannel, error) {
	index, err := s.readSubBindingIndexFrom(ctx, db, owner)
	if err != nil {
		return nil, err
	}
	out := []subInstalledChannel{}
	for _, source := range facts {
		for _, provider := range source.Providers {
			if provider.Temporary && !provider.IdentityChanged {
				continue
			}
			site, hashes := subBindingSite(provider.Base), configuredKeyHashes(provider)
			if provider.Provider == "" || site == "" || len(hashes) == 0 {
				continue
			}
			item := subInstalledChannel{Kind: "configured", SourceID: source.Source.ID, SourceName: source.Source.Name, Provider: provider.Provider, Base: site, Models: []string{}, Positions: map[string]int{}, BoundKeys: []subBoundKey{}, Fingerprint: tokenHash(controlTarget(source.Source) + "\n" + provider.Provider + "\n" + site + "\n" + mustJSON(hashes))}
			out = append(out, index.resolve(item, site, hashes))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return qualityChannelID(out[i].SourceID, out[i].Provider) < qualityChannelID(out[j].SourceID, out[j].Provider)
	})
	return out, nil
}

func managementQualityBindings(bindings []subInstalledChannel, refs []subChannelRef, facts []managementGatewayFacts) ([]qualityBinding, error) {
	byChannel := map[string]qualityBinding{}
	for _, binding := range bindings {
		if binding.AccountID != "" && binding.GroupID > 0 && binding.BindingStatus == "matched" {
			byChannel[qualityChannelID(binding.SourceID, binding.Provider)] = qualityBinding{binding.SourceID, binding.Provider, binding.AccountID, binding.GroupID}
		}
	}
	index := newSubProviderRefIndex(refs)
	for _, source := range facts {
		var controls subGatewayControls
		if err := decodeMap(source.Controls, &controls); err != nil {
			return nil, errManagementSourceContract
		}
		providers := map[string]configuredProvider{}
		keys := map[string]bool{}
		for _, key := range source.Keys {
			keys[key.ID] = true
		}
		for _, provider := range source.Providers {
			providers[provider.Provider] = provider
		}
		for _, channel := range controls.Temporary {
			provider, ok := providers[channel.Provider]
			if !ok || !keys[channel.KeyID] || !provider.Temporary || channel.IdentityChanged || provider.IdentityChanged {
				continue
			}
			matches := index.matches(channel.Provider, channel.KeyID, provider.Base)
			if len(matches) == 1 {
				ref := matches[0]
				byChannel[qualityChannelID(source.Source.ID, channel.Provider)] = qualityBinding{source.Source.ID, channel.Provider, ref.Account, ref.Group}
			}
		}
	}
	out := make([]qualityBinding, 0, len(byChannel))
	for _, binding := range byChannel {
		out = append(out, binding)
	}
	sort.Slice(out, func(i, j int) bool {
		return qualityChannelID(out[i].Source, out[i].Provider) < qualityChannelID(out[j].Source, out[j].Provider)
	})
	return out, nil
}

func (s *controlStore) managementSourcesFrom(ctx context.Context, db controlQueryDB) ([]controlSource, error) {
	views, err := s.listSourcesFrom(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]controlSource, 0, len(views))
	for _, view := range views {
		source, err := s.sourceFrom(ctx, db, view.ID)
		if err != nil {
			return nil, err
		}
		if source.configKeyError != nil {
			return nil, errManagementSourceContract
		}
		out = append(out, source)
	}
	return out, nil
}

// Check both identity and membership; adding or disabling a source invalidates
// the complete view just as rotating a credential does.
func verifyManagementSources(sources []controlSource, facts []managementGatewayFacts) error {
	if len(sources) != len(facts) {
		return errManagementSourceRevision
	}
	byID := map[string]string{}
	for _, source := range facts {
		if _, exists := byID[source.Source.ID]; exists {
			return errManagementSourceContract
		}
		byID[source.Source.ID] = managementSourceIdentity(source.Source)
	}
	for _, source := range sources {
		if byID[source.ID] != managementSourceIdentity(source) {
			return errManagementSourceRevision
		}
	}
	return nil
}

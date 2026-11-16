package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

type configuredDisplayIdentity struct {
	Provider, Site, Fingerprint string
	Hashes                      []string
	Active                      bool
}

// A caller-owned copy may outlive its native provider's catalog entry. Retired
// inventory is name evidence only: never turn it into ownership/rate evidence.
// The exact generated ID, live caller membership, unchanged identity, source
// target, site and credential hashes must all agree before using the old name.
func (s *Service) observationCopyLabels(ctx context.Context, src controlSource, state subGatewayControls, keys []string) map[string]string {
	callers := map[string]bool{}
	for _, key := range keys {
		callers[key] = key != ""
	}
	wanted := map[string]string{}
	for _, channel := range state.Temporary {
		if !channel.IdentityChanged && callers[channel.KeyID] && strings.HasPrefix(channel.Provider, "sub2api-copy-") {
			wanted[channel.Provider] = channel.KeyID
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT provider,site,key_hashes,fingerprint,active FROM console_configured_channels WHERE source_id=$1`, src.ID)
	if err != nil {
		return nil // Optional presentation metadata must not break the listing.
	}
	defer rows.Close()
	identities := []configuredDisplayIdentity{}
	for rows.Next() {
		var item configuredDisplayIdentity
		var raw []byte
		if rows.Scan(&item.Provider, &item.Site, &raw, &item.Fingerprint, &item.Active) != nil || json.Unmarshal(raw, &item.Hashes) != nil {
			return nil
		}
		identities = append(identities, item)
	}
	if rows.Err() != nil {
		return nil
	}
	return configuredCopyDisplayLabels(src, wanted, identities)
}

func configuredCopyDisplayLabels(src controlSource, wanted map[string]string, identities []configuredDisplayIdentity) map[string]string {
	verified := map[string]configuredDisplayIdentity{}
	for _, item := range identities {
		if item.Provider == "" || item.Site == "" || len(item.Hashes) == 0 {
			continue
		}
		raw, _ := json.Marshal(item.Hashes)
		if item.Fingerprint == tokenHash(controlTarget(src)+"\n"+item.Provider+"\n"+item.Site+"\n"+string(raw)) {
			verified[item.Provider] = item
		}
	}
	labels := map[string]string{}
	ambiguous := map[string]bool{}
	for _, origin := range verified {
		if generatedSubProvider(origin.Provider) {
			continue // Do not replace one generated ID with another.
		}
		for copyID, key := range wanted {
			copy, ok := verified[copyID]
			if key == "" || !ok || !copy.Active || copyID != configuredImportName(origin.Provider, key) || copy.Site != origin.Site || !slices.Equal(copy.Hashes, origin.Hashes) {
				continue
			}
			if previous, exists := labels[copyID]; exists && previous != origin.Provider {
				ambiguous[copyID] = true
			}
			labels[copyID] = origin.Provider
		}
	}
	for provider := range ambiguous {
		delete(labels, provider)
	}
	return labels
}

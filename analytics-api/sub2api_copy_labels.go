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
	labels, _ := readConfiguredCopyLabels(ctx, s.control.db, src, configuredCopyCallers(state, callers), nil)
	return labels // Optional presentation metadata must not break the listing.
}

func configuredCopyCallers(state subGatewayControls, callers map[string]bool) map[string]string {
	wanted := map[string]string{}
	for _, channel := range state.Temporary {
		if !channel.IdentityChanged && channel.KeyID != "" && (callers == nil || callers[channel.KeyID]) && strings.HasPrefix(channel.Provider, "sub2api-copy-") {
			wanted[channel.Provider] = channel.KeyID
		}
	}
	return wanted
}

func readManagementCopyLabels(ctx context.Context, db controlQueryDB, src controlSource, snapshot managementSnapshot) (map[string]string, error) {
	wanted := map[string]string{}
	for provider, key := range snapshot.CopyKeys {
		if !snapshot.Copies[provider] && snapshot.Providers[provider].Fingerprint != "" {
			wanted[provider] = key
		}
	}
	return readConfiguredCopyLabels(ctx, db, src, wanted, snapshot.Providers)
}

func readConfiguredCopyLabels(ctx context.Context, db controlQueryDB, src controlSource, wanted map[string]string, live map[string]managementProvider) (map[string]string, error) {
	if len(wanted) == 0 {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT provider,site,key_hashes,fingerprint,active FROM console_configured_channels WHERE source_id=$1`, src.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	identities := []configuredDisplayIdentity{}
	for rows.Next() {
		var item configuredDisplayIdentity
		var raw []byte
		if err := rows.Scan(&item.Provider, &item.Site, &raw, &item.Fingerprint, &item.Active); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Hashes); err != nil {
			return nil, err
		}
		identities = append(identities, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return configuredCopyDisplayLabels(src, wanted, identities, live), nil
}

func configuredCopyDisplayLabels(src controlSource, wanted map[string]string, identities []configuredDisplayIdentity, live map[string]managementProvider) map[string]string {
	target := controlTarget(src)
	verified := map[string]configuredDisplayIdentity{}
	for _, item := range identities {
		if item.Provider == "" || item.Site == "" || len(item.Hashes) == 0 {
			continue
		}
		raw, _ := json.Marshal(item.Hashes)
		if item.Fingerprint == tokenHash(target+"\n"+item.Provider+"\n"+item.Site+"\n"+string(raw)) {
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
			if key == "" || copyID != configuredImportName(origin.Provider, key) {
				continue
			}
			if live != nil {
				// The periodic binding inventory excludes ordinary temporary
				// copies. Use the management catalog's current fingerprint to
				// prove the same site and credentials instead.
				raw, _ := json.Marshal(origin.Hashes)
				if live[copyID].Fingerprint != tokenHash(target+"\n"+copyID+"\n"+origin.Site+"\n"+string(raw)) {
					continue
				}
			} else {
				copy, ok := verified[copyID]
				if !ok || !copy.Active || copy.Site != origin.Site || !slices.Equal(copy.Hashes, origin.Hashes) {
					continue
				}
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

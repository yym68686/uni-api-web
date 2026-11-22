package main

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
)

type configuredDisplayIdentity struct {
	Provider, Site, Fingerprint string
	Hashes                      []string
	Active                      bool
}

func isConfiguredCopyID(provider string) bool {
	const prefix = "sub2api-copy-"
	return len(provider) == len(prefix)+20 && strings.HasPrefix(provider, prefix)
}

// A caller-owned copy may outlive its native provider's catalog entry. Retired
// inventory is name evidence only: never turn it into ownership/rate evidence.
// The exact generated ID, live caller membership, unchanged identity, source
// target, site and credential hashes must all agree before using the old name.
func (s *Service) observationCopyLabels(ctx context.Context, src controlSource, state subGatewayControls, keys []string, catalogCopies map[string]bool) map[string]string {
	callers := map[string]bool{}
	for _, key := range keys {
		callers[key] = key != ""
	}
	wanted := map[string][]string{}
	for provider, key := range configuredCopyCallers(state, callers) {
		wanted[provider] = []string{key}
	}
	labels, _ := readConfiguredCopyLabels(ctx, s.control.db, src, wanted, nil)
	// Promoted copies no longer have a temporary-channel record. Verify their
	// current configured identity just as the management view does. A changed
	// temporary copy must not fall through to the promoted path.
	for _, channel := range state.Temporary {
		delete(catalogCopies, channel.Provider)
	}
	if len(catalogCopies) == 0 || len(keys) == 0 {
		return labels
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	providers, err := effectiveProviders(ctx, src, true)
	if err != nil {
		return labels
	}
	promoted := map[string][]string{}
	live := map[string]managementProvider{}
	for _, p := range providers {
		// The provider export marks every generated copy as identity_changed,
		// including promoted copies. Once not temporary, the live fingerprint
		// (not that flag) is the same verification used by channel management.
		if !catalogCopies[p.Provider] || p.Temporary {
			continue
		}
		evidence := managementProviderEvidence(src, p)
		if evidence.Base != "" && evidence.CredentialCount > 0 {
			live[p.Provider], promoted[p.Provider] = evidence, keys
		}
	}
	verified, _ := readConfiguredCopyLabels(ctx, s.control.db, src, promoted, live)
	for provider, name := range verified {
		if labels == nil {
			labels = map[string]string{}
		}
		labels[provider] = name
	}
	return labels // Optional presentation metadata must not break the listing.
}

func configuredCopyCallers(state subGatewayControls, callers map[string]bool) map[string]string {
	wanted := map[string]string{}
	for _, channel := range state.Temporary {
		if !channel.IdentityChanged && channel.KeyID != "" && (callers == nil || callers[channel.KeyID]) && isConfiguredCopyID(channel.Provider) {
			wanted[channel.Provider] = channel.KeyID
		}
	}
	return wanted
}

func readManagementCopyLabels(ctx context.Context, db controlQueryDB, src controlSource, snapshot managementSnapshot, keys []string) (map[string]string, error) {
	wanted := map[string][]string{}
	for _, row := range snapshot.Rows {
		provider := row.Provider
		if !isConfiguredCopyID(provider) || snapshot.Copies[provider] || snapshot.Providers[provider].Fingerprint == "" {
			continue
		}
		if key := snapshot.CopyKeys[provider]; key != "" {
			wanted[provider] = []string{key}
		} else if len(keys) > 0 {
			// Promoted definitions no longer appear in temporary_channels.
			// Their generated ID can still prove an origin for a source key.
			wanted[provider] = keys
		}
	}
	return readConfiguredCopyLabels(ctx, db, src, wanted, snapshot.Providers)
}

func readConfiguredCopyLabels(ctx context.Context, db controlQueryDB, src controlSource, wanted map[string][]string, live map[string]managementProvider) (map[string]string, error) {
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

func configuredCopyDisplayLabels(src controlSource, wanted map[string][]string, identities []configuredDisplayIdentity, live map[string]managementProvider) map[string]string {
	target := controlTarget(src)
	verified := map[string]configuredDisplayIdentity{}
	known := map[string]bool{}
	for _, item := range identities {
		known[item.Provider] = true
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
	add := func(provider, label string) {
		if previous, exists := labels[provider]; exists && previous != label {
			ambiguous[provider] = true
		}
		labels[provider] = label
	}
	matches := func(copyID, origin string, keys []string) bool {
		return slices.ContainsFunc(keys, func(key string) bool {
			return key != "" && copyID == configuredImportName(origin, key)
		})
	}
	for _, origin := range verified {
		if generatedSubProvider(origin.Provider) {
			continue // Do not replace one generated ID with another.
		}
		for copyID, keys := range wanted {
			if !matches(copyID, origin.Provider, keys) {
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
			add(copyID, origin.Provider)
		}
	}
	if live != nil {
		for provider, keys := range wanted {
			if labels[provider] != "" {
				continue
			}
			// Old copies may predate inventory. A DNS label is only evidence
			// when it reproduces the exact ID with a key on this same source.
			// Never use it to override contrary recorded credentials.
			u, err := url.Parse(live[provider].Base)
			if err != nil || net.ParseIP(u.Hostname()) != nil {
				continue
			}
			parts := strings.Split(strings.TrimSuffix(strings.ToLower(u.Hostname()), "."), ".")
			for _, name := range parts[:len(parts)-1] {
				if name != "" && !known[name] && matches(provider, name, keys) {
					add(provider, name)
				}
			}
		}
	}
	for provider := range ambiguous {
		delete(labels, provider)
	}
	return labels
}

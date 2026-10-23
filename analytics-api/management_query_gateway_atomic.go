package main

import "context"

// Only a genuine 404 negotiates the older multi-request protocol. Permission,
// transport, malformed-data and revision errors must not silently downgrade.
func readManagementGatewayAtomic(ctx context.Context, source controlSource) (managementGatewayFacts, bool, error) {
	out := managementGatewayFacts{Source: source, AtomicSnapshot: true}
	if source.configKeyError != nil {
		return out, true, source.configKeyError
	}
	admin := source
	if admin.ConfigKey != "" {
		admin.Key = admin.ConfigKey
	}
	raw, code, err := subGateway(ctx, admin, "GET", "/v1/channel-settings/management-snapshot", nil)
	if code == 404 {
		return out, false, nil
	}
	if err != nil {
		return out, true, err
	}
	if raw["format"] != "management-snapshot-v1" {
		return out, true, errManagementSourceContract
	}
	out.Revision, _ = raw["revision"].(string)
	out.ConfigRevision, _ = raw["config_revision"].(string)
	if out.Revision == "" || out.ConfigRevision == "" {
		return out, true, errManagementSourceContract
	}
	for _, field := range []string{"controls", "keys", "catalog", "routes", "providers", "export"} {
		if raw[field] == nil {
			return out, true, errManagementSourceContract
		}
	}
	if decodeMap(raw["controls"], &out.Controls) != nil || out.Controls == nil ||
		decodeMap(raw["keys"], &out.Keys) != nil || out.Keys == nil ||
		decodeMap(raw["catalog"], &out.Catalog) != nil || out.Catalog == nil ||
		decodeMap(raw["routes"], &out.Routes) != nil || out.Routes == nil ||
		decodeMap(raw["providers"], &out.Providers) != nil || out.Providers == nil ||
		decodeMap(raw["export"], &out.Export) != nil || out.Export == nil {
		return out, true, errManagementSourceContract
	}
	if out.Controls["revision"] != out.Revision || out.Controls["config_revision"] != out.ConfigRevision || out.Export["revision"] != out.Revision {
		return out, true, errManagementSourceRevision
	}
	providers := map[string]bool{}
	for _, provider := range out.Providers {
		if provider.Provider == "" || providers[provider.Provider] {
			return out, true, errManagementSourceContract
		}
		providers[provider.Provider] = true
	}
	validRows := func(rows []managementModel) bool {
		for _, row := range rows {
			if row.Provider == "" || row.Model == "" || !providers[row.Provider] {
				return false
			}
		}
		return true
	}
	if !validRows(out.Catalog) {
		return out, true, errManagementSourceContract
	}
	keys := map[string]bool{}
	for _, key := range out.Keys {
		if key.ID == "" || keys[key.ID] || out.Routes[key.ID] == nil || !validRows(out.Routes[key.ID]) {
			return out, true, errManagementSourceContract
		}
		keys[key.ID] = true
	}
	if len(keys) != len(out.Routes) {
		return out, true, errManagementSourceContract
	}
	// Configuration administration cannot mask a revoked observation credential.
	// Preserve the legacy requirement that both source identities are valid.
	if source.ConfigKey != "" && source.ConfigKey != source.Key {
		controls, _, err := subGateway(ctx, source, "GET", "/v1/channel-controls", nil)
		if err != nil {
			return out, true, err
		}
		if controls["revision"] != out.Revision || controls["config_revision"] != out.ConfigRevision {
			return out, true, errManagementSourceRevision
		}
	}
	return out, true, nil
}

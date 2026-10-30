package main

import (
	"context"
	"golang.org/x/sync/errgroup"
)

// Both credential reads are independent. Their revisions are still required
// to match, and the complete pair still encloses the PostgreSQL snapshot.
func managementGatewayDocuments(ctx context.Context, source controlSource, path string) (map[string]any, int, error, map[string]any, error) {
	admin := source
	if admin.ConfigKey != "" {
		admin.Key = admin.ConfigKey
	}
	var document, controls map[string]any
	var code int
	var adminErr, observationErr error
	var group errgroup.Group
	group.Go(func() error { document, code, adminErr = subGateway(ctx, admin, "GET", path, nil); return nil })
	if source.ConfigKey != "" && source.ConfigKey != source.Key {
		group.Go(func() error {
			if path == "/v1/channel-settings/management-revision" {
				controls, observationErr = readManagementObservationRevision(ctx, source)
			} else {
				controls, _, observationErr = subGateway(ctx, source, "GET", "/v1/channel-controls", nil)
			}
			return nil
		})
	}
	_ = group.Wait()
	return document, code, adminErr, controls, observationErr
}

// Only a genuine 404 negotiates the older multi-request protocol. Permission,
// transport, malformed-data and revision errors must not silently downgrade.
func readManagementGatewayAtomic(ctx context.Context, source controlSource) (managementGatewayFacts, bool, error) {
	defer managementReadSpan(ctx, "gateway_atomic")()
	out := managementGatewayFacts{Source: source, AtomicSnapshot: true}
	if source.configKeyError != nil {
		return out, true, source.configKeyError
	}
	endDocuments := managementReadSpan(ctx, "gateway_atomic_documents")
	raw, code, err, controls, observationErr := managementGatewayDocuments(ctx, source, "/v1/channel-settings/management-snapshot")
	endDocuments()
	if code == 404 {
		out.Controls = controls
		return out, false, observationErr
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
	endDecode := managementReadSpan(ctx, "gateway_atomic_decode")
	invalid := decodeMap(raw["controls"], &out.Controls) != nil || out.Controls == nil ||
		decodeMap(raw["keys"], &out.Keys) != nil || out.Keys == nil ||
		decodeMap(raw["catalog"], &out.Catalog) != nil || out.Catalog == nil ||
		decodeMap(raw["routes"], &out.Routes) != nil || out.Routes == nil ||
		decodeMap(raw["providers"], &out.Providers) != nil || out.Providers == nil ||
		decodeMap(raw["export"], &out.Export) != nil || out.Export == nil
	endDecode()
	if invalid {
		return out, true, errManagementSourceContract
	}
	endValidate := managementReadSpan(ctx, "gateway_atomic_validate")
	err = validateManagementGatewayFacts(out)
	endValidate()
	if err != nil {
		return out, true, err
	}
	// Configuration administration cannot mask a revoked observation credential.
	// Preserve the legacy requirement that both source identities are valid.
	if source.ConfigKey != "" && source.ConfigKey != source.Key {
		if observationErr != nil {
			return out, true, observationErr
		}
		if controls["revision"] != out.Revision || controls["config_revision"] != out.ConfigRevision {
			return out, true, errManagementSourceRevision
		}
	}
	return out, true, nil
}

func validateManagementGatewayFacts(out managementGatewayFacts) error {
	if out.Revision == "" || out.ConfigRevision == "" || out.Controls == nil || out.Export == nil ||
		out.Keys == nil || out.Catalog == nil || out.Routes == nil || out.Providers == nil {
		return errManagementSourceContract
	}
	if out.Controls["revision"] != out.Revision || out.Controls["config_revision"] != out.ConfigRevision || out.Export["revision"] != out.Revision {
		return errManagementSourceRevision
	}
	providers := map[string]bool{}
	for _, provider := range out.Providers {
		if provider.Provider == "" || providers[provider.Provider] {
			return errManagementSourceContract
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
		return errManagementSourceContract
	}
	keys := map[string]bool{}
	for _, key := range out.Keys {
		if key.ID == "" || keys[key.ID] || out.Routes[key.ID] == nil || !validRows(out.Routes[key.ID]) {
			return errManagementSourceContract
		}
		keys[key.ID] = true
	}
	if len(keys) != len(out.Routes) {
		return errManagementSourceContract
	}
	return nil
}

// New gateways expose an observation-authorized scalar revision. Only 404
// falls back to the older full control document; permission/transport/format
// failures cannot be hidden by negotiating a different route.
func readManagementObservationRevision(ctx context.Context, source controlSource) (map[string]any, error) {
	raw, code, err := subGateway(ctx, source, "GET", "/v1/channel-controls/revision", nil)
	if code == 404 {
		old, _, oldErr := subGateway(ctx, source, "GET", "/v1/channel-controls", nil)
		return old, oldErr
	}
	if err != nil {
		return nil, err
	}
	if raw["format"] != "management-revision-v1" {
		return nil, errManagementSourceContract
	}
	return raw, nil
}

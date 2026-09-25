package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
)

// Sharing is explicit for a batch, never inferred from a displayed name or
// hostname. Read both effective definitions (including overrides) without
// moving credentials to the browser or copying detection rows between sources.
func (s *Service) sharedConfiguredEvidence(ctx context.Context, target controlSource, provider string, selected configuredProvider, sourceID string, needed map[string]bool) map[string]modelEvidence {
	result := map[string]modelEvidence{}
	peer, err := s.control.source(ctx, sourceID)
	if err != nil {
		return result
	}
	providers, err := configuredProviders(ctx, peer)
	if err != nil {
		return result
	}
	var equivalent *configuredProvider
	for _, p := range providers {
		if p.Provider == provider && p.Base == selected.Base && reflect.DeepEqual(configuredKeyHashes(p), configuredKeyHashes(selected)) {
			equivalent = &p
			break
		}
	}
	if equivalent == nil || len(configuredKeyHashes(selected)) == 0 {
		return result
	}
	left, err := s.effectiveEvidenceDocument(ctx, target, provider)
	if err != nil {
		return result
	}
	right, err := s.effectiveEvidenceDocument(ctx, peer, provider)
	if err != nil || !reflect.DeepEqual(left, right) {
		return result
	}
	definition, ok := right["definition"].(map[string]any)
	if !ok || definition["base_url"] != equivalent.Base || !reflect.DeepEqual(configuredKeyHashes(configuredProvider{API: definition["api"]}), configuredKeyHashes(*equivalent)) {
		return result
	}
	mapped := map[string]string{}
	models, ok := definition["model"].([]any)
	if !ok {
		return result
	}
	for _, entry := range models {
		switch v := entry.(type) {
		case string:
			mapped[v] = v
		case map[string]any:
			for upstream, value := range v {
				if public, ok := value.(string); ok {
					mapped[public] = upstream
				}
			}
		}
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT model,state,result FROM console_configured_checks WHERE source_id=$1 AND provider=$2 AND source_target=$3 AND fingerprint=$4 AND kind IN ('model','availability')`, peer.ID, provider, controlTarget(peer), configuredProbeFingerprint(peer, *equivalent))
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var model, state string
		var raw []byte
		if rows.Scan(&model, &state, &raw) != nil {
			return map[string]modelEvidence{}
		}
		upstream := mapped[model]
		if upstream == "" {
			upstream = model
		}
		if !needed[upstream] {
			continue
		}
		var probe *subResult
		_ = json.Unmarshal(raw, &probe)
		v := modelEvidence{state, probe}
		if old, exists := result[upstream]; exists {
			v = newestModelEvidence(old, v)
		}
		result[upstream] = v
	}
	if rows.Err() != nil {
		return map[string]modelEvidence{}
	}
	return result
}

var errEvidenceConfigChanged = errors.New("检测配置已变化")

func (s *Service) effectiveEvidenceDocument(ctx context.Context, src controlSource, provider string) (map[string]any, error) {
	// Another source in the same parallel batch may finish its restore while
	// this read is in flight. Retry that revision race once, never a mutation.
	document, err := s.readEvidenceDocument(ctx, src, provider)
	if errors.Is(err, errEvidenceConfigChanged) {
		return s.readEvidenceDocument(ctx, src, provider)
	}
	return document, err
}

func (s *Service) readEvidenceDocument(ctx context.Context, src controlSource, provider string) (map[string]any, error) {
	admin := src
	if src.ConfigKey != "" {
		admin.Key = src.ConfigKey
	}
	exported, _, err := s.settingsGateway(ctx, admin, "GET", "/v1/channel-settings/export", nil)
	if err != nil {
		return nil, err
	}
	snapshot := retainedSnapshot{}
	if err = decodeMap(exported["channel_settings"], &snapshot.Settings); err != nil {
		return nil, err
	}
	var definitions map[string]json.RawMessage
	if err = decodeMap(exported["temporary_definitions"], &definitions); err != nil {
		return nil, err
	}
	if raw := definitions[provider]; len(raw) > 0 {
		snapshot.Channels = []retainedChannel{{Provider: provider, Definition: raw}}
	}
	raw, _, err := subGateway(ctx, admin, "GET", "/v1/api_config", nil)
	if err != nil {
		return nil, err
	}
	var config struct {
		Providers   []map[string]any `json:"providers"`
		Preferences map[string]any   `json:"preferences"`
	}
	if err = decodeMap(raw["api_config"], &config); err != nil {
		return nil, err
	}
	base := map[string]map[string]any{}
	for _, p := range config.Providers {
		if name, ok := p["provider"].(string); ok {
			base[name] = p
		}
	}
	document, _, err := s.importProviderDocument(ctx, src, snapshot, provider, "", base)
	if err != nil {
		return nil, err
	}
	// Fence the read against a concurrent settings/config change.
	state, _, err := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if err != nil {
		return nil, err
	}
	if exported["revision"] == nil || state["revision"] != exported["revision"] {
		return nil, errEvidenceConfigChanged
	}
	return map[string]any{"definition": document, "global_preferences": config.Preferences}, nil
}

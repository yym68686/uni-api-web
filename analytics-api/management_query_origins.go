package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Classification uses the same current exported definitions and historical
// override evidence as the existing optimizer. Missing proof remains unknown;
// it never turns an explicit user engine choice into a migration candidate.
func (s *Service) managementEngineOriginsFrom(ctx context.Context, db controlQueryDB, facts managementGatewayFacts, refs []subChannelRef) (map[string]string, error) {
	overrides, err := engineOverrideHistoryFrom(ctx, db, facts.Source.ID)
	if err != nil {
		return nil, err
	}
	origins := map[string]string{}
	for provider := range overrides {
		origins[provider] = engineOriginUser
	}
	if facts.Export == nil {
		return origins, nil
	}
	var definitions map[string]json.RawMessage
	snapshot := retainedSnapshot{Version: 2}
	var live subGatewayControls
	if decodeMap(facts.Export["temporary_definitions"], &definitions) != nil || definitions == nil || decodeMap(facts.Export["channel_settings"], &snapshot.Settings) != nil || snapshot.Settings == nil || decodeMap(facts.Controls, &live) != nil {
		return nil, errManagementSourceContract
	}
	for provider := range snapshot.Settings {
		if protocolRepairCustomized(snapshot, provider) {
			origins[provider] = engineOriginUser
		}
	}
	var retained map[string]retainedChannel
	readRetained := false
	for _, channel := range live.Temporary {
		definition, exists := definitions[channel.Provider]
		var item retainedChannel
		if exists {
			var doc struct {
				Base string `json:"base_url"`
			}
			if json.Unmarshal(definition, &doc) != nil {
				return nil, errManagementSourceContract
			}
			item = retainedChannel{Provider: channel.Provider, KeyID: channel.KeyID, Base: doc.Base, Models: channel.Models, Definition: definition}
		} else {
			if !readRetained {
				readRetained = true
				var record retainedRecord
				err := db.QueryRowContext(ctx, `SELECT encrypted_snapshot,target_hash,revision FROM console_control_snapshots WHERE source_id=$1`, facts.Source.ID).Scan(&record.Encrypted, &record.Target, &record.Revision)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, err
				}
				if err == nil && record.Target == controlTarget(facts.Source) && record.Revision == facts.Revision && record.Encrypted != "" {
					saved, err := s.control.retainedSnapshot(record)
					if err != nil {
						return nil, err
					}
					retained = map[string]retainedChannel{}
					for _, c := range saved.Channels {
						retained[c.Provider] = c
					}
				}
			}
			var ok bool
			item, ok = retained[channel.Provider]
			if !ok || item.KeyID != channel.KeyID || item.Key == "" {
				continue
			}
		}
		origins[channel.Provider] = legacyEngineOrigin(snapshot, item, refs, overrides)
	}
	return origins, nil
}

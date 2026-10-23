package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	engineOriginLegacy  = "legacy_default"
	engineOriginUser    = "user_override"
	engineOriginUnknown = "unknown"
)

// Settings can be folded into a channel definition by later model edits. Keep
// historical engine/address overrides protected even when live settings are empty.
func (s *Service) engineOverrideHistory(ctx context.Context, source string) (map[string]bool, error) {
	return engineOverrideHistoryFrom(ctx, s.control.db, source)
}

func engineOverrideHistoryFrom(ctx context.Context, db controlQueryDB, source string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT p->>'provider'
 FROM console_channel_settings_operations o
 CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(o.public_result->'previews')='array' THEN o.public_result->'previews' ELSE '[]'::jsonb END) p
 WHERE o.source_id=$1 AND o.status<>'rejected' AND
 (p->>'created'='true' OR p->'before'->'engine' IS DISTINCT FROM p->'after'->'engine'
 OR p->'before'->'base_url' IS DISTINCT FROM p->'after'->'base_url')`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var provider *string
		if err = rows.Scan(&provider); err != nil {
			return nil, err
		}
		if provider != nil {
			result[*provider] = true
		}
	}
	return result, rows.Err()
}

func legacyEngineOrigin(snapshot retainedSnapshot, channel retainedChannel, refs []subChannelRef, overrides map[string]bool) string {
	if overrides[channel.Provider] || protocolRepairCustomized(snapshot, channel.Provider) {
		return engineOriginUser
	}
	var document map[string]any
	if len(channel.Definition) > 0 {
		if json.Unmarshal(channel.Definition, &document) != nil {
			return engineOriginUnknown
		}
	} else {
		// A definition-less temporary channel used the old gateway's gpt default.
		document = map[string]any{"provider": channel.Provider, "engine": "gpt", "base_url": channel.Base}
	}
	if document["engine"] != "gpt" || document["provider"] != channel.Provider {
		return engineOriginUnknown
	}
	base, ok := document["base_url"].(string)
	if !ok || strings.TrimRight(base, "/") != strings.TrimRight(channel.Base, "/") {
		return engineOriginUnknown
	}
	for _, ref := range refs {
		// Old imports had exactly this generated root name and Responses URL.
		// Protocol-suffixed, native or copied channels are not legacy evidence.
		if channel.KeyID != "" && channel.Provider == subProviderName(ref.Account, ref.Group, channel.KeyID) &&
			strings.TrimRight(base, "/") == strings.TrimRight(ref.Base, "/")+"/v1/responses" {
			return engineOriginLegacy
		}
	}
	return engineOriginUnknown
}

func (s *Service) snapshotEngineOrigins(ctx context.Context, src controlSource, snapshot retainedSnapshot, owner string) (map[string]string, error) {
	refs, err := s.control.subChannelRefs(ctx, owner)
	if err != nil {
		return nil, err
	}
	overrides, err := s.engineOverrideHistory(ctx, src.ID)
	if err != nil {
		return nil, err
	}
	origins := map[string]string{}
	for provider := range overrides {
		origins[provider] = engineOriginUser
	}
	for provider := range snapshot.Settings {
		if protocolRepairCustomized(snapshot, provider) {
			origins[provider] = engineOriginUser
		}
	}
	for _, c := range snapshot.Channels {
		origins[c.Provider] = legacyEngineOrigin(snapshot, c, refs, overrides)
	}
	return origins, nil
}

// This only prepares documents in memory. Proof is re-read under the source
// lock, and migration shares the model batch's single revision-fenced restore.
func (s *Service) prepareLegacyEngineMigrations(ctx context.Context, src controlSource, snapshot retainedSnapshot, state map[string]any, owner string, in channelBatchInput, catalogs map[string][]batchCatalogRow, documents map[string]map[string]any) error {
	requested := false
	for _, t := range in.Targets {
		requested = requested || t.MigrateLegacyEngine
	}
	if !requested {
		return nil
	}
	if in.Part != "optimize" || state["automatic_engine"] != true {
		return errors.New("来源不支持旧版协议升级，请刷新后重试")
	}
	origins, err := s.snapshotEngineOrigins(ctx, src, snapshot, owner)
	if err != nil {
		return errors.New("旧配置来源读取失败，未提交修改")
	}
	for _, t := range in.Targets {
		if !t.MigrateLegacyEngine {
			continue
		}
		if t.Account == "" || in.removes(t) || origins[t.Provider] != engineOriginLegacy || catalogChannelProtocol(catalogs[t.Key], t.Provider) != "chat" {
			return fmt.Errorf("%s：无法确认是可升级的旧版默认配置，未提交修改", t.Provider)
		}
		doc := documents[t.Key+"\n"+t.Provider]
		if doc == nil || doc["engine"] != "gpt" {
			return fmt.Errorf("%s：渠道引擎已变化，请重新分析", t.Provider)
		}
		delete(doc, "engine")
	}
	return nil
}

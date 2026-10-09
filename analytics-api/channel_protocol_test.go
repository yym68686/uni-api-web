package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestChannelProtocolPartitionAndSplitPreservesRules(t *testing.T) {
	models := map[string]string{"gpt-6-luna": "gpt-6-luna", "gpt-alias": "claude-fable-5"}
	parts, err := partitionChannelModels(models, map[string]string{"gpt-6-luna": "responses", "claude-fable-5": "messages"})
	if err != nil || len(parts) != 2 {
		t.Fatal(parts, err)
	}
	root := subProviderName("account", 7, "key")
	old := retainedChannel{Provider: root, KeyID: "key", Key: "fixture", Models: []string{"gpt-6-luna", "gpt-alias"}}
	doc := map[string]any{"engine": "gpt", "base_url": "https://site.test/prefix/v1/responses", "api": []string{"fixture"}, "preferences": map[string]any{"cooldown_period": 33}}
	snapshot := retainedSnapshot{Channels: []retainedChannel{old}, Settings: map[string]json.RawMessage{}, Rules: []retainedRule{
		{KeyID: "key", Order: []string{"first", root, "last"}, Disabled: []string{root}},
		{KeyID: "key", Model: "gpt-alias", Order: []string{root, "last"}, Disabled: []string{root}},
	}}
	channels, err := splitProtocolChannel(&snapshot, old, doc, parts)
	if err != nil || len(channels) != 2 {
		t.Fatal(channels, err)
	}
	if channels[0].Provider != root || channels[0].Base != "https://site.test/prefix/v1/responses" || channels[1].Base != "https://site.test/prefix/v1/messages" {
		t.Fatal(channels)
	}
	if !reflect.DeepEqual(snapshot.Rules[0].Disabled, []string{root, root + "--messages"}) || !reflect.DeepEqual(snapshot.Rules[1].Order, []string{root + "--messages", "last"}) {
		t.Fatal(snapshot.Rules)
	}
	for _, channel := range channels {
		var result map[string]any
		_ = json.Unmarshal(channel.Definition, &result)
		if _, exists := result["engine"]; exists {
			t.Fatal("engine inference must be delegated to the gateway")
		}
		if result["preferences"].(map[string]any)["cooldown_period"] != float64(33) {
			t.Fatal("settings lost")
		}
	}
	if !matchesSubProvider(root+"--messages", "account", 7, "key") || matchesSubProvider(root+"--messages", "account", 7, "other") || matchesSubProvider(root+"--arbitrary", "account", 7, "key") {
		t.Fatal("provider ownership mismatch")
	}
	if _, err = partitionChannelModels(map[string]string{"unknown": "unknown"}, nil); err == nil {
		t.Fatal("unknown protocol silently inferred")
	}
	if _, err = partitionChannelModels(map[string]string{"claude": "claude-fable-5"}, map[string]string{"claude-fable-5": "chat"}); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolRepairProtectsManualOverrides(t *testing.T) {
	for _, path := range []string{"/engine", "/base_url"} {
		for _, raw := range []string{`{"set":{"` + path + `":"custom"}}`, `{"remove":["` + path + `"]}`} {
			if !protocolRepairCustomized(retainedSnapshot{Settings: map[string]json.RawMessage{"channel": json.RawMessage(raw)}}, "channel") {
				t.Fatal("manual override not protected")
			}
		}
	}
}

func TestCatalogProtocolUsesGatewayEngineNotModelOrProviderSuffix(t *testing.T) {
	rows := []batchCatalogRow{
		{Provider: "auto", Model: "claude-fable-5", Engine: "codex"},
		{Provider: "custom--responses", Model: "gpt-6-luna", Engine: "claude"},
		{Provider: "missing", Model: "gpt-6-luna"},
	}
	for provider, expected := range map[string]string{"auto": "responses", "custom--responses": "messages", "missing": "", "absent": ""} {
		if actual := catalogChannelProtocol(rows, provider); actual != expected {
			t.Fatalf("provider %s: got %q, want %q", provider, actual, expected)
		}
	}
}

func TestProtocolSplitRejectsConflictingSiblingOrSuffix(t *testing.T) {
	parts, _ := partitionChannelModels(map[string]string{"gpt-6-luna": "gpt-6-luna", "claude-fable-5": "claude-fable-5"}, map[string]string{"gpt-6-luna": "responses", "claude-fable-5": "messages"})
	old := retainedChannel{Provider: "root", Models: []string{"gpt-6-luna"}}
	document := map[string]any{"engine": "codex", "base_url": "https://site.test/v1/responses"}
	snapshot := retainedSnapshot{Channels: []retainedChannel{old, {Provider: "root--messages"}}}
	if _, err := splitProtocolChannel(&snapshot, old, document, parts); err == nil {
		t.Fatal("existing protocol sibling overwritten")
	}
	old.Provider = "root--messages"
	parts, _ = partitionChannelModels(map[string]string{"gpt-6-luna": "gpt-6-luna"}, map[string]string{"gpt-6-luna": "responses"})
	if _, err := splitProtocolChannel(&snapshot, old, document, parts); err == nil {
		t.Fatal("protocol suffix changed meaning")
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestLegacyEngineOriginRequiresGeneratedIdentityURLAndNoManualOverride(t *testing.T) {
	ref := subChannelRef{Account: "account", Group: 7, Base: "https://site.example/gateway"}
	provider := subProviderName(ref.Account, ref.Group, "key")
	old := retainedChannel{Provider: provider, KeyID: "key", Base: ref.Base + "/v1/responses"}
	for _, scenario := range []string{"legacy", "definitionless", "manual-engine", "manual-address", "history", "native", "suffix", "url", "malformed", "engine"} {
		t.Run(scenario, func(t *testing.T) {
			c := old
			doc := map[string]any{"provider": provider, "engine": "gpt", "base_url": old.Base, "model": []string{"m"}}
			snapshot := retainedSnapshot{Settings: map[string]json.RawMessage{}}
			history := map[string]bool{}
			want := engineOriginUnknown
			switch scenario {
			case "legacy", "definitionless":
				want = engineOriginLegacy
			case "manual-engine":
				snapshot.Settings[provider] = json.RawMessage(`{"set":{"/engine":"gpt"}}`)
				want = engineOriginUser
			case "manual-address":
				snapshot.Settings[provider] = json.RawMessage(`{"remove":["/base_url"]}`)
				want = engineOriginUser
			case "history":
				history[provider] = true
				want = engineOriginUser
			case "native":
				c.Provider = "native"
				doc["provider"] = c.Provider
			case "suffix":
				c.Provider += "--responses"
				doc["provider"] = c.Provider
			case "url":
				c.Base = "https://other.example/v1/responses"
				doc["base_url"] = c.Base
			case "engine":
				doc["engine"] = "claude"
			}
			c.Definition = json.RawMessage(mustJSON(doc))
			if scenario == "definitionless" {
				c.Definition = nil
			}
			if scenario == "malformed" {
				c.Definition = json.RawMessage(`{`)
			}
			if got := legacyEngineOrigin(snapshot, c, []subChannelRef{ref}, history); got != want {
				t.Fatal(got, want)
			}
		})
	}
}

func TestLegacyEngineOptimizationSharesAtomicRestoreAndPreservesConfiguration(t *testing.T) {
	s, a, src := bindingFixture(t, "https://site.example/gateway")
	ctx := context.Background()
	key := "key-" + strings.Repeat("a", 64)
	provider := subProviderName(a.ID, 7, key)
	base := a.Base + "/v1/responses"
	definition := map[string]any{"provider": provider, "engine": "gpt", "base_url": base, "api": []string{"fixture-upstream-secret"}, "model": []any{map[string]any{"old-upstream": "alias"}}, "headers": map[string]string{"X-Test": "retained"}, "preferences": map[string]any{"cooldown_period": 23}}
	current := retainedSnapshot{Version: 2, Channels: []retainedChannel{{Provider: provider, KeyID: key, Key: "fixture-upstream-secret", Base: base, Models: []string{"alias"}, Definition: json.RawMessage(mustJSON(definition))}}, Settings: map[string]json.RawMessage{provider: json.RawMessage(`{"set":{"/preferences/timeout":42}}`)}, Rules: []retainedRule{{KeyID: key, Model: "alias", Order: []string{"peer", provider}, Disabled: []string{provider}}, {KeyID: "other", Model: "alias", Order: []string{"untouched"}}}}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,active) VALUES($1,7,'group','openai',true)`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,7,'new','done',$2)`, a.ID, mustJSON(subResult{Availability: subProbe{Status: "success", Protocol: "responses"}})); err != nil {
		t.Fatal(err)
	}
	revision, restores := "r1", 0
	state := func() map[string]any {
		defs := map[string]json.RawMessage{}
		for _, c := range current.Channels {
			defs[c.Provider] = c.Definition
		}
		return map[string]any{"instance_id": "boot", "revision": revision, "temporary_channel_management": true, "temporary_channel_restore": true, "automatic_engine": true, "channel_settings": true, "channel_definitions": true, "channel_definitions_digest": tokenHash(canonicalSettings(defs)), "channel_settings_digest": tokenHash(canonicalSettings(current.Settings)), "temporary_channels": current.Channels, "rules": current.Rules}
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/channel-controls":
			writeJSON(w, 200, state())
		case "/v1/channel-settings/export":
			defs := map[string]json.RawMessage{}
			for _, c := range current.Channels {
				defs[c.Provider] = c.Definition
			}
			writeJSON(w, 200, map[string]any{"revision": revision, "temporary_definitions": defs, "channel_settings": current.Settings})
		case "/v1/api-keys":
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"key_id": key, "prefix": "masked", "position": 1}}})
		case "/v1/model-channels":
			rows := []any{map[string]any{"provider": "peer", "model": "alias", "upstream_model": "old-upstream", "engine": "codex", "engine_mode": "auto"}}
			var doc map[string]any
			json.Unmarshal(current.Channels[0].Definition, &doc)
			models, _ := protocolDocumentModels(doc)
			engine, mode := "gpt", "explicit"
			if doc["engine"] == nil {
				engine, mode = "codex", "auto"
			}
			for m, up := range models {
				rows = append(rows, map[string]any{"provider": provider, "model": m, "upstream_model": up, "engine": engine, "engine_mode": mode})
			}
			writeJSON(w, 200, map[string]any{"data": rows})
		case "/v1/channel-controls/restore":
			var in struct {
				Revision string           `json:"revision"`
				Snapshot retainedSnapshot `json:"snapshot"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Revision != revision {
				http.Error(w, "stale", 409)
				return
			}
			restores++
			current = in.Snapshot
			if current.Settings == nil {
				current.Settings = map[string]json.RawMessage{}
			}
			revision = "r2"
			writeJSON(w, 200, state())
		default:
			t.Errorf("unexpected gateway request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	src.Base = gateway.URL
	if _, err := s.control.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	if err := s.saveLiveControls(ctx, src, state()); err != nil {
		t.Fatal(err)
	}
	token, err := s.control.newSession(ctx, a.Owner)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/sources/"+src.ID+path, strings.NewReader(mustJSON(body)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := call("GET", "/channel-routes", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"engine_origin":"legacy_default"`) || !strings.Contains(w.Body.String(), `"legacy_engine_migration":true`) || restores != 0 {
		t.Fatal(w.Code, w.Body.String(), restores)
	}
	in := channelBatchInput{Revision: revision, Part: "optimize", Targets: []channelBatchTarget{{Key: key, Provider: provider, Account: a.ID, Group: 7, Current: map[string]string{"alias": "old-upstream"}, Models: map[string]string{"alias": "old-upstream", "new": "new"}, Positions: map[string]int{"alias": 2, "new": 1}, MigrateLegacyEngine: true}}}
	// A real settings override invalidates a prior candidate before any write.
	current.Settings[provider] = json.RawMessage(`{"set":{"/engine":"gpt","/preferences/timeout":42}}`)
	if w = call("POST", "/channel-batch", in); w.Code != 400 || restores != 0 {
		t.Fatal("override accepted", w.Code, w.Body.String(), restores)
	}
	current.Settings[provider] = json.RawMessage(`{"set":{"/preferences/timeout":42}}`)
	withoutMigration := in
	withoutMigration.Targets = append([]channelBatchTarget{}, in.Targets...)
	withoutMigration.Targets[0].MigrateLegacyEngine = false
	if unselected := call("POST", "/channel-batch", withoutMigration); unselected.Code != 400 || restores != 0 {
		t.Fatal("unselected migration changed protocol", unselected.Code, restores)
	}
	stale := in
	stale.Revision = "old"
	if w = call("POST", "/channel-batch", stale); w.Code != 409 || restores != 0 {
		t.Fatal("stale accepted", w.Code, restores)
	}
	if w = call("POST", "/channel-batch", in); w.Code != 200 || restores != 1 {
		t.Fatal(w.Code, w.Body.String(), restores)
	}
	// A second pass cannot reclassify the now-automatic channel as a legacy
	// default, even when the caller sends an obsolete migration flag.
	in.Revision = revision
	in.Targets[0].Current = in.Targets[0].Models
	if retry := call("POST", "/channel-batch", in); retry.Code != 400 || restores != 1 {
		t.Fatal("obsolete migration accepted", retry.Code, retry.Body.String(), restores)
	}
	var actual map[string]any
	json.Unmarshal(current.Channels[0].Definition, &actual)
	if _, exists := actual["engine"]; exists || actual["base_url"] != base || actual["headers"].(map[string]any)["X-Test"] != "retained" || actual["preferences"].(map[string]any)["timeout"] != float64(42) {
		t.Fatal("migration changed unrelated configuration", actual)
	}
	models, _ := protocolDocumentModels(actual)
	if !reflect.DeepEqual(models, in.Targets[0].Models) || !reflect.DeepEqual(providerKeys(actual["api"]), []string{"fixture-upstream-secret"}) {
		t.Fatal("models or credentials changed")
	}
	for _, rule := range current.Rules {
		if rule.KeyID == "other" && !reflect.DeepEqual(rule.Order, []string{"untouched"}) {
			t.Fatal(rule)
		}
		if rule.KeyID == key && rule.Model == "alias" && (!reflect.DeepEqual(rule.Order, []string{"peer", provider}) || !reflect.DeepEqual(rule.Disabled, []string{provider})) {
			t.Fatal(rule)
		}
	}
	if strings.Contains(w.Body.String(), "fixture-upstream-secret") {
		t.Fatal("secret leaked")
	}
}

func TestEngineHistoryProtectsOverridesFoldedIntoDefinitions(t *testing.T) {
	s, _, src := bindingFixture(t, "https://site.example")
	ctx := context.Background()
	defer s.control.db.Exec(`DELETE FROM console_channel_settings_operations WHERE source_id=$1`, src.ID)
	for name, status := range map[string]string{"manual": "applied", "pending": "pending", "rejected": "rejected", "unrelated": "applied"} {
		before, after := map[string]any{"engine": "gpt"}, map[string]any{"engine": "claude"}
		if name == "unrelated" {
			after = map[string]any{"engine": "gpt", "model": []string{"new"}}
		}
		raw := mustJSON(map[string]any{"previews": []any{map[string]any{"provider": name, "before": before, "after": after}}})
		if _, err := s.control.db.Exec(`INSERT INTO console_channel_settings_operations(id,source_id,owner,request_hash,request_revision,status,encrypted_before,encrypted_intent,public_result) VALUES($1,$2,'owner','hash','r1',$3,'','',$4)`, randomID(), src.ID, status, raw); err != nil {
			t.Fatal(err)
		}
	}
	history, err := s.engineOverrideHistory(ctx, src.ID)
	if err != nil || !history["manual"] || !history["pending"] || history["rejected"] || history["unrelated"] {
		t.Fatal(history, err)
	}
}

func TestMigrationOnlyIsNotDiscardedAsUnchangedMembership(t *testing.T) {
	snapshot := retainedSnapshot{Version: 2, Settings: map[string]json.RawMessage{}, Channels: []retainedChannel{{Provider: "legacy", KeyID: "key", Base: "https://site.example/v1/responses", Models: []string{"alias"}, Definition: json.RawMessage(`{"provider":"legacy","engine":"gpt","base_url":"https://site.example/v1/responses","model":[{"upstream":"alias"}]}`)}}, Rules: []retainedRule{{KeyID: "key", Model: "alias", Order: []string{"peer", "legacy"}, Disabled: []string{"legacy"}}}}
	in := channelBatchInput{Part: "optimize", Targets: []channelBatchTarget{{Key: "key", Provider: "legacy", Account: "account", Current: map[string]string{"alias": "upstream"}, Models: map[string]string{"alias": "upstream"}, Positions: map[string]int{"alias": 2}, MigrateLegacyEngine: true}}}
	catalog := map[string][]batchCatalogRow{"key": {{Provider: "peer", Model: "alias", Upstream: "upstream"}, {Provider: "legacy", Model: "alias", Upstream: "upstream"}}}
	docs := map[string]map[string]any{"key\nlegacy": {"provider": "legacy", "base_url": "https://site.example/v1/responses", "api": "fixture", "model": []any{map[string]string{"upstream": "alias"}}}}
	changed, err := buildBatchSnapshot(&snapshot, in, catalog, docs)
	if err != nil || !changed || strings.Contains(string(snapshot.Channels[0].Definition), `"engine"`) {
		t.Fatal(changed, err)
	}
	if !reflect.DeepEqual(snapshot.Rules[0].Order, []string{"peer", "legacy"}) || !reflect.DeepEqual(snapshot.Rules[0].Disabled, []string{"legacy"}) {
		t.Fatal(snapshot.Rules)
	}
}

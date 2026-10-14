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

// A custom-named, key-owned channel is absent from binding discovery but is
// still editable. Exercise the HTTP save path without any real gateway writes.
func TestConfiguredImportEditsCustomNamedTemporaryChannel(t *testing.T) {
	s, account, src := bindingFixture(t, "https://site.example")
	ctx := context.Background()
	key := "key-" + strings.Repeat("a", 64)
	provider := configuredProvider{Provider: "openai-next", Temporary: true, Base: "https://site.example/v1/responses", API: "fixture-upstream-secret"}
	definition := map[string]any{"provider": provider.Provider, "base_url": provider.Base, "api": provider.API, "engine": "codex", "model": []string{"gpt-6-luna"}, "preferences": map[string]any{"headers": map[string]string{"X-Fixture": "preserved"}}}
	snapshot := retainedSnapshot{Version: 2, Settings: map[string]json.RawMessage{}, Channels: []retainedChannel{{Provider: provider.Provider, KeyID: key, Base: provider.Base, Models: []string{"gpt-6-luna"}, Definition: json.RawMessage(mustJSON(definition))}}, Rules: []retainedRule{{KeyID: "other-key", Model: "untouched", Order: []string{"other", "peer"}, Disabled: []string{"peer"}}}}
	revision := "boot:1"
	writes := 0
	live := func() map[string]any {
		return map[string]any{"instance_id": "boot", "revision": revision, "temporary_channel_restore": true, "channel_settings": true, "channel_definitions": true, "temporary_channels": snapshot.Channels, "rules": snapshot.Rules}
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/channel-settings/providers":
			if r.Method != "GET" {
				t.Error("unexpected provider mutation")
			}
			writeJSON(w, 200, map[string]any{"providers": []configuredProvider{provider}})
		case "/v1/channel-controls":
			writeJSON(w, 200, live())
		case "/v1/channel-settings/export":
			defs := map[string]json.RawMessage{}
			for _, c := range snapshot.Channels {
				defs[c.Provider] = c.Definition
			}
			writeJSON(w, 200, map[string]any{"revision": revision, "channel_settings": snapshot.Settings, "temporary_definitions": defs})
		case "/v1/model-channels":
			rows := []batchCatalogRow{}
			for _, c := range snapshot.Channels {
				for _, m := range c.Models {
					rows = append(rows, batchCatalogRow{Provider: c.Provider, Model: m, Upstream: m})
				}
			}
			writeJSON(w, 200, map[string]any{"data": rows})
		case "/v1/channel-controls/restore":
			var in struct {
				Revision string           `json:"revision"`
				Snapshot retainedSnapshot `json:"snapshot"`
			}
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&in) != nil {
				t.Error("invalid restore")
				http.Error(w, "invalid", 400)
				return
			}
			if in.Revision != revision {
				http.Error(w, "conflict", 409)
				return
			}
			snapshot = in.Snapshot
			revision = "boot:2"
			writes++
			writeJSON(w, 200, live())
		default:
			t.Error("unexpected gateway path", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	src.Base = gateway.URL
	if _, err := s.control.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	session, err := s.control.newSession(ctx, account.Owner)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(mustJSON(body)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	// Keep duplicate-suppression for inventory; existence/evidence uses the full
	// installed list, independently of whether an identity override was saved.
	inventory, err := configuredProviders(ctx, src)
	if err != nil || len(inventory) != 0 {
		t.Fatal("import leaked into binding inventory", inventory, err)
	}
	info := "/v1/sources/" + src.ID + "/channel-info?provider=openai-next"
	for _, reveal := range []bool{false, true} {
		path := info
		if reveal {
			path += "&reveal=true"
		}
		w := call("GET", path, nil)
		if w.Code != 200 {
			t.Fatalf("existing imported channel info: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "fixture-upstream-secret") != reveal {
			t.Fatal("incorrect key disclosure", reveal)
		}
	}
	if w := call("GET", "/v1/sources/"+src.ID+"/channel-info?provider=missing", nil); w.Code != 404 {
		t.Fatal("missing provider accepted", w.Code)
	}
	body := map[string]any{"source_id": src.ID, "api_key_id": src.ID + "::" + key, "provider": provider.Provider, "edit_provider": provider.Provider, "revision": revision, "position": 1, "models": []string{"gpt-6-luna", "gpt-6-sol"}}
	seed := func(fingerprint, status string) {
		t.Helper()
		_, err := s.control.db.Exec(`INSERT INTO console_configured_checks(source_id,provider,kind,model,source_target,fingerprint,state,result) VALUES($1,$2,'model','gpt-6-sol',$3,$4,'done',$5) ON CONFLICT(source_id,provider,kind,model) DO UPDATE SET fingerprint=excluded.fingerprint,result=excluded.result`, src.ID, provider.Provider, controlTarget(src), fingerprint, mustJSON(subResult{Model: "gpt-6-sol", CheckedAt: 1, Availability: subProbe{Status: status}}))
		if err != nil {
			t.Fatal(err)
		}
	}
	fingerprint := configuredProbeFingerprint(src, provider)
	for _, bad := range []struct{ fingerprint, status string }{{"stale-credentials", "success"}, {fingerprint, "error"}} {
		seed(bad.fingerprint, bad.status)
		w := call("POST", "/v1/channel-management", body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "未通过：gpt-6-sol") || writes != 0 {
			t.Fatalf("invalid evidence was not rejected precisely: %d %s writes=%d", w.Code, w.Body.String(), writes)
		}
	}
	seed(fingerprint, "success")
	w := call("POST", "/v1/channel-management", body)
	if w.Code != 200 || writes != 1 {
		t.Fatalf("save existing import: %d %s writes=%d", w.Code, w.Body.String(), writes)
	}
	if strings.Contains(w.Body.String(), "fixture-upstream-secret") {
		t.Fatal("save leaked secret")
	}
	if len(snapshot.Channels) != 1 || snapshot.Channels[0].Provider != provider.Provider || snapshot.Channels[0].KeyID != key {
		t.Fatal("replaced existing channel with a different copy", snapshot.Channels)
	}
	var saved map[string]any
	if json.Unmarshal(snapshot.Channels[0].Definition, &saved) != nil {
		t.Fatal("invalid saved definition")
	}
	for _, field := range []string{"provider", "base_url", "api", "engine", "preferences"} {
		if mustJSON(saved[field]) != mustJSON(definition[field]) {
			t.Fatal("lost existing setting", field)
		}
	}
	if !reflect.DeepEqual(snapshot.Channels[0].Models, []string{"gpt-6-luna", "gpt-6-sol"}) {
		t.Fatal("saved routes were not retained", snapshot.Channels[0].Models)
	}
	if !reflect.DeepEqual(snapshot.Rules[0], retainedRule{KeyID: "other-key", Model: "untouched", Order: []string{"other", "peer"}, Disabled: []string{"peer"}}) {
		t.Fatal("changed another caller", snapshot.Rules)
	}
	if w := call("POST", "/v1/channel-management", body); w.Code != 409 || writes != 1 {
		t.Fatal("stale write accepted", w.Code, writes)
	}
}

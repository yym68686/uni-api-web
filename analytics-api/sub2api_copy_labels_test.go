package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestObservationUnboundCopyUsesExactRetiredOriginName(t *testing.T) {
	s := managementBindingTestService(t)
	ctx := context.Background()
	const origin, caller = "bananarouter", "local-only-caller"
	copyID := configuredImportName(origin, caller)
	var identityChanged, missingCaller, promoted, rotated, unavailable atomic.Bool
	var configReads atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("name lookup attempted %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/channel-controls":
			channels := []any{}
			if !promoted.Load() {
				channels = append(channels, map[string]any{"provider": copyID, "api_key_id": caller, "identity_changed": identityChanged.Load(), "models": []string{"gemini-4-argon"}})
			}
			writeJSON(w, 200, map[string]any{"revision": "boot:1", "temporary_channels": channels})
		case "/v1/channel-settings/providers":
			configReads.Add(1)
			if unavailable.Load() {
				http.Error(w, "temporarily unavailable", 503)
				return
			}
			key := "local-only-routing"
			if rotated.Load() {
				key = "local-only-rotated"
			}
			writeJSON(w, 200, map[string]any{"providers": []configuredProvider{{Provider: copyID, Base: "https://api.bananarouter.test/v1", API: key, IdentityChanged: true}}})
		case "/v1/api-keys":
			keys := []any{}
			if !missingCaller.Load() {
				keys = append(keys, map[string]any{"key_id": caller, "position": 1})
			}
			writeJSON(w, 200, map[string]any{"data": keys})
		case "/v1/model-channels":
			// The native provider no longer appears in the effective catalog.
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"provider": copyID, "model": "gemini-4-argon", "engine": "gemini"}}})
		default:
			t.Errorf("unexpected upstream request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	src := controlSource{sourceView: sourceView{ID: "copy-source", Name: "Source", Base: gateway.URL}, Key: "local-only-gateway"}
	if _, err := s.control.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	p := configuredProvider{Provider: origin, Base: "https://api.bananarouter.test/v1", API: "local-only-routing"}
	if err := s.saveConfiguredInventory(ctx, src, []configuredProvider{p}); err != nil {
		t.Fatal(err)
	}
	p.Provider = copyID
	if err := s.saveConfiguredInventory(ctx, src, []configuredProvider{p}); err != nil {
		t.Fatal(err)
	}
	token, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	read := func(query, want string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/sub2api/channels"+query, nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result struct {
			Labels      map[string]map[string]string         `json:"labels"`
			Data        []subInstalledChannel                `json:"data"`
			Multipliers map[string]map[string]*float64       `json:"multipliers"`
			Billing     map[string]map[string]map[string]any `json:"model_billing"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if got := result.Labels[src.ID][copyID]; got != want {
			t.Fatalf("copy label = %q, want %q", got, want)
		}
		for _, item := range result.Data {
			if item.Provider == copyID && (item.AccountID != "" || item.GroupID != 0 || len(item.BoundKeys) != 0 || item.BindingStatus != "no_account") {
				t.Fatal("display label invented an account binding", item)
			}
		}
		if result.Multipliers[src.ID][copyID] != nil || len(result.Billing[src.ID][copyID]) != 0 {
			t.Fatal("name fallback invented billing evidence")
		}
		if strings.Contains(w.Body.String(), "local-only-") {
			t.Fatal("display response exposed credentials")
		}
	}
	read("", origin)
	read("?view=bindings", "") // Editing/ownership discovery stays unchanged.
	identityChanged.Store(true)
	read("", "")
	identityChanged.Store(false)
	missingCaller.Store(true)
	read("", "")
	missingCaller.Store(false)
	if configReads.Load() != 0 {
		t.Fatal("temporary copy labels should not load configuration")
	}
	promoted.Store(true)
	read("", origin)
	rotated.Store(true)
	read("", "") // A plausible hostname must not override known conflicting credentials.
	rotated.Store(false)
	unavailable.Store(true)
	read("", "") // Optional display metadata cannot break the observation response.
	unavailable.Store(false)
	missingCaller.Store(true)
	read("", "")
	missingCaller.Store(false)
	if _, err := s.control.db.Exec(`UPDATE console_configured_channels SET key_hashes='["rotated"]' WHERE provider=$1`, copyID); err != nil {
		t.Fatal(err)
	}
	promoted.Store(false)
	read("", "")
	promoted.Store(true)
	if _, err := s.control.db.Exec(`DELETE FROM console_configured_channels WHERE provider=$1`, origin); err != nil {
		t.Fatal(err)
	}
	read("", origin) // Older promoted copies may predate the origin inventory.
	if configReads.Load() != 4 {
		t.Fatalf("unexpected optional config reads: %d", configReads.Load())
	}
}

func TestConfiguredCopyDisplayLabelsRequireExactIdentityEvidence(t *testing.T) {
	src := controlSource{sourceView: sourceView{ID: "source", Base: "https://gateway.test"}, Key: "gateway"}
	const origin, caller = "bananarouter", "caller"
	copyID := configuredImportName(origin, caller)
	identity := func(provider, site, credential string, active bool) configuredDisplayIdentity {
		item := configuredDisplayIdentity{Provider: provider, Site: site, Hashes: []string{tokenHash(credential)}, Active: active}
		raw, _ := json.Marshal(item.Hashes)
		item.Fingerprint = tokenHash(controlTarget(src) + "\n" + provider + "\n" + site + "\n" + string(raw))
		return item
	}
	original := identity(origin, "https://banana.test/tenant", "private", false)
	copy := identity(copyID, original.Site, "private", true)
	for _, name := range []string{"exact_retired_origin", "active_origin", "other_caller", "other_source_target", "other_site", "other_tenant_path", "changed_credentials", "inactive_copy", "invalid_fingerprint", "missing_origin", "custom_provider"} {
		t.Run(name, func(t *testing.T) {
			source, key := src, caller
			a, b := original, copy
			want := ""
			switch name {
			case "exact_retired_origin":
				want = origin
			case "active_origin":
				a.Active = true
				want = origin
			case "other_caller":
				key = "different-caller"
			case "other_source_target":
				source.Base = "https://different-gateway.test"
			case "other_site":
				b = identity(copyID, "https://different.test/tenant", "private", true)
			case "other_tenant_path":
				b = identity(copyID, "https://banana.test/different", "private", true)
			case "changed_credentials":
				b = identity(copyID, original.Site, "rotated", true)
			case "inactive_copy":
				b.Active = false
			case "invalid_fingerprint":
				b.Fingerprint = "invalid"
			case "missing_origin":
				a = configuredDisplayIdentity{}
			case "custom_provider":
				b.Provider = "custom-name"
			}
			got := configuredCopyDisplayLabels(source, map[string][]string{b.Provider: {key}}, []configuredDisplayIdentity{a, b}, nil)
			if got[b.Provider] != want {
				t.Fatalf("labels = %v, want %q", got, want)
			}
		})
	}
}

func TestManagementCopyNameUsesLiveIdentityAndRetiredOriginWithoutBinding(t *testing.T) {
	s := managementBindingTestService(t)
	ctx := context.Background()
	src := controlSource{sourceView: sourceView{ID: "source", Name: "Source", Base: "https://gateway.test"}, Key: "gateway"}
	if _, err := s.control.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	const origin, caller, model = "example-origin", "caller", "model"
	copyID := configuredImportName(origin, caller)
	native := configuredProvider{Provider: origin, Base: "https://upstream.test/v1", API: "upstream-key"}
	if err := s.saveConfiguredInventory(ctx, src, []configuredProvider{native}); err != nil {
		t.Fatal(err)
	}
	if err := s.saveConfiguredInventory(ctx, src, nil); err != nil {
		t.Fatal(err)
	}
	copy := native
	copy.Provider, copy.Temporary = copyID, true
	for _, tc := range []struct {
		name, origin, base, want      string
		missingKey, promoted, rotated bool
	}{
		{name: "matching live copy", want: origin},
		{name: "promoted copy with historical origin", want: origin, promoted: true},
		{name: "promoted copy predating inventory", origin: "other-origin", base: "https://api.other-origin.test/v1", want: "other-origin", promoted: true},
		{name: "unrelated hostname isn't proof", origin: "missing-origin", base: "https://api.other-origin.test/v1", promoted: true},
		{name: "missing caller", want: copyID, missingKey: true},
		{name: "rotated upstream credential", want: copyID, rotated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := copy
			want := tc.want
			if tc.origin != "" {
				live.Provider = configuredImportName(tc.origin, caller)
			}
			if tc.base != "" {
				live.Base = tc.base
			}
			if want == "" {
				want = live.Provider
			}
			if tc.rotated {
				live.API = "different-key"
			}
			live.Temporary = !tc.promoted
			facts := managementGatewayFacts{Source: src, Providers: []configuredProvider{live},
				Catalog: []managementModel{{Provider: live.Provider, Model: model, Engine: "gpt"}},
			}
			if live.Temporary {
				facts.Controls = map[string]any{"temporary_channels": []any{map[string]any{
					"provider": live.Provider, "api_key_id": caller, "models": []string{model},
				}}}
			}
			var keys []string
			if !tc.missingKey {
				keys = []string{"another-key", caller}
				facts.Keys = []managementGatewayKey{{ID: caller, Position: 1}}
				facts.Routes = map[string][]managementModel{caller: facts.Catalog}
			}
			labels, err := readManagementCopyLabels(ctx, s.control.db, src, managementSnapshotFromFacts(facts), keys)
			if err != nil {
				t.Fatal(err)
			}
			view := managementDatabaseView{CopyLabels: map[string]map[string]string{src.ID: labels}}
			if err := assembleManagementGatewayView(&view, []managementGatewayFacts{facts}, "owner"); err != nil {
				t.Fatal(err)
			}
			rows, err := buildManagementQueryRows(view.Input, "", "")
			if err != nil || len(rows) != 1 {
				t.Fatalf("management rows=%d err=%v", len(rows), err)
			}
			row := rows[0]
			if row.Configured == nil || row.Target.Name != want || row.Configured.Name != want || row.Target.Channel != live.Provider || len(row.AccountIDs) != 0 || len(view.Input.Imports) != 0 {
				t.Fatalf("expected a display-only name %q, got %#v", want, row.Target)
			}
			if want != live.Provider {
				filtered := filterManagementQueryRows(rows, managementQueryFilters{Search: want, GroupStatus: "all", AccountID: "__unassigned__"}, managementFilterEvidence{})
				if len(filtered.Rows) != 1 || view.Input.Routes[src.ID].Data[0].Provider != live.Provider || view.Input.Routes[src.ID].Data[0].OriginProvider != "" {
					t.Fatal("expected search by label without changing routing")
				}
			}
		})
	}
}

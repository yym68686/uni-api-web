package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagementGatewayCredentialChecksOverlapWithoutWeakeningFence(t *testing.T) {
	for _, mode := range []string{"stable", "changed", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			adminStarted, observationStarted := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var other <-chan struct{}
				if r.URL.Path == "/v1/channel-settings/management-revision" {
					if r.Header.Get("Authorization") != "Bearer fixture-admin" {
						t.Error("admin credential changed")
					}
					close(adminStarted)
					other = observationStarted
				} else if r.URL.Path == "/v1/channel-controls" {
					if r.Header.Get("Authorization") != "Bearer fixture-observer" {
						t.Error("observation credential changed")
					}
					close(observationStarted)
					other = adminStarted
				} else {
					t.Error("unexpected endpoint")
					http.NotFound(w, r)
					return
				}
				select {
				case <-other:
				case <-r.Context().Done():
					return
				}
				if mode == "unauthorized" && r.URL.Path == "/v1/channel-controls" {
					http.Error(w, "denied", 403)
					return
				}
				revision := "r1"
				if mode == "changed" && r.URL.Path == "/v1/channel-controls" {
					revision = "r2"
				}
				fmt.Fprintf(w, `{"format":"management-revision-v1","revision":%q,"config_revision":"c1"}`, revision)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := verifyManagementGateway(ctx, managementGatewayFacts{AtomicSnapshot: true, Source: controlSource{sourceView: sourceView{Base: server.URL}, Key: "fixture-observer", ConfigKey: "fixture-admin"}, Revision: "r1", ConfigRevision: "c1"})
			if mode == "stable" && err != nil || mode != "stable" && err == nil {
				t.Fatal("credential/fence result", err)
			}
			if ctx.Err() != nil {
				t.Fatal("credential reads did not overlap")
			}
		})
	}
}

func TestManagementGatewayReadsAreBoundedVersionFencedAndReadOnly(t *testing.T) {
	for _, mode := range []string{"stable", "changed", "old-catalog", "wrong-key", "missing-provider", "duplicate-provider", "unknown-key-provider"} {
		t.Run(mode, func(t *testing.T) {
			var revision atomic.Int64
			revision.Store(1)
			var concurrent, maximum, controls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := concurrent.Add(1)
				defer concurrent.Add(-1)
				for {
					old := maximum.Load()
					if n <= old || maximum.CompareAndSwap(old, n) {
						break
					}
				}
				if r.Method != "GET" {
					t.Error("management read issued a mutation")
					http.Error(w, "bad method", 400)
					return
				}
				switch r.URL.Path {
				case "/v1/channel-settings/management-snapshot":
					http.NotFound(w, r)
				case "/v1/channel-controls":
					controls.Add(1)
					writeJSON(w, 200, map[string]any{"revision": fmt.Sprint(revision.Load()), "config_revision": "config-1", "rules": []any{}, "temporary_channels": []any{}})
				case "/v1/api-keys":
					keys := []managementGatewayKey{}
					for i := 0; i < 6; i++ {
						keys = append(keys, managementGatewayKey{ID: fmt.Sprint(i), Position: i + 1, Prefix: "masked"})
					}
					writeJSON(w, 200, map[string]any{"snapshot_revision": "config-1", "data": keys})
				case "/v1/channel-settings/providers":
					if r.Header.Get("Authorization") != "Bearer config-secret" {
						t.Error("wrong configuration identity")
					}
					providers := []any{map[string]any{"provider": "native", "base_url": "https://fixture.test", "api": "private-upstream-key"}}
					if mode == "missing-provider" {
						providers = []any{}
					}
					if mode == "duplicate-provider" {
						providers = append(providers, providers[0])
					}
					writeJSON(w, 200, map[string]any{"providers": providers})
				case "/v1/model-channels":
					if r.URL.Query().Get("endpoint") != "all" || r.URL.Query().Get("stream") != "all" {
						t.Error("catalog scope changed")
					}
					selected := r.URL.Query().Get("api_key_id")
					if selected != "" {
						time.Sleep(5 * time.Millisecond)
					}
					if mode == "changed" && selected == "2" {
						revision.Store(2)
					}
					if mode == "wrong-key" && selected == "2" {
						selected = "1"
					}
					config := "config-1"
					if mode == "old-catalog" {
						config = "config-old"
					}
					provider := "native"
					if mode == "unknown-key-provider" && selected == "2" {
						provider = "missing"
					}
					writeJSON(w, 200, map[string]any{"snapshot_revision": config, "api_key_id": selected, "data": []any{map[string]any{"provider": provider, "model": "public", "upstream_model": "actual", "engine": "gpt"}}})
				default:
					t.Error("unexpected endpoint; no model requests are permitted", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			source := controlSource{sourceView: sourceView{ID: "fixture", Name: "fixture", Base: server.URL}, Key: "catalog-secret", ConfigKey: "config-secret"}
			facts, err := readManagementGateway(context.Background(), source)
			if maximum.Load() > 4 {
				t.Fatal("unbounded gateway reads", maximum.Load())
			}
			if mode != "stable" {
				if err == nil {
					t.Fatal("incomplete or mixed source snapshot accepted")
				}
				if mode == "changed" || mode == "old-catalog" {
					if !errors.Is(err, errManagementSourceRevision) {
						t.Fatal(err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(facts.Routes) != 6 || len(facts.Catalog) != 1 || controls.Load() != 2 {
				t.Fatal("source read incomplete")
			}
			serialized := mustJSON(facts)
			for _, secret := range []string{"catalog-secret", "config-secret", "private-upstream-key"} {
				if strings.Contains(serialized, secret) {
					t.Fatal("private source data serialized")
				}
			}
			routes := managementGatewayRoutes(facts)
			if len(routes.Data) != 6 || routes.SnapshotConsistent == nil || !*routes.SnapshotConsistent || len(routes.UnavailableKeys) != 0 || routes.Revision != facts.Revision || !routes.BatchRevisions || !routes.AtomicBatch || !routes.OptimizeBatch {
				t.Fatal("route scope incomplete")
			}
			for i, route := range routes.Data {
				if route.KeyID != fmt.Sprint(i) || route.Model != "public" || route.Upstream != "actual" || route.Position != 1 {
					t.Fatal("route identity/order changed", route)
				}
			}
			revision.Store(3)
			if err := verifyManagementGateway(context.Background(), facts); !errors.Is(err, errManagementSourceRevision) {
				t.Fatal("post-database gateway change accepted", err)
			}
		})
	}
}

func TestManagementGatewayAtomicSnapshotIsAcceptedAsOneRevision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/channel-controls" {
			if r.Header.Get("Authorization") != "Bearer catalog-secret" {
				t.Error("observation credential was not verified")
			}
			writeJSON(w, 200, map[string]any{"revision": "runtime-1", "config_revision": "config-1"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer config-secret" {
			t.Error("snapshot used a non-admin identity")
		}
		if r.URL.Path == "/v1/channel-settings/management-revision" {
			writeJSON(w, 200, map[string]any{"format": "management-revision-v1", "revision": "runtime-1", "config_revision": "config-1"})
			return
		}
		if r.URL.Path != "/v1/channel-settings/management-snapshot" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, 200, map[string]any{
			"format": "management-snapshot-v1", "revision": "runtime-1", "config_revision": "config-1",
			"controls":  map[string]any{"revision": "runtime-1", "config_revision": "config-1"},
			"keys":      []any{map[string]any{"key_id": "k1", "prefix": "masked", "position": 1}},
			"catalog":   []any{map[string]any{"provider": "native", "model": "public", "upstream_model": "actual", "engine": "gpt", "engine_mode": "auto"}},
			"routes":    map[string]any{"k1": []any{map[string]any{"provider": "native", "model": "public", "upstream_model": "actual", "engine": "gpt", "engine_mode": "auto"}}},
			"providers": []any{map[string]any{"provider": "native", "base_url": "https://site.test/v1", "api": []any{"opaque"}}},
			"export":    map[string]any{"revision": "runtime-1", "channel_settings": map[string]any{}, "temporary_definitions": map[string]any{}},
		})
	}))
	defer server.Close()
	source := controlSource{sourceView: sourceView{ID: "fixture", Name: "fixture", Base: server.URL}, Key: "catalog-secret", ConfigKey: "config-secret"}
	facts, err := readManagementGateway(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.AtomicSnapshot || facts.Revision != "runtime-1" || facts.ConfigRevision != "config-1" || len(facts.Routes["k1"]) != 1 || facts.Catalog[0].Provider != "native" {
		t.Fatalf("atomic snapshot was not preserved: %+v", facts)
	}
	if err := verifyManagementGateway(context.Background(), facts); err != nil {
		t.Fatal(err)
	}
}

func TestManagementGatewayAtomicRejectsIncompleteOrUnauthorizedReads(t *testing.T) {
	for _, mode := range []string{"forbidden", "unavailable", "conflict", "format", "missing-export", "mixed-export", "mixed-controls", "duplicate-provider", "duplicate-key", "missing-key-routes", "extra-key-routes", "unknown-provider", "revoked-observer", "changed-fence", "revoked-at-fence"} {
		t.Run(mode, func(t *testing.T) {
			var legacy, observerReads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("read attempted a mutation")
				}
				switch r.URL.Path {
				case "/v1/channel-settings/management-snapshot":
					status := map[string]int{"forbidden": 403, "unavailable": 503, "conflict": 409}[mode]
					if status != 0 {
						http.Error(w, "private upstream diagnostic", status)
						return
					}
					row := managementModel{Provider: "native", Model: "m"}
					raw := map[string]any{
						"format": "management-snapshot-v1", "revision": "r1", "config_revision": "c1",
						"controls": map[string]any{"revision": "r1", "config_revision": "c1"},
						"export":   map[string]any{"revision": "r1", "channel_settings": map[string]any{}, "temporary_definitions": map[string]any{}},
						"keys":     []managementGatewayKey{{ID: "key"}}, "catalog": []managementModel{row},
						"routes":    map[string][]managementModel{"key": {row}},
						"providers": []configuredProvider{{Provider: "native", API: "private-provider"}},
					}
					switch mode {
					case "format":
						raw["format"] = "unknown"
					case "missing-export":
						delete(raw, "export")
					case "mixed-export":
						raw["export"].(map[string]any)["revision"] = "r2"
					case "mixed-controls":
						raw["controls"].(map[string]any)["config_revision"] = "c2"
					case "duplicate-provider":
						raw["providers"] = []configuredProvider{{Provider: "native"}, {Provider: "native"}}
					case "duplicate-key":
						raw["keys"] = []managementGatewayKey{{ID: "key"}, {ID: "key"}}
					case "missing-key-routes":
						raw["routes"] = map[string][]managementModel{}
					case "extra-key-routes":
						raw["routes"].(map[string][]managementModel)["extra"] = []managementModel{}
					case "unknown-provider":
						raw["catalog"] = []managementModel{{Provider: "other", Model: "m"}}
					}
					writeJSON(w, 200, raw)
				case "/v1/channel-controls":
					n := observerReads.Add(1)
					if mode == "revoked-observer" || mode == "revoked-at-fence" && n > 1 {
						http.Error(w, "revoked", 403)
						return
					}
					writeJSON(w, 200, map[string]any{"revision": "r1", "config_revision": "c1"})
				case "/v1/channel-settings/management-revision":
					revision := "r1"
					if mode == "changed-fence" {
						revision = "r2"
					}
					writeJSON(w, 200, map[string]any{"format": "management-revision-v1", "revision": revision, "config_revision": "c1"})
				default:
					legacy.Add(1)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			source := controlSource{sourceView: sourceView{Base: server.URL}, Key: "observer", ConfigKey: "admin"}
			facts, err := readManagementGateway(context.Background(), source)
			if mode == "changed-fence" || mode == "revoked-at-fence" {
				if err != nil {
					t.Fatal("initial read failed", err)
				}
				err = verifyManagementGateway(context.Background(), facts)
			}
			if err == nil || legacy.Load() != 0 {
				t.Fatal("invalid snapshot accepted or silently downgraded", err, legacy.Load())
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("private diagnostics escaped")
			}
		})
	}
}

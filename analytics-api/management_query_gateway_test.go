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

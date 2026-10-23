package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestManagementSnapshotFencesSourcesAndGatewayAroundReadOnlyDatabase(t *testing.T) {
	for _, mode := range []string{"stable", "gateway-changed", "source-changed-before-db", "source-disabled-after-db", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			s := managementBindingTestService(t)
			s.engine = postgresTestEngine(t)
			ctx := context.WithValue(context.Background(), consoleRequestIDKey{}, "management-trace-test")
			var controls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("snapshot attempted a gateway mutation")
				}
				switch r.URL.Path {
				case "/v1/channel-settings/management-snapshot":
					http.NotFound(w, r)
				case "/v1/channel-settings/management-revision":
					writeJSON(w, 200, map[string]any{"format": "management-revision-v1", "revision": "1", "config_revision": "1"})
				case "/v1/channel-controls":
					n := controls.Add(1)
					revision := "1"
					if n == 3 {
						if mode == "gateway-changed" {
							revision = "2"
						}
						if mode == "source-disabled-after-db" {
							if _, err := s.control.db.Exec(`UPDATE console_sources SET enabled=false WHERE id='source'`); err != nil {
								t.Error(err)
							}
						}
					}
					if n == 2 && mode == "source-changed-before-db" {
						if _, err := s.control.db.Exec(`UPDATE console_sources SET name='Changed' WHERE id='source'`); err != nil {
							t.Error(err)
						}
					}
					writeJSON(w, 200, map[string]any{"revision": revision, "config_revision": "1", "temporary_channels": []any{}})
				case "/v1/api-keys":
					if mode == "unavailable" {
						http.Error(w, "unavailable", 503)
						return
					}
					writeJSON(w, 200, map[string]any{"snapshot_revision": "1", "data": []any{}})
				case "/v1/model-channels":
					writeJSON(w, 200, map[string]any{"snapshot_revision": "1", "api_key_id": "", "data": []any{map[string]any{"provider": "native", "model": "gpt-5.5", "engine": "gpt"}}})
				case "/v1/channel-settings/providers":
					writeJSON(w, 200, map[string]any{"providers": []configuredProvider{{Provider: "native", Base: "https://site.test/tenant/v1", API: "upstream-secret"}}})
				default:
					t.Error("unexpected gateway call", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			if _, err := s.control.saveSource(ctx, controlSource{sourceView: sourceView{ID: "source", Name: "Source", Base: server.URL}, Key: "source-secret"}, false); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			view, err := s.readManagementSnapshot(ctx, "owner")
			for _, secret := range []string{"source-secret", "upstream-secret", server.URL} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("private source details escaped stage logs")
				}
			}
			for _, stage := range []string{"sources", "gateway_atomic", "gateway"} {
				if !strings.Contains(logs.String(), "stage="+stage+" ") || !strings.Contains(logs.String(), "request_id=management-trace-test") {
					t.Fatal("outer management stage was not traced", stage, logs.String())
				}
			}
			if mode != "stable" {
				if err == nil || len(view.Input.Channels) != 0 || len(view.Sources) != 0 {
					t.Fatal("partial or mixed snapshot published", err)
				}
				if mode != "unavailable" && !errors.Is(err, errManagementSourceRevision) {
					t.Fatal("wrong fence error", err)
				}
				return
			}
			if err != nil || len(view.Input.Channels) != 1 || len(view.Input.Imports) != 1 || view.ReadAt.IsZero() || controls.Load() != 3 {
				t.Fatal("incomplete fresh snapshot", err)
			}
			for _, stage := range []string{"database", "gateway_fence"} {
				if !strings.Contains(logs.String(), "stage="+stage+" ") {
					t.Fatal("snapshot phase was not traced", stage)
				}
			}
			if view.Input.Channels[0].BindingStatus != "no_account" || view.Input.Channels[0].AccountID != "" {
				t.Fatal("unowned channel acquired account")
			}
			for _, secret := range []string{"source-secret", "upstream-secret"} {
				if strings.Contains(mustJSON(view), secret) {
					t.Fatal("credential escaped view")
				}
			}
			// No periodic inventory or retained snapshot writes are needed to read.
			var count int
			if err := s.control.db.QueryRow(`SELECT (SELECT count(*) FROM console_quality_group_links)+(SELECT count(*) FROM console_configured_channels)+(SELECT count(*) FROM console_control_snapshots)`).Scan(&count); err != nil || count != 0 {
				t.Fatal("snapshot persisted derived state", count, err)
			}
		})
	}
}

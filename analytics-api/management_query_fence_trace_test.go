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

func TestManagementGatewayFenceTracePreservesErrorsAndReadCount(t *testing.T) {
	for _, document := range []string{"admin", "observer", "legacy"} {
		for _, change := range []string{"none", "revision", "config", "both", "missing", "wrong-type"} {
			t.Run(document+"/"+change, func(t *testing.T) {
				var reads atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					reads.Add(1)
					if r.Method != http.MethodGet {
						t.Error("fence attempted a mutation")
					}
					admin := r.URL.Path == "/v1/channel-settings/management-revision"
					raw := map[string]any{"format": "management-revision-v1", "revision": "private-runtime", "config_revision": "private-config"}
					if document == "legacy" || (document == "admin") == admin {
						switch change {
						case "revision":
							raw["revision"] = "private-new-runtime"
						case "config":
							raw["config_revision"] = "private-new-config"
						case "both":
							raw["revision"], raw["config_revision"] = "private-new-runtime", "private-new-config"
						case "missing":
							delete(raw, "revision")
							delete(raw, "config_revision")
						case "wrong-type":
							raw["revision"], raw["config_revision"] = []any{"private-runtime"}, map[string]any{"value": "private-config"}
						}
					}
					writeJSON(w, 200, raw)
				}))
				defer server.Close()
				facts := managementGatewayFacts{
					Source:   controlSource{sourceView: sourceView{ID: "private-source", Base: server.URL}, Key: "private-key"},
					Revision: "private-runtime", ConfigRevision: "private-config", AtomicSnapshot: document != "legacy",
				}
				if document == "observer" {
					facts.Source.ConfigKey = "private-admin-key"
				}
				var output bytes.Buffer
				previous := log.Writer()
				ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "fence-trace-123")
				func() {
					log.SetOutput(&output)
					defer log.SetOutput(previous)
					err := verifyManagementGateway(ctx, facts)
					if change == "none" && err != nil || change != "none" && !errors.Is(err, errManagementSourceRevision) {
						t.Fatal("fence behavior changed", err)
					}
				}()
				wantReads := int32(1)
				if document == "observer" {
					wantReads = 2
				}
				if reads.Load() != wantReads {
					t.Fatal("diagnostics added gateway reads", reads.Load())
				}
				logged := output.String()
				if change == "none" {
					if strings.Contains(logged, "management_gateway_fence_rejected") {
						t.Fatal("stable fence logged a rejection")
					}
				} else {
					want := "document=" + document + " revision_changed="
					if change == "config" {
						want += "false"
					} else {
						want += "true"
					}
					want += " config_changed="
					if change == "revision" {
						want += "false"
					} else {
						want += "true"
					}
					want += " request_id=fence-trace-123"
					if !strings.Contains(logged, want) {
						t.Fatal("missing fixed rejection fields", logged)
					}
				}
				for _, private := range []string{"private-", server.URL} {
					if strings.Contains(logged, private) {
						t.Fatal("fence diagnostics exposed a private value")
					}
				}
			})
		}
	}
}

func TestManagementGatewayFenceTraceIsDisabledWithoutTraceContext(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	if !managementGatewayFenceChanged(context.Background(), "admin", nil, managementGatewayFacts{Revision: "private"}) || output.Len() != 0 {
		t.Fatal("trace context changed fence decision or emitted diagnostics")
	}
}

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBatchSharedEvidenceRequiresIdenticalUpstreamAndNoLocalFailure(t *testing.T) {
	s, account, target := bindingFixture(t, "https://site.test")
	ctx := context.Background()
	definitions := map[string]map[string]any{}
	for _, id := range []string{"target", "peer"} {
		definitions[id] = map[string]any{"provider": "native", "base_url": "https://upstream.test/v1", "api": "upstream-fixture-key", "engine": "gemini", "model": []any{"old"}, "preferences": map[string]any{"headers": map[string]any{"x-mode": "same"}}}
	}
	revisions := map[string]string{"target": "r1", "peer": "r1"}
	global := map[string]map[string]any{"target": {}, "peer": {}}
	overrides := map[string]map[string]any{"target": {}, "peer": {}}
	server := func(id string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				t.Errorf("unexpected mutation: %s", r.Method)
			}
			d := definitions[id]
			switch r.URL.Path {
			case "/v1/channel-settings/providers":
				writeJSON(w, 200, map[string]any{"providers": []configuredProvider{{Provider: "native", Base: d["base_url"].(string), API: d["api"]}}})
			case "/v1/channel-settings/export":
				writeJSON(w, 200, map[string]any{"revision": "r1", "channel_settings": overrides[id], "temporary_definitions": map[string]any{}})
			case "/v1/api_config":
				writeJSON(w, 200, map[string]any{"api_config": map[string]any{"providers": []any{d}, "preferences": global[id]}})
			case "/v1/channel-controls":
				writeJSON(w, 200, map[string]any{"revision": revisions[id]})
			case "/v1/model-channels":
				writeJSON(w, 200, map[string]any{"data": []batchCatalogRow{{Provider: "native", Model: "old", Upstream: "old"}}})
			default:
				http.NotFound(w, r)
			}
		}))
	}
	local, remote := server("target"), server("peer")
	defer local.Close()
	defer remote.Close()
	target.Base = local.URL
	peer := target
	peer.ID += "-peer"
	peer.Base = remote.URL
	if _, err := s.control.saveSource(ctx, peer, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_sources WHERE id=$1`, peer.ID) })
	seed := func(src controlSource, status, state, fingerprint string) {
		t.Helper()
		if _, err := s.control.db.Exec(`INSERT INTO console_configured_checks(source_id,provider,kind,model,source_target,fingerprint,state,result) VALUES($1,'native','availability','new-model',$2,$3,$4,$5) ON CONFLICT(source_id,provider,kind,model) DO UPDATE SET fingerprint=excluded.fingerprint,state=excluded.state,result=excluded.result`, src.ID, controlTarget(src), fingerprint, state, mustJSON(subResult{CheckedAt: 10, Availability: subProbe{Status: status}})); err != nil {
			t.Fatal(err)
		}
	}
	fingerprint := func(src controlSource) string {
		return configuredProbeFingerprint(src, configuredProvider{Provider: "native", Base: "https://upstream.test/v1", API: "upstream-fixture-key"})
	}
	check := func(want bool) {
		t.Helper()
		err := s.validateConfiguredModelChanges(ctx, target, "native", account.Owner, nil, map[string]string{"new-model": "new-model"}, peer.ID)
		if (err == nil) != want {
			t.Fatalf("want %v: %v", want, err)
		}
	}
	seed(peer, "success", "done", fingerprint(peer))
	check(true)
	seed(peer, "success", "done", "stale-credentials")
	check(false)
	seed(peer, "success", "done", fingerprint(peer))
	definitions["peer"]["api"] = "different-key"
	check(false)
	definitions["peer"]["api"] = "upstream-fixture-key"
	definitions["peer"]["base_url"] = "https://other.test/v1"
	check(false)
	definitions["peer"]["base_url"] = "https://upstream.test/v1"
	definitions["peer"]["engine"] = "claude"
	check(false)
	definitions["peer"]["engine"] = "gemini"
	definitions["peer"]["preferences"] = map[string]any{"headers": map[string]any{"x-mode": "different"}}
	check(false)
	definitions["peer"]["preferences"] = definitions["target"]["preferences"]
	definitions["peer"]["model"] = []any{map[string]any{"different": "old"}}
	check(false)
	definitions["peer"]["model"] = definitions["target"]["model"]
	global["peer"]["model_timeout"] = 99
	check(false)
	delete(global["peer"], "model_timeout")
	overrides["peer"]["native"] = map[string]any{"set": map[string]any{"/preferences/headers/x-mode": "changed"}, "remove": []string{}}
	check(false)
	delete(overrides["peer"], "native")
	revisions["peer"] = "changed"
	check(false)
	revisions["peer"] = "r1"
	seed(target, "error", "done", fingerprint(target))
	check(false)
	seed(target, "success", "running", fingerprint(target))
	check(false)
	if _, err := s.control.db.Exec(`DELETE FROM console_configured_checks WHERE source_id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	seed(peer, "error", "done", fingerprint(peer))
	check(false)
	seed(peer, "success", "running", fingerprint(peer))
	check(false)
	seed(peer, "success", "done", fingerprint(peer))
	check(true)
	if err := s.validateConfiguredModelChanges(ctx, target, "native", account.Owner, nil, map[string]string{"new-model": "new-model"}); err == nil {
		t.Fatal("ordinary save borrowed cross-source proof without explicit batch anchor")
	}
}

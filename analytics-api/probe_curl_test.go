package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestProbeCurlCapturesActualRequestsAndReplaysWithoutShellExpansion(t *testing.T) {
	store := &controlStore{key: []byte(strings.Repeat("k", 32))}
	for _, mode := range []string{"responses", "quality", "messages", "gemini", "tool-use", "compaction", "targeted"} {
		t.Run(mode, func(t *testing.T) {
			var received []probeCurlSnapshot
			var receivedMu sync.Mutex
			readReceived := func() []probeCurlSnapshot {
				receivedMu.Lock()
				defer receivedMu.Unlock()
				return append([]probeCurlSnapshot(nil), received...)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				receivedMu.Lock()
				received = append(received, probeCurlSnapshot{Method: r.Method, URL: r.URL.String(), Headers: r.Header.Clone(), Body: string(body)})
				receivedMu.Unlock()
				w.WriteHeader(400)
			}))
			defer server.Close()
			ctx := withProbeCurl(context.Background(), store, probeCurlScope{Account: "fixture-account"})
			client := server.Client()
			key := "fixture-'secret-$HOME-`whoami`"
			marker := filepath.Join(t.TempDir(), "must-not-exist")
			prompt := "say 'test' $(touch " + marker + ")\nsecond line"
			var probe subProbe
			switch mode {
			case "messages":
				probe = subProbeNative(ctx, client, server.URL, key, "claude-fixture")
			case "gemini":
				probe = subProbeNative(ctx, client, server.URL, key, "gemini-3.1-pro")
			case "tool-use":
				probe = subProbeToolUse(ctx, client, server.URL, key, "gpt-6-astra")
			case "compaction":
				probe = subProbeCompaction(ctx, client, server.URL, key, "gpt-6-astra")
			default:
				if mode == "quality" {
					prompt = checkPrompt
				}
				if mode == "targeted" {
					client.Transport = configuredProbeTransport{base: http.DefaultTransport, provider: "only-this-channel", allowUnconfigured: true}
				}
				probe = subProbeStream(ctx, client, server.URL, key, prompt, "gpt-6-astra")
			}
			if probe.CurlToken == "" || strings.Contains(mustJSON(probe), key) {
				t.Fatal("missing encrypted snapshot or plaintext credential")
			}
			raw, err := store.decrypt(probe.CurlToken)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot probeCurlSnapshot
			observed := readReceived()
			if json.Unmarshal([]byte(raw), &snapshot) != nil || len(observed) != 1 || snapshot.Body != observed[0].Body {
				t.Fatal("snapshot does not match actual body")
			}
			for name, values := range snapshot.Headers {
				if strings.Join(values, ",") != strings.Join(observed[0].Headers.Values(name), ",") {
					t.Fatalf("header mismatch: %s", name)
				}
			}
			for _, name := range []string{"User-Agent", "Accept-Encoding"} {
				if snapshot.Headers.Get(name) != observed[0].Headers.Get(name) {
					t.Fatalf("lost transport header %s", name)
				}
			}
			if mode == "gemini" && (!strings.Contains(snapshot.URL, "/v1beta/models/gemini-3.1-pro:streamGenerateContent?alt=sse") || snapshot.Headers.Get("x-goog-api-key") != key) {
				t.Fatal("incorrect Gemini protocol")
			}
			if mode == "messages" && (!strings.HasSuffix(snapshot.URL, "/v1/messages") || snapshot.Headers.Get("x-api-key") != key || snapshot.Headers.Get("anthropic-version") == "") {
				t.Fatal("incorrect Messages protocol")
			}
			if mode == "targeted" && snapshot.Headers.Get("X-Uni-API-Provider") != "only-this-channel" {
				t.Fatal("lost target pin")
			}
			if strings.Contains(mustJSON(compactSummary(probe)), "curl_token") {
				t.Fatal("snapshot reached summary")
			}
			// Execute curl only against this local fixture, including hostile shell text.
			if out, err := exec.Command("sh", "-c", probeCurl(snapshot)).CombinedOutput(); err != nil {
				t.Fatalf("curl failed: %v %s", err, out)
			}
			observed = readReceived()
			if len(observed) != 2 || observed[1].Body != observed[0].Body || observed[1].URL != observed[0].URL {
				t.Fatal("curl did not replay the exact request")
			}
			for name, values := range snapshot.Headers {
				if strings.Join(values, ",") != strings.Join(observed[1].Headers.Values(name), ",") {
					t.Fatalf("replay header mismatch: %s", name)
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("shell expansion executed")
			}
		})
	}
}

func TestProbeCurlExportAuthorizationAndLegacyFallback(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	owner := "curl-" + randomID()
	account := owner + "-account"
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, account)
	encrypted, _ := store.encrypt("current-fixture-key")
	_, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'fixture','https://fixture.test','fixture@test','')`, account, owner)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := newProbeRequest(ctx, "https://original.test", "original-fixture-key", "gpt-6-astra", "compaction", "", "probe-fixture")
	token := captureProbeCurl(withProbeCurl(ctx, store, probeCurlScope{Account: account}), http.DefaultClient, req)
	capability := subCapabilityResult{Attempts: []subProbe{{ID: "legacy-probe", RequestedModel: "gpt-6-astra", Protocol: "responses", Status: "supported"}}}
	_, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key,compaction) VALUES($1,1,'fixture','openai',$2,$3)`, account, encrypted, mustJSON(capability))
	if err != nil {
		t.Fatal(err)
	}
	session, _ := store.newSession(ctx, owner)
	foreign, _ := store.newSession(ctx, owner+"-other")
	service := &Service{control: store}
	call := func(session string, body any) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/sub2api/check-curl", strings.NewReader(mustJSON(body)))
		r.Header.Set("Content-Type", "application/json")
		if session != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		}
		w := httptest.NewRecorder()
		service.authenticate(service.controlHandler()).ServeHTTP(w, r)
		return w
	}
	exact := map[string]any{"token": token}
	if w := call("", exact); w.Code != 401 {
		t.Fatalf("anonymous status %d", w.Code)
	}
	if w := call(foreign, exact); w.Code != 404 || strings.Contains(w.Body.String(), "fixture-key") {
		t.Fatal("foreign account exported snapshot")
	}
	w := call(session, exact)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "original-fixture-key") || !strings.Contains(w.Body.String(), `"original":true`) {
		t.Fatalf("snapshot export failed: %d %s", w.Code, w.Body.String())
	}
	if w := call(session, map[string]any{"token": encrypted}); w.Code != 404 {
		t.Fatal("accepted a non-probe ciphertext")
	}
	legacy := map[string]any{"probe_id": "legacy-probe", "kind": "compaction", "targets": []probeCurlTarget{{Account: account, Group: 1}}}
	w = call(session, legacy)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "current-fixture-key") || !strings.Contains(w.Body.String(), `"original":false`) || !strings.Contains(w.Body.String(), "历史记录") {
		t.Fatalf("legacy export failed: %d %s", w.Code, w.Body.String())
	}
	if w := call(foreign, legacy); w.Code != 404 {
		t.Fatal("foreign account exported legacy request")
	}
	source := owner + "-source"
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, source)
	var providerAvailable atomic.Bool
	providerAvailable.Store(true)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/channel-settings/providers" || r.Header.Get("Authorization") != "Bearer fixture-admin-key" {
			t.Error("export sent an inference request or used the wrong configuration credential")
			http.Error(w, "unexpected request", 400)
			return
		}
		providers := []configuredProvider{}
		if providerAvailable.Load() {
			providers = append(providers, configuredProvider{Provider: "pinned-provider", Base: "https://upstream.test/tenant/v1/responses", API: []any{"upstream-fixture-key", "second-upstream-key"}})
		}
		writeJSON(w, 200, map[string]any{"providers": providers})
	}))
	defer gateway.Close()
	adminKey, _ := store.encrypt("fixture-admin-key")
	_, err = store.db.Exec(`INSERT INTO console_sources(id,name,base,encrypted_key,encrypted_config_key) VALUES($1,'fixture',$2,$3,$4)`, source, gateway.URL, encrypted, adminKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`INSERT INTO console_configured_checks(source_id,provider,kind,source_target,result) VALUES($1,'pinned-provider','compaction','fixture',$2)`, source, mustJSON(capability))
	if err != nil {
		t.Fatal(err)
	}
	sourceRequest := map[string]any{"probe_id": "legacy-probe", "kind": "compaction", "targets": []probeCurlTarget{{Source: source, Provider: "pinned-provider"}}}
	assertDirect := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != 200 || !strings.Contains(w.Body.String(), "https://upstream.test/tenant/v1/responses") || !strings.Contains(w.Body.String(), "upstream-fixture-key") || !strings.Contains(w.Body.String(), `"original":false`) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("source export did not use upstream: %d %s", w.Code, w.Body.String())
		}
		for _, forbidden := range []string{gateway.URL, "current-fixture-key", "fixture-admin-key", "second-upstream-key", "X-Uni-Api-Provider", "X-Uni-Api-Probe-Unconfigured-Model"} {
			if strings.Contains(w.Body.String(), forbidden) {
				t.Fatal("source export retained a gateway credential/header or unrelated key")
			}
		}
	}
	assertDirect(call(session, sourceRequest))
	sourceReq, _ := newProbeRequest(ctx, gateway.URL, "current-fixture-key", "gpt-5.6-luna", "responses", "say test", "original-probe")
	sourceClient := &http.Client{Transport: configuredProbeTransport{base: http.DefaultTransport, provider: "pinned-provider", allowUnconfigured: true}}
	sourceToken := captureProbeCurl(withProbeCurl(ctx, store, probeCurlScope{Source: source}), sourceClient, sourceReq)
	assertDirect(call(session, map[string]string{"token": sourceToken}))
	providerAvailable.Store(false)
	for _, body := range []any{sourceRequest, map[string]string{"token": sourceToken}} {
		if w := call(session, body); w.Code != 503 || strings.Contains(w.Body.String(), "fixture-key") {
			t.Fatal("missing provider silently fell back to the gateway")
		}
	}
	_, err = store.db.Exec(`UPDATE console_sources SET enabled=false WHERE id=$1`, source)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(session, map[string]string{"token": sourceToken}); w.Code != 404 {
		t.Fatal("disabled source export allowed")
	}
	if w := call(session, sourceRequest); w.Code != 404 {
		t.Fatal("disabled source legacy export allowed")
	}
	legacy["probe_id"] = "not-a-record"
	if w := call(session, legacy); w.Code != 404 {
		t.Fatal("invented a request for missing record")
	}
}

func TestUpstreamProbeSnapshotUsesChannelAddressAndProtocolCredential(t *testing.T) {
	for _, tt := range []struct {
		base, mode, model, wantURL, auth string
	}{
		{"https://upstream.test", "responses", "gpt-5.6-luna", "https://upstream.test/v1/responses", "Authorization"},
		{"https://upstream.test/v1/", "responses", "gpt-5.6-luna", "https://upstream.test/v1/responses", "Authorization"},
		{"https://upstream.test/tenant/v1/responses?route=one", "responses", "gpt-5.6-luna", "https://upstream.test/tenant/v1/responses?route=one", "Authorization"},
		{"https://upstream.test/openai/responses?api-version=fixture", "responses", "gpt-5.6-luna", "https://upstream.test/openai/responses?api-version=fixture", "Authorization"},
		{"https://upstream.test/tenant/v1/messages", "messages", "claude-fixture", "https://upstream.test/tenant/v1/messages", "X-Api-Key"},
		{"https://upstream.test/custom/messages", "messages", "claude-fixture", "https://upstream.test/custom/messages", "X-Api-Key"},
		{"https://upstream.test/tenant/v1beta/", "gemini", "gemini-3.1-pro", "https://upstream.test/tenant/v1beta/models/gemini-3.1-pro:streamGenerateContent?alt=sse", "X-Goog-Api-Key"},
	} {
		t.Run(tt.mode+"-"+tt.base, func(t *testing.T) {
			req, err := newProbeRequest(context.Background(), "https://gateway.test/prefix", "gateway-secret", tt.model, tt.mode, "say test", "original-probe")
			if err != nil {
				t.Fatal(err)
			}
			req = (configuredProbeTransport{provider: "only-this-channel", allowUnconfigured: true}).prepare(req)
			req.Header.Set("Cookie", "gateway-session")
			snapshot, _ := snapshotProbeRequest(req, probeCurlScope{Source: "fixture-source"})
			direct, err := upstreamProbeSnapshot(snapshot, "https://gateway.test/prefix", configuredProvider{Base: tt.base, API: []any{"upstream-secret", "other-secret"}})
			if err != nil {
				t.Fatal(err)
			}
			wantAuth := "upstream-secret"
			if tt.auth == "Authorization" {
				wantAuth = "Bearer " + wantAuth
			}
			if direct.URL != tt.wantURL || direct.Headers.Get(tt.auth) != wantAuth || direct.Body != snapshot.Body || direct.Headers.Get("X-Request-ID") != "original-probe" {
				t.Fatal("incorrect direct URL, authentication or original payload")
			}
			for _, secret := range []string{"gateway-secret", "gateway-session", "X-Uni-Api-Provider", "X-Uni-Api-Probe-Unconfigured-Model", "other-secret"} {
				if strings.Contains(probeCurl(direct), secret) {
					t.Fatal("gateway data leaked into upstream curl")
				}
			}
			if snapshot.Headers.Get("Cookie") != "gateway-session" || snapshot.Headers.Get("X-Uni-API-Provider") == "" {
				t.Fatal("mutated the stored snapshot")
			}
		})
	}
	for _, provider := range []configuredProvider{{Base: "https://upstream.test"}, {Base: "file:///tmp/fixture", API: "secret"}, {Base: "https://user:pass@upstream.test", API: "secret"}} {
		if _, err := upstreamProbeSnapshot(probeCurlSnapshot{}, "https://gateway.test", provider); err == nil {
			t.Fatal("accepted missing credentials or an invalid provider URL")
		}
	}
}

func TestUpstreamProbeSnapshotConvertsGatewayTextProbeToChannelProtocol(t *testing.T) {
	for _, tt := range []struct{ base, wantURL, auth, field string }{
		{"https://upstream.test/prefix/v1/chat/completions", "https://upstream.test/prefix/v1/chat/completions", "Authorization", "messages"},
		{"https://upstream.test/custom/chat/completions", "https://upstream.test/custom/chat/completions", "Authorization", "messages"},
		{"https://upstream.test/prefix/v1/messages", "https://upstream.test/prefix/v1/messages", "X-Api-Key", "messages"},
		{"https://upstream.test/prefix/v1beta", "https://upstream.test/prefix/v1beta/models/gemini-3.1-pro:streamGenerateContent?alt=sse", "X-Goog-Api-Key", "contents"},
	} {
		t.Run(tt.base, func(t *testing.T) {
			req, _ := newProbeRequest(context.Background(), "https://gateway.test", "gateway-secret", "gemini-3.1-pro", "responses", "say test", "probe")
			snapshot, _ := snapshotProbeRequest(req, probeCurlScope{Source: "source"})
			direct, err := upstreamProbeSnapshot(snapshot, "https://gateway.test", configuredProvider{Base: tt.base, API: "upstream-secret"})
			if err != nil || direct.URL != tt.wantURL || !strings.Contains(direct.Headers.Get(tt.auth), "upstream-secret") {
				t.Fatal("wrong direct protocol", err)
			}
			var body map[string]any
			if json.Unmarshal([]byte(direct.Body), &body) != nil || body[tt.field] == nil || body["input"] != nil || !strings.Contains(direct.Body, "say test") {
				t.Fatal("retained the gateway body for a different channel protocol")
			}
		})
	}
}

func TestProbeCurlLegacyQualityUsesItsActualPrompt(t *testing.T) {
	probe := subProbe{ID: "quality", RequestedModel: checkModel}
	raw := []byte(mustJSON(subResult{Availability: probe, Quality: probe}))
	if found, quality := findLegacyProbe(raw, "quality", "availability"); found == nil || !quality {
		t.Fatal("quality-only availability lost its prompt")
	}
	raw = []byte(mustJSON(subResult{Availability: subProbe{ID: "ordinary", RequestedModel: checkModel}, Quality: probe}))
	if found, _ := findLegacyProbe(raw, "quality", "availability"); found != nil {
		t.Fatal("used quality result for availability")
	}
}

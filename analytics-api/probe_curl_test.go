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
	"testing"
)

func TestProbeCurlCapturesActualRequestsAndReplaysWithoutShellExpansion(t *testing.T) {
	store := &controlStore{key: []byte(strings.Repeat("k", 32))}
	for _, mode := range []string{"responses", "quality", "messages", "gemini", "tool-use", "compaction", "targeted"} {
		t.Run(mode, func(t *testing.T) {
			var received []probeCurlSnapshot
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				received = append(received, probeCurlSnapshot{Method: r.Method, URL: r.URL.String(), Headers: r.Header.Clone(), Body: string(body)})
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
			if json.Unmarshal([]byte(raw), &snapshot) != nil || len(received) != 1 || snapshot.Body != received[0].Body {
				t.Fatal("snapshot does not match actual body")
			}
			for name, values := range snapshot.Headers {
				if strings.Join(values, ",") != strings.Join(received[0].Headers.Values(name), ",") {
					t.Fatalf("header mismatch: %s", name)
				}
			}
			for _, name := range []string{"User-Agent", "Accept-Encoding"} {
				if snapshot.Headers.Get(name) != received[0].Headers.Get(name) {
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
			if len(received) != 2 || received[1].Body != received[0].Body || received[1].URL != received[0].URL {
				t.Fatal("curl did not replay the exact request")
			}
			for name, values := range snapshot.Headers {
				if strings.Join(values, ",") != strings.Join(received[1].Headers.Values(name), ",") {
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
	_, err = store.db.Exec(`INSERT INTO console_sources(id,name,base,encrypted_key) VALUES($1,'fixture','https://gateway.test',$2)`, source, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`INSERT INTO console_configured_checks(source_id,provider,kind,source_target,result) VALUES($1,'pinned-provider','compaction','fixture',$2)`, source, mustJSON(capability))
	if err != nil {
		t.Fatal(err)
	}
	sourceRequest := map[string]any{"probe_id": "legacy-probe", "kind": "compaction", "targets": []probeCurlTarget{{Source: source, Provider: "pinned-provider"}}}
	if w := call(session, sourceRequest); w.Code != 200 || !strings.Contains(w.Body.String(), "X-Uni-Api-Provider: pinned-provider") || !strings.Contains(w.Body.String(), "current-fixture-key") {
		t.Fatalf("source export failed: %d %s", w.Code, w.Body.String())
	}
	sourceToken := captureProbeCurl(withProbeCurl(ctx, store, probeCurlScope{Source: source}), http.DefaultClient, req)
	if w := call(session, map[string]string{"token": sourceToken}); w.Code != 200 {
		t.Fatal("source snapshot export failed")
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

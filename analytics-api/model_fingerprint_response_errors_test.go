package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the real Go -> Node -> authenticated Go bridge, without production
// credentials or a billable upstream. The reference effort must not be removed.
func TestFingerprintResponsesEffortRejectionThroughTargetedBridge(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/responses" || r.URL.RawQuery != "" ||
			r.Header.Get("Authorization") != "Bearer gateway-fixture" || r.Header.Get("X-Uni-API-Provider") != "exact-provider" ||
			r.Header.Get("X-Uni-API-Probe-Unconfigured-Model") != "true" || !strings.HasPrefix(r.Header.Get("X-Request-ID"), "fingerprint-") {
			t.Error("lost fixed endpoint, credential or provider targeting")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "gemini-4-argon" || mustJSON(body["reasoning"]) != `{"effort":"low"}` || body["stream"] != true {
			t.Error("changed fingerprint sampling conditions")
		}
		upstreamError := mustJSON(map[string]any{"error": map[string]string{
			"message": "reasoning is not supported for Claude/Gemini Responses compatibility",
			"type":    "invalid_request_error", "code": "invalid_request",
		}, "debug": "private-upstream-details"})
		// Match the gateway json_error wrapper around the upstream body, rather
		// than pretending the configured channel returns it directly to Node.
		writeJSON(w, 400, map[string]any{"error": map[string]string{"message": upstreamError}})
	}))
	defer up.Close()
	client := &http.Client{Transport: configuredProbeTransport{base: up.Client().Transport, provider: "exact-provider", allowUnconfigured: true}}
	config := map[string]any{"model": "gemini-4-argon", "format": "responses", "base_url": up.URL, "key": "gateway-fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	checkpoints := 0
	raw, err := runFingerprintViaTransportProgress(ctx, client, config, nil, func(result, samples json.RawMessage) error {
		checkpoints++
		if len(samples) != 0 || !strings.Contains(string(result), `"error_code":"reference_effort_unsupported"`) {
			t.Error("missing safe checkpoint diagnostic or fabricated resume evidence")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status      string   `json:"status"`
		UsedOutputs int      `json:"used_outputs"`
		Probability *float64 `json:"probability"`
		Samples     []struct {
			HTTPStatus int    `json:"http_status"`
			ErrorCode  string `json:"error_code"`
		} `json:"samples"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Status != "error" || result.UsedOutputs != 0 || result.Probability != nil ||
		len(result.Samples) != 1 || result.Samples[0].HTTPStatus != 400 || result.Samples[0].ErrorCode != "reference_effort_unsupported" || calls.Load() != 1 || checkpoints != 1 {
		t.Fatalf("unexpected result/calls/checkpoints: %s %d %d", raw, calls.Load(), checkpoints)
	}
	for _, secret := range []string{"gateway-fixture", "private-upstream-details", "reasoning is not supported"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("untrusted error or credential escaped")
		}
	}
}

func TestFingerprintTargetedBridgeStopsOnGroupAndGatewayRejection(t *testing.T) {
	for _, tc := range []struct {
		status        int
		message, code string
	}{
		{403, `{"code":"GROUP_DELETED","message":"private details"}`, "group_deleted"},
		{503, "All matching channels are temporarily disabled", "route_temporarily_disabled"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/v1/chat/completions" || r.Header.Get("X-Uni-API-Provider") != "exact-provider" {
					t.Error("lost targeted Chat protocol")
				}
				writeJSON(w, tc.status, map[string]any{"error": map[string]string{"message": tc.message}})
			}))
			defer up.Close()
			client := &http.Client{Transport: configuredProbeTransport{base: up.Client().Transport, provider: "exact-provider", fingerprintProtocol: "chat"}}
			config := map[string]any{"model": "deepseek-v4.1-flash-1017", "format": "openai", "base_url": up.URL, "key": "gateway-fixture", "targeted": true}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			result, err := runFingerprintViaTransport(ctx, client, config)
			if err != nil || calls.Load() != 1 || !strings.Contains(string(result), `"error_code":"`+tc.code+`"`) || strings.Contains(string(result), "private details") {
				t.Fatalf("calls=%d, result=%s, error=%v", calls.Load(), result, err)
			}
		})
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestControlDecodeDistinguishesBodySizeFromMalformedJSON(t *testing.T) {
	for _, tc := range []struct {
		body, content string
		status        int
		message       string
	}{
		{`{"value":"` + strings.Repeat("x", 65) + `"}`, "application/json", 413, "大小限制"},
		{`{"value":`, "application/json", 400, "invalid input"},
		{`{}`, "text/plain", 415, "JSON required"},
		{`{"value":"valid"}`, "application/json", 200, ""},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		var v map[string]any
		ok := decodeControlLimit(w, r, &v, 64)
		if ok != (tc.status == 200) || w.Code != tc.status || !strings.Contains(w.Body.String(), tc.message) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestConfigurationDecoderExceedsFormerBatchAndByteLimits(t *testing.T) {
	t.Setenv("CONFIGURATION_MAX_BYTES", "0")
	models := []string{}
	for i := 0; i < 101; i++ {
		models = append(models, fmt.Sprintf("%03d", i)+strings.Repeat("m", 253))
	}
	targets := []map[string]any{}
	for i := 0; i < 700; i++ {
		targets = append(targets, map[string]any{"source_id": "primary", "provider": fmt.Sprintf("%03d", i) + strings.Repeat("p", 253), "models": models})
	}
	raw, _ := json.Marshal(map[string]any{"kind": "check", "targets": targets})
	if len(raw) <= 16<<20 {
		t.Fatal("fixture must exceed old byte quota", len(raw))
	}
	r := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	var decoded struct {
		Targets []struct {
			Models []string `json:"models"`
		} `json:"targets"`
	}
	w := httptest.NewRecorder()
	if !decodeConfiguration(w, r, &decoded) || len(decoded.Targets) != 700 || len(decoded.Targets[699].Models) != 101 {
		t.Fatal("maximum batch lost models", w.Code)
	}
}

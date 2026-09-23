package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// Reproduce the 28-channel UI selection with the common catalog plus 36 extras.
// Do not start workers: queue verification must never invoke paid upstreams.
func TestConfiguredChecksBatchAllSelectedModels(t *testing.T) {
	s, a, src := bindingFixture(t, "https://unrelated.example")
	token, err := s.control.newSession(context.Background(), a.Owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_configured_checks WHERE source_id=$1`, src.ID) })
	models := append([]string{}, subModels...)
	for i := 0; i < 36; i++ {
		models = append(models, fmt.Sprintf("extra-channel-model-%02d", i))
	}
	targets := []map[string]any{}
	for i := 0; i < 28; i++ {
		targets = append(targets, map[string]any{"source_id": src.ID, "provider": fmt.Sprintf("configured-channel-%02d", i), "models": models})
	}
	raw, _ := json.Marshal(map[string]any{"kind": "check", "targets": targets})
	if len(raw) <= 32<<10 {
		t.Fatal("fixture does not cross legacy body limit", len(raw))
	}
	request := func(body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/channel-management/checks", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := request(raw)
	if w.Code != 202 {
		t.Fatalf("%d-byte batch: HTTP %d %s", len(raw), w.Code, w.Body.String())
	}
	var queued int
	if err = s.control.db.QueryRow(`SELECT count(*) FROM console_configured_checks WHERE source_id=$1 AND state='queued'`, src.ID).Scan(&queued); err != nil || queued != 28*len(models) {
		t.Fatal("batch incomplete", queued, err)
	}
	// Clicking again during the same run cannot create duplicate probes.
	w = request(raw)
	if w.Code != 202 || !bytes.Contains(w.Body.Bytes(), []byte(`"queued":0`)) {
		t.Fatal(w.Code, w.Body.String())
	}
	// Target/model-count validation still applies even with a larger body limit.
	tooMany := append(append([]string{}, models...), make([]string, 101-len(models))...)
	invalid, _ := json.Marshal(map[string]any{"kind": "check", "targets": []any{map[string]any{"source_id": src.ID, "provider": "invalid", "models": tooMany}}})
	if w = request(invalid); w.Code != 400 || bytes.Contains(w.Body.Bytes(), []byte("invalid input")) {
		t.Fatal(w.Code, w.Body.String())
	}
}

// Optional incident replay: the file contains only catalog IDs/model names.
// Invalid kind ensures that even a fixed decoder cannot enqueue live requests.
func TestConfiguredChecksIncidentDecode(t *testing.T) {
	path := os.Getenv("TEST_CONFIGURED_CHECK_BATCH")
	if path == "" {
		t.Skip("incident request fixture not supplied")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var in struct {
		Kind    string `json:"kind"`
		Targets []struct {
			Source   string   `json:"source_id"`
			Provider string   `json:"provider"`
			Models   []string `json:"models"`
		} `json:"targets"`
	}
	r := httptest.NewRequest("POST", "/v1/channel-management/checks", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if !decodeControlLimit(w, r, &in, 32<<10) {
		if w.Code != 400 && w.Code != 413 {
			t.Fatal(w.Code)
		}
		t.Logf("reproduced old rejection: %d bytes, HTTP %d: %s", len(raw), w.Code, w.Body.String())
	} else {
		t.Fatal("incident did not exceed legacy decoder limit")
	}
	if err = json.Unmarshal(raw, &in); err != nil {
		t.Fatal("invalid incident JSON", err)
	}
	t.Logf("valid JSON: %d targets, %d models in first target", len(in.Targets), len(in.Targets[0].Models))
	// Reach semantic validation without creating any tasks, locally or upstream.
	in.Kind = "validation-only"
	raw, _ = json.Marshal(in)
	r = httptest.NewRequest("POST", "/v1/channel-management/checks", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	(&Service{}).queueConfiguredChecks(w, r)
	if w.Code != 400 || !bytes.Contains(w.Body.Bytes(), []byte("无效检测任务")) {
		t.Fatal("large valid JSON was not decoded", w.Code, w.Body.String())
	}
}

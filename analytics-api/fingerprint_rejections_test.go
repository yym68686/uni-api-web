package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFingerprintLegacyRejectionsAreExactReadOnlyCorrelations(t *testing.T) {
	s, account, source := bindingFixture(t, "https://site.example")
	s.engine = postgresTestEngine(t)
	now := time.Now().Truncate(time.Second)
	model := "deepseek-v4.1-flash-1017"
	cases := []struct {
		name     string
		status   int
		kind     string
		stage    string
		detail   string
		mismatch string
		existing string
		want     string
	}{
		{name: "deleted", status: 403, kind: "billing", detail: `{"error":{"error_code":"GROUP_DELETED","error_message":"private provider detail"}}`, want: "group_deleted"},
		{name: "wrong-source", status: 403, kind: "billing", detail: `{"error":{"error_code":"GROUP_DELETED"}}`, mismatch: "source"},
		{name: "wrong-provider", status: 403, kind: "billing", detail: `{"error":{"error_code":"GROUP_DELETED"}}`, mismatch: "provider"},
		{name: "wrong-model", status: 403, kind: "billing", detail: `{"error":{"error_code":"GROUP_DELETED"}}`, mismatch: "model"},
		{name: "wrong-time", status: 403, kind: "billing", detail: `{"error":{"error_code":"GROUP_DELETED"}}`, mismatch: "time"},
		{name: "wrong-http", status: 403, kind: "billing", detail: `{"error":{"error_code":"GROUP_DELETED"}}`, mismatch: "status"},
		{name: "arbitrary-text", status: 403, kind: "billing", detail: `{"error":{"error_message":"GROUP_DELETED"}}`},
		{name: "not-billing", status: 403, kind: "trace", detail: `{"error":{"error_code":"GROUP_DELETED"}}`},
		{name: "balance", status: 403, kind: "billing", detail: `{"error":{"error_code":"INSUFFICIENT_BALANCE"}}`, want: "insufficient_balance"},
		{name: "model", status: 404, kind: "billing", detail: `{"error":{"error_type":"model_not_found"}}`, want: "model_not_found"},
		{name: "temporarily-disabled", status: 503, kind: "trace", stage: "gateway_error", detail: `{"error_message":"All matching channels are temporarily disabled"}`, want: "route_temporarily_disabled"},
		{name: "existing-safe-code", status: 403, kind: "billing", detail: `{"error":{"error_code":"GROUP_DELETED"}}`, existing: "group_disabled", want: "group_disabled"},
	}
	facts := []Fact{}
	for i, tc := range cases {
		request := fmt.Sprintf("fingerprint-00000000-0000-4000-a000-%012x", i)
		target := fingerprintTarget{Source: source.ID, Provider: tc.name, Model: model}
		if err := enqueueFingerprint(context.Background(), s.control.db, target); err != nil {
			t.Fatal(err)
		}
		result := mustJSON(map[string]any{"status": "error", "checked_at": now.Unix(), "samples": []any{
			map[string]any{"state": "error", "request_id": request, "http_status": tc.status, "error_code": tc.existing},
		}})
		if _, err := s.control.db.Exec(`UPDATE console_model_fingerprints SET state='done',result=$1,revision='same' WHERE scope=$2 AND model=$3`, result, target.scope(), model); err != nil {
			t.Fatal(err)
		}
		f := Fact{Schema: 1, EventID: tc.name, SourceID: source.ID, RequestID: request, Provider: tc.name, Kind: tc.kind, Stage: tc.stage, Model: model, AtMS: now.Add(-time.Second).UnixMilli(), Status: tc.status, TraceDetail: json.RawMessage(tc.detail)}
		switch tc.mismatch {
		case "source":
			f.SourceID = "other"
		case "provider":
			f.Provider = "other"
		case "model":
			f.Model = "other"
		case "time":
			f.AtMS = now.Add(-16 * time.Minute).UnixMilli()
		case "status":
			f.Status = 404
		}
		if tc.stage == "gateway_error" {
			f.Provider, f.Model = "", ""
		}
		facts = append(facts, f)
	}
	if err := s.engine.Import(context.Background(), "legacy-fingerprint-test", "hash", facts); err != nil {
		t.Fatal(err)
	}
	got := fingerprintRequest(t, s, account.Owner, "GET", nil)
	if got.Code != 200 || strings.Contains(got.Body.String(), "private provider detail") {
		t.Fatal(got.Code, got.Body.String())
	}
	var response struct{ Data []fingerprintRecord }
	if err := json.Unmarshal(got.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{}
	for _, record := range response.Data {
		if record.Source != source.ID {
			continue
		}
		var result struct {
			Samples []struct {
				Code string `json:"error_code"`
			}
		}
		if err := json.Unmarshal(record.Result, &result); err != nil || len(result.Samples) != 1 {
			t.Fatal(record.Result, err)
		}
		codes[record.Provider] = result.Samples[0].Code
	}
	for _, tc := range cases {
		if code, ok := codes[tc.name]; !ok || code != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, code, tc.want)
		}
	}
	var stored string
	if err := s.control.db.QueryRow(`SELECT result->'samples'->0->>'error_code' FROM console_model_fingerprints WHERE source_id=$1 AND provider='deleted'`, source.ID).Scan(&stored); err != nil || stored != "" {
		t.Fatal("legacy annotation must not alter the stored result", stored, err)
	}
}

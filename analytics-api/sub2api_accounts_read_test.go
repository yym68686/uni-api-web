package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSubAccountModelSummaryCompaction(t *testing.T) {
	price, tokens := 2.5, int64(120)
	probe := subProbe{ID: "receipt-id", CurlToken: "secret-curl", StartedAt: 123, RequestIDs: []string{"request-1"}, Text: strings.Repeat("reply", 1000), Status: "success", TerminalStatus: "completed", RequestedModel: "m", ResponseModel: "m", ModelMatch: "match", Protocol: "gemini", TTFT: &tokens, ResponseCreatedMS: &tokens, FirstResponseMS: &tokens, Duration: 321, HTTPStatus: 200, Message: strings.Repeat("消息", 200), Usage: &subUsage{Status: "pending", InputPrice: &price, OutputPrice: &price, InputTokens: &tokens, OutputTokens: &tokens, ActualCost: &price, RequestID: "private-request"}}
	for _, result := range []string{mustJSON(subResult{Model: "m", CheckedAt: 234, Verdict: "pass", Availability: probe, Quality: probe}), `null`, `{}`, `{"availability":null,"quality":null}`, `{"availability":{"usage":{}},"quality":{"usage":null}}`} {
		var full, projected *subResult
		if err := json.Unmarshal([]byte(result), &full); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(result), &projected); err != nil {
			t.Fatal(err)
		}
		compactSubModelResult(projected)
		if !reflect.DeepEqual(compactSummary(full), compactSummary(projected)) {
			t.Fatalf("list evidence changed: full=%s projected=%s", mustJSON(compactSummary(full)), mustJSON(compactSummary(projected)))
		}
		if full != nil && full.Availability.ID != "" && (projected.Availability.ID != full.Availability.ID || projected.Availability.Text != "" || projected.Availability.CurlToken != "") {
			t.Fatal("receipt ID lost or detail retained")
		}
	}
}

func TestSubAccountSummaryBulkModelsAndCurrentReceipt(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := "bulk-read-" + randomID()
	other := owner + "-foreign"
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE owner=ANY($1)`, []string{owner, other})
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := store.db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	for i, a := range []string{owner + "-a", owner + "-b", other} {
		o := owner
		if i == 2 {
			o = other
		}
		exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,$1,'https://fixture.test',$1,'')`, a, o)
		exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES($1,7,$1,'openai')`, a)
		for _, m := range []string{"z", "a", "gpt-6-astra"} {
			result := subResult{Model: m, Availability: subProbe{ID: a + "-" + m, Text: "detail-" + a, Status: "success", Usage: &subUsage{Status: "pending"}}}
			exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,7,$2,'done',$3)`, a, m, mustJSON(result))
		}
		exec(`INSERT INTO console_sub_models(account_id,group_id,model,state) VALUES($1,7,'pending','running')`, a)
	}
	price, tokens := 9.0, int64(42)
	exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES($1,$2,7,0,'a',0,'[]','matched',$3)`, owner+"-a-a", owner+"-a", mustJSON(subUsage{Status: "matched", InputPrice: &price, InputTokens: &tokens}))
	s := &Service{control: store}
	token, _ := store.newSession(context.Background(), owner)
	get := func(path, view string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Console-View", view)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w
	}
	full := get("/v1/sub2api/accounts", "")
	var detailed struct {
		Data []subAccount `json:"data"`
	}
	if err := json.Unmarshal(full.Body.Bytes(), &detailed); err != nil {
		t.Fatal(err)
	}
	summary := get("/v1/sub2api/accounts", "summary")
	var compact struct {
		Data []struct {
			Value struct {
				AccountID string     `json:"account_id"`
				Target    *subTarget `json:"target"`
			}
		} `json:"data"`
	}
	if err := json.Unmarshal(summary.Body.Bytes(), &compact); err != nil {
		t.Fatal(err)
	}
	gotTargets := 0
	for _, row := range compact.Data {
		if row.Value.Target == nil {
			continue
		}
		gotTargets++
		target := row.Value.Target
		if len(target.Models) != 4 || target.Models[0].Model != "a" || target.Models[3].Model != "z" || target.Models[2].Result != nil {
			t.Fatal("missing/unordered model or pending result fabricated")
		}
		for _, account := range detailed.Data {
			if account.ID == row.Value.AccountID && !reflect.DeepEqual(compactSummary(account.Targets[0]), compactSummary(target)) {
				t.Fatal("summary differs from detail")
			}
		}
		if row.Value.AccountID == owner+"-a" && (target.Models[0].Result.Availability.Usage.Status != "matched" || *target.Models[0].Result.Availability.Usage.InputPrice != price) {
			t.Fatal("summary lost current receipt")
		}
	}
	if gotTargets != 2 || strings.Contains(summary.Body.String(), other) || strings.Contains(summary.Body.String(), "detail-") {
		t.Fatal("scope or detail leakage")
	}
	detail := get(fmt.Sprintf("/v1/sub2api/accounts/%s/groups/7/details?model=a", owner+"-b"), "")
	if !strings.Contains(detail.Body.String(), "detail-"+owner+"-b") || strings.Contains(detail.Body.String(), `"model":"z"`) || !strings.Contains(detail.Body.String(), `"model":"gpt-6-astra"`) {
		t.Fatal("model detail scope changed")
	}
}

func TestSubAccountTypedSummaryMatchesLegacy(t *testing.T) {
	price, tokens := 2.5, int64(120)
	probe := subProbe{ID: "private-probe", CurlToken: "private-curl", Text: "private-reply", StartedAt: 123,
		RequestIDs: []string{"private-request"}, RequestedModel: "m", ResponseModel: "m", ModelMatch: "match",
		Status: "success", TerminalStatus: "missing", Message: strings.Repeat("消息🙂", 161), TTFT: &tokens,
		ResponseCreatedMS: &tokens, FirstResponseMS: &tokens, Protocol: "gemini", Duration: 123, HTTPStatus: 200,
		Usage: &subUsage{Status: "matched", InputPrice: &price, OutputPrice: &price, InputTokens: &tokens, OutputTokens: &tokens, RequestID: "private"}}
	for _, models := range [][]subModelResult{nil, {}, {{Model: "pending", State: "queued"}}, {
		{Model: "m", State: "done", Message: strings.Repeat("长消息", 180), Result: &subResult{Model: "m", CheckedAt: 321, Verdict: "pass", Availability: probe, Quality: probe}},
		{Model: "zero", Result: &subResult{}},
		{Model: "empty-usage", Result: &subResult{Availability: subProbe{Usage: &subUsage{}}}},
	}} {
		target := subTarget{Models: models, Result: &subResult{Availability: probe}, Message: strings.Repeat("目标🙂", 180)}
		before := mustJSON(target)
		var typed any
		if err := json.Unmarshal([]byte(mustJSON(compactAccountTarget(target))), &typed); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(compactSummary(target), typed) {
			t.Fatalf("summary semantics changed: legacy=%s typed=%s", mustJSON(compactSummary(target)), mustJSON(typed))
		}
		if mustJSON(target) != before {
			t.Fatal("summary mutated shared input")
		}
	}
}

func TestSubAccountSQLProjectionPreservesEvidence(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, raw := range []string{`null`, `{}`, `{"availability":null,"quality":null}`,
		`{"availability":{"id":"receipt","text":"reply","curl_token":"secret","request_ids":["r"],"usage":{"status":"matched","message":"private","request_id":"r","input_tokens":42},"message":"keep","http_status":200},"quality":{"text":"reply","status":"skipped"}}`} {
		var projected []byte
		if err := store.db.QueryRow(`SELECT `+subModelSummarySQL+` FROM (SELECT $1::jsonb AS result) m`, raw).Scan(&projected); err != nil {
			t.Fatal(err)
		}
		var full, slim *subResult
		if err := json.Unmarshal([]byte(raw), &full); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(projected, &slim); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(compactSummary(full), compactSummary(slim)) {
			t.Fatal("projection lost list evidence")
		}
		if full != nil && full.Availability.ID != slim.Availability.ID {
			t.Fatal("projection lost receipt identity")
		}
		if strings.Contains(string(projected), "secret") || strings.Contains(string(projected), "private") {
			t.Fatal("projection retained verbose details")
		}
	}
}

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSummaryPreservesListEvidenceWithoutDetailedReplies(t *testing.T) {
	price := 2.5
	tokens := int64(100)
	probe := subProbe{ID: "unique-probe-identifier", Status: "success", Text: strings.Repeat("private-reply", 5000), ModelMatch: "match", TTFT: &tokens, Usage: &subUsage{Status: "matched", InputPrice: &price, OutputPrice: &price, InputTokens: &tokens, OutputTokens: &tokens, ActualCost: &price}}
	input := subTarget{GroupID: 1, Models: []subModelResult{{Model: "m", State: "done", Result: &subResult{Model: "m", CheckedAt: 10, Availability: probe, Quality: probe, Verdict: "pass"}}}, ToolUse: &subCapabilityResult{Status: "supported", Models: []subToolUseModel{{Model: "m", State: "done", Result: &subCapabilityResult{Status: "supported", Attempts: []subProbe{probe}}}}}}
	before := mustJSON(input)
	compact := mustJSON(compactSummary(input))
	if strings.Contains(compact, "private-reply") || strings.Contains(compact, "unique-probe-identifier") || len(compact) >= len(before)/10 {
		t.Fatal("verbose body remained in list")
	}
	var output subTarget
	if json.Unmarshal([]byte(compact), &output) != nil {
		t.Fatal("summary cannot be read")
	}
	result := output.Models[0].Result
	if result.Availability.Status != "success" || result.Availability.ModelMatch != "match" || *result.Availability.Usage.InputPrice != price || *result.Availability.Usage.InputTokens != tokens || output.ToolUse.Models[0].Result.Status != "supported" {
		t.Fatal("list evidence lost")
	}
	if mustJSON(input) != before {
		t.Fatal("summary mutated shared detail")
	}
}

func TestAccountSummaryDeltaDetailsAndAuthorization(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := "low-bandwidth-" + randomID()
	account := owner + "-account"
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE owner=$1`, owner)
	_, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'fixture','https://fixture.test','fixture@example.com','')`, account, owner)
	if err != nil {
		t.Fatal(err)
	}
	for group := 1; group <= 2; group++ {
		_, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES($1,$2,'group','openai')`, account, group)
		if err != nil {
			t.Fatal(err)
		}
		for _, model := range []string{"m", "other"} {
			result := subResult{Model: model, CheckedAt: 1, Availability: subProbe{Status: "success", Text: strings.Repeat("original-reply", 3000)}, Quality: subProbe{Status: "skipped"}}
			_, err = store.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,$2,$3,'done',$4)`, account, group, model, mustJSON(result))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	s := &Service{control: store}
	token, _ := store.newSession(context.Background(), owner)
	foreign, _ := store.newSession(context.Background(), owner+"-other")
	get := func(path, view, since, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Console-View", view)
		r.Header.Set("X-Console-Since", since)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	path := "/v1/sub2api/accounts"
	full := get(path, "", "", token)
	if full.Code != 200 {
		t.Fatal(full.Code, full.Body.String())
	}
	summary := get(path, "summary", "", token)
	if summary.Code != 200 {
		t.Fatal(summary.Code, summary.Body.String())
	}
	if strings.Contains(summary.Body.String(), "original-reply") {
		t.Fatal("summary leaked verbose reply")
	}
	var payload struct {
		Version string       `json:"version"`
		Base    string       `json:"base_version"`
		Data    []summaryRow `json:"data"`
		Removed []string     `json:"removed"`
	}
	json.Unmarshal(summary.Body.Bytes(), &payload)
	version := payload.Version
	if len(payload.Data) != 3 || version == "" {
		t.Fatal("summary missing accounts or groups")
	}
	if w := get(path, "summary", version, token); w.Code != 304 || w.Body.Len() != 0 {
		t.Fatal("unchanged downloaded again", w.Code)
	}
	_, err = store.db.Exec(`UPDATE console_sub_targets SET message='changed' WHERE account_id=$1 AND group_id=1`, account)
	if err != nil {
		t.Fatal(err)
	}
	w := get(path, "summary", version, token)
	json.Unmarshal(w.Body.Bytes(), &payload)
	if w.Code != 200 || len(payload.Data) != 1 || payload.Base != version {
		t.Fatal("did not send only changed group", w.Code, w.Body.String())
	}
	version = payload.Version
	_, err = store.db.Exec(`DELETE FROM console_sub_targets WHERE account_id=$1 AND group_id=2`, account)
	if err != nil {
		t.Fatal(err)
	}
	w = get(path, "summary", version, token)
	json.Unmarshal(w.Body.Bytes(), &payload)
	if len(payload.Removed) != 1 || payload.Removed[0] != qualityGroupID(account, 2) {
		t.Fatal("deletion missing")
	}
	w = get(path, "summary", version, foreign)
	if w.Code != 200 || strings.Contains(w.Body.String(), account) {
		t.Fatal("cross-user summary leak")
	}
	detailPath := fmt.Sprintf("/v1/sub2api/accounts/%s/groups/1/details?model=m", account)
	w = get(detailPath, "", "", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "original-reply") || strings.Contains(w.Body.String(), `"model":"other"`) {
		t.Fatal("scoped detail unavailable", w.Code)
	}
	if w = get(detailPath, "", "", foreign); w.Code != 404 {
		t.Fatal("foreign detail accepted", w.Code)
	}
	headers := get(path, "accounts", "", token)
	if strings.Contains(headers.Body.String(), "original-reply") || strings.Contains(headers.Body.String(), `"target":`) {
		t.Fatal("account header downloaded models")
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	gz.Write(summary.Body.Bytes())
	gz.Close()
	t.Logf("full=%d summary=%d gzip-summary=%d bytes", full.Body.Len(), summary.Body.Len(), compressed.Len())
}

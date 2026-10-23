package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestManagementHTTPCompleteSelectionAuthorizationAndFreshness(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	s := managementBindingTestService(t)
	s.engine = postgresTestEngine(t)
	ctx := context.Background()
	for _, query := range []string{
		`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('owned','owner','Own','https://site.test','owner@example.test',''),('foreign','foreign','PRIVATE-ACCOUNT','https://foreign.test','private@example.test','')`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,active,state) SELECT 'owned',g,'Group','openai',g,true,'done' FROM generate_series(1,61) g`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,active,state) VALUES('foreign',1,'PRIVATE-TARGET','openai',1,true,'done')`,
	} {
		if _, err := s.control.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	ownerToken, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	foreignToken, err := s.control.newSession(ctx, "foreign")
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	call := func(token string, params url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query?"+params.Encode(), nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
			t.Fatal("current query acquired cache validators")
		}
		return w
	}
	if got := call("", nil); got.Code != 401 {
		t.Fatal("anonymous read allowed", got.Code)
	}
	response := call(ownerToken, nil)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, stage := range []string{"database_predicate_snapshot", "assemble_gateway", "assemble_rows", "filter_evidence", "filter_page", "hydrate_page"} {
		if !strings.Contains(logs.String(), "stage="+stage+" ") {
			t.Fatal("missing current-read stage", stage)
		}
	}
	if strings.Contains(logs.String(), "PRIVATE") || strings.Contains(logs.String(), "private@example") {
		t.Fatal("private data escaped timing logs")
	}
	if strings.Contains(response.Body.String(), "PRIVATE") || strings.Contains(response.Body.String(), "private@example") {
		t.Fatal("foreign data leaked")
	}
	var page managementPageResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 25 || page.Total != 61 || page.EligibleCount != 61 || len(page.Accounts) != 1 || len(page.Accounts[0].Targets) != 0 || page.SelectionToken == "" {
		t.Fatal("incomplete page metadata", page.Total, page.EligibleCount, len(page.Rows))
	}
	if len(page.Context.Fingerprints) != 25 || page.Context.Imports == nil || page.Context.Routes == nil || page.Context.Prices == nil || page.Accounts[0].ActiveGroups == nil || *page.Accounts[0].ActiveGroups != 61 {
		t.Fatal("page omitted current display metadata or full account counts")
	}
	if !page.Rows[0].Target.DetailsOmitted {
		t.Fatal("compact target presented as a full diagnostic")
	}
	second := call(ownerToken, url.Values{"cursor": {page.NextCursor}})
	var next managementPageResponse
	if err := json.Unmarshal(second.Body.Bytes(), &next); err != nil || second.Code != 200 || next.Offset != 25 || len(next.Rows) != 25 || next.Revision != page.Revision {
		t.Fatal("valid cursor failed", second.Code, second.Body.String(), err)
	}
	complete := call(ownerToken, url.Values{"selection": {page.SelectionToken}})
	var selected managementSelectionResponse
	if err := json.Unmarshal(complete.Body.Bytes(), &selected); err != nil || complete.Code != 200 || len(selected.Rows) != 61 || len(selected.Eligible) != 61 || selected.Total != 61 || selected.Revision != page.Revision {
		t.Fatal("batch selection truncated to page", complete.Code, len(selected.Rows), len(selected.Eligible), err)
	}
	for _, params := range []url.Values{
		{"cursor": {page.SelectionToken}, "selection": {page.SelectionToken}},
		{"selection": {page.NextCursor}},
		{"selection": {page.SelectionToken}, "filters": {`{"search":"Own"}`}},
		{"cursor": {"forged"}},
	} {
		if got := call(ownerToken, params); got.Code != 400 {
			t.Fatal("invalid selection accepted", got.Code)
		}
	}
	if got := call(foreignToken, url.Values{"selection": {page.SelectionToken}}); got.Code != 400 {
		t.Fatal("another owner reused selection", got.Code)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET state='error' WHERE account_id='owned' AND group_id=61`); err != nil {
		t.Fatal(err)
	}
	for _, params := range []url.Values{{"selection": {page.SelectionToken}}, {"cursor": {page.NextCursor}}} {
		if got := call(ownerToken, params); got.Code != 409 || strings.Contains(got.Body.String(), `"rows"`) {
			t.Fatal("changed data silently accepted", got.Code)
		}
	}
	fresh := call(ownerToken, nil)
	var latest managementPageResponse
	if err := json.Unmarshal(fresh.Body.Bytes(), &latest); err != nil || latest.EligibleCount != 60 || latest.Revision == page.Revision {
		t.Fatal("fresh page did not observe committed change", fresh.Code, err)
	}
	// No channel edit, test request or persisted selection is created by reading.
	var queued int
	if err := s.control.db.QueryRow(`SELECT count(*) FROM console_sub_check_queue`).Scan(&queued); err != nil || queued != 0 {
		t.Fatal("selection executed an action", queued, err)
	}
}

func TestManagementQueryRejectsInvalidFilters(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"model":5}`, `{"unknown":"value"}`, `{} {}`, `{"sort":"random"}`, `{"groupStatus":"obsolete"}`, `{"maxRate":"NaN"}`, `{"maxRate":"Inf"}`, `{"maxRate":"-1"}`, `{"minQuality":"50.5"}`, `{"minQuality":"110"}`, `{"availability":"invented"}`, `{"model":"\u0000"}`} {
		if _, err := parseManagementQuery(url.Values{"filters": {raw}}); err == nil {
			t.Fatal("invalid filter accepted", raw)
		}
	}
	for _, params := range []url.Values{{"unknown": {"x"}}, {"filters": {"{}", "{}"}}, {"filters": {strings.Repeat("x", 8193)}}} {
		if _, err := parseManagementQuery(params); err == nil {
			t.Fatal("invalid query accepted")
		}
	}
	filters, err := parseManagementQuery(url.Values{"filters": {`{"model":"公开模型/别名","maxRate":"0","minQuality":"100"}`}})
	if err != nil || filters.GroupStatus != "available" || filters.Model != "公开模型/别名" || filters.MaxRate != "0" {
		t.Fatal("valid filter rejected", filters, err)
	}
}

func TestManagementHTTPRejectsMissingControlService(t *testing.T) {
	s := &Service{}
	r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("management query bypassed unavailable account service", w.Code)
	}
}

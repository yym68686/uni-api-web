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

func TestManagementHTTPTierSelectionKeepsSignedFilteredScopeWithoutRawReceipts(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	for _, q := range []string{
		`UPDATE console_sub_targets SET name='Ignore off-page' WHERE group_id=60`,
		`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,status}','"error"') WHERE group_id=61`,
		`UPDATE console_sub_models SET result=jsonb_set(result,'{service_tiers}',jsonb_build_object('ultrafast',jsonb_build_object('status','supported','checked_at',extract(epoch from now())::bigint))) WHERE model='gpt-5.5' AND group_id=59`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,active,state)
		 SELECT 'a',g,'Group '||g,'openai',g,true,'done' FROM generate_series(62,980) g`,
		`INSERT INTO console_sub_models(account_id,group_id,model,state,message,result)
		 SELECT 'a',g,m,'done','',jsonb_build_object('model',m,'checked_at',g,'availability',
		   jsonb_build_object('id','historic-'||g||'-'||m,'status','success','text','TIER_MUST_NOT_READ_DIAGNOSTICS'))
		 FROM generate_series(62,980) g CROSS JOIN unnest(ARRAY['gpt-5.5','gpt-6-astra','extra-model']) m`,
		`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result)
		 SELECT 'historic-'||g||'-'||m,'a',g,g,m,0,'[]','matched','{"input_price":2,"output_price":3}'
		 FROM generate_series(62,980) g CROSS JOIN unnest(ARRAY['gpt-5.5','gpt-6-astra','extra-model']) m`,
		`UPDATE console_sub_targets SET result=jsonb_build_object('quality',jsonb_build_object('id','q-'||group_id||'-gpt-5.5')) WHERE group_id<=61`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	prepareManagementRevisionsForTest(t, db)
	owner, err := s.control.newSession(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.control.newSession(context.Background(), "foreign")
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	call := func(session string, values url.Values, list bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query?"+values.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		if list {
			r.Header.Set("Accept", managementPageV3MediaType)
			r.Header.Set(managementListViewHeader, managementListViewV1)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	for _, c := range []struct {
		filters string
		count   int
		catalog bool
	}{
		{`{"excludeName":"ignore"}`, 979, true},
		{`{"availability":"success","excludeName":"ignore"}`, 978, true},
		{`{"availability":"success","excludeName":"ignore","model":"gpt-5.5","ultrafast":"unknown"}`, 977, false},
	} {
		filters, err := parseManagementQuery(url.Values{"filters": {c.filters}})
		if err != nil {
			t.Fatal(err)
		}
		// Use a real projected list snapshot to sign the token, including the
		// group-availability path that intentionally differs from a raw read.
		ctx := context.WithValue(context.Background(), managementListViewKey{}, true)
		snapshot, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
		if err != nil || snapshot.Page.Total != c.count || len(snapshot.Page.Rows) != 25 {
			t.Fatalf("filter %s: list total=%d err=%v", c.filters, snapshot.Page.Total, err)
		}
		first := call(owner, url.Values{"filters": {c.filters}}, true)
		var page managementColumnPage
		if err := json.Unmarshal(first.Body.Bytes(), &page); first.Code != 200 || err != nil || page.Revision != snapshot.Page.Revision || page.SelectionToken == "" {
			t.Fatalf("list response did not issue the expected token: status=%d err=%v", first.Code, err)
		}
		params := url.Values{"filters": {c.filters}, "selection": {page.SelectionToken}, "check_selection": {"tier"}}
		logs.Reset()
		response := call(owner, params, false)
		var selection managementTierSelectionResponse
		if err := json.Unmarshal(response.Body.Bytes(), &selection); response.Code != 200 || err != nil || selection.Format != "management-tier-selection-v1" || selection.Revision != snapshot.Page.Revision || selection.Total != c.count || selection.EligibleCount != c.count || len(selection.Eligible) != c.count {
			t.Fatalf("filter %s: selection status=%d total=%d eligible=%d err=%v\n%s", c.filters, response.Code, selection.Total, len(selection.Eligible), err, logs.String())
		}
		for i, row := range selection.Eligible {
			if row.ID != snapshot.Filtered.Rows[i].ID || len(row.Checks) != 0 || row.Selected != nil {
				t.Fatal("tier action changed filter membership/order or serialized diagnostics", row.ID)
			}
		}
		if strings.Contains(response.Body.String(), `"input":`) || strings.Contains(response.Body.String(), "TIER_MUST_NOT_READ_DIAGNOSTICS") || selection.Imports == nil || selection.Checks == nil {
			t.Fatal("invalid compact tier response")
		}
		readLog := logs.String()
		if !strings.Contains(readLog, "management_signal_receipts deferred=true") || !strings.Contains(readLog, "management_account_receipt_signals requested=61 resolved=61") ||
			strings.Contains(readLog, "stage=account_receipts ") || strings.Contains(readLog, "stage=hydrate_page ") ||
			strings.Contains(readLog, "catalog_only=true") != c.catalog {
			t.Fatalf("tier selection fell back to expensive reads:\n%s", readLog)
		}
		if response := call(foreign, params, false); response.Code != 400 {
			t.Fatal("cross-owner tier selection accepted", response.Code)
		}
		params.Set("filters", `{"search":"unrelated"}`)
		if response := call(owner, params, false); response.Code != 400 {
			t.Fatal("selection token accepted with different filters", response.Code)
		}
		params.Set("filters", c.filters)
		if _, err := db.Exec(`UPDATE console_sub_models SET message=message||'changed' WHERE group_id=980`); err != nil {
			t.Fatal(err)
		}
		if response := call(owner, params, false); response.Code != 409 {
			t.Fatal("off-page evidence change accepted", response.Code)
		}
	}
}

func TestManagementHTTPCheckSelectionKeepsAllTargetsWithoutDiagnosticPayload(t *testing.T) {
	s := managementHydrationFixture(t)
	const marker = "BATCH_DIAGNOSTIC_MUST_NOT_BE_SERIALIZED"
	db := s.control.db
	for _, q := range []string{
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,active,state)
		     SELECT 'a',g,'G'||g,'openai',g,true,'done' FROM generate_series(62,980) g`,
		`INSERT INTO console_sub_models(account_id,group_id,model,state,message,result)
		     SELECT 'a',g,m,CASE WHEN g=980 AND m='gpt-6.2-sol' THEN 'queued' ELSE 'done' END,'',
		            jsonb_build_object('model',m,'checked_at',g,'verdict','pass','availability',
		              jsonb_build_object('status','success','text',repeat('` + marker + `',256)))
		     FROM generate_series(62,980) g CROSS JOIN unnest(ARRAY['gpt-6.2-sol','gpt-6-astra','gpt-5.5']) m`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	session, err := s.control.newSession(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	call := func(values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query?"+values.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, model := range []string{"gpt-6.2-sol", ""} {
		filters := `{"model":"` + model + `"}`
		pageParams := url.Values{"filters": {filters}}
		if model != "" {
			pageParams.Set("receipt_view", "selected")
			pageParams.Set("model_view", "selected")
		}
		first := call(pageParams)
		var page managementPageResponse
		if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil || first.Code != 200 || page.Total != 980 || len(page.Rows) != 25 {
			t.Fatalf("expected production-size page, got status=%d total=%d rows=%d err=%v", first.Code, page.Total, len(page.Rows), err)
		}
		params := url.Values{"filters": {filters}, "selection": {page.SelectionToken}, "check_selection": {"1"}}
		response := call(params)
		var result managementCheckSelectionResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 ||
			result.Format != "management-check-selection-v1" || result.Revision != page.Revision || result.Total != 980 || result.EligibleCount != 980 || len(result.Eligible) != 980 {
			t.Fatalf("invalid check selection: status=%d total=%d eligible=%d err=%v", response.Code, result.Total, len(result.Eligible), err)
		}
		if body := response.Body; body.Len() > 800_000 || strings.Contains(body.String(), marker) || strings.Contains(body.String(), `"input":`) {
			t.Fatal("batch response included diagnostic inventory", body.Len())
		}
		found := false
		for _, row := range result.Eligible {
			if row.Target.GroupID == 980 {
				found = len(row.Target.Models) == 1 && row.Target.Models[0].Model == "gpt-6.2-sol" && row.Target.Models[0].State == "queued"
			}
		}
		if !found {
			t.Fatal("off-page running or queued model was lost")
		}
		if model == "" {
			if _, err := db.Exec(`UPDATE console_sub_models SET state='done' WHERE account_id='a' AND group_id=980 AND model='gpt-6.2-sol'`); err != nil {
				t.Fatal(err)
			}
			if stale := call(params); stale.Code != http.StatusConflict {
				t.Fatal("stale check selection accepted", stale.Code)
			}
		}
	}
}

func TestManagementHTTPCompleteSelectionAuthorizationAndFreshness(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	s := managementBindingTestService(t)
	s.engine = postgresTestEngine(t)
	ctx := context.Background()
	for _, query := range []string{
		`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('owned','owner','Own Kiro','https://site.test','owner@example.test',''),('foreign','foreign','PRIVATE-ACCOUNT','https://foreign.test','private@example.test','')`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,active,state) SELECT 'owned',g,CASE WHEN g%2=0 THEN 'Claude KiRo proxy' ELSE 'Group' END,'openai',g,true,'done' FROM generate_series(1,61) g`,
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
	for _, exclusion := range []string{"kiro", "KIRO"} {
		params := url.Values{"filters": {`{"excludeName":"` + exclusion + `"}`}}
		filtered := call(ownerToken, params)
		var first, last managementPageResponse
		if err := json.Unmarshal(filtered.Body.Bytes(), &first); err != nil || filtered.Code != 200 || first.Total != 31 || first.EligibleCount != 31 || len(first.Rows) != 25 {
			t.Fatal("exclusion did not precede pagination", filtered.Code, first.Total, len(first.Rows), err)
		}
		params.Set("cursor", first.NextCursor)
		response := call(ownerToken, params)
		if err := json.Unmarshal(response.Body.Bytes(), &last); err != nil || response.Code != 200 || last.Offset != 25 || len(last.Rows) != 6 || last.NextCursor != "" {
			t.Fatal("excluded pagination scope changed", response.Code, last.Offset, len(last.Rows), err)
		}
		params.Del("cursor")
		params.Set("selection", first.SelectionToken)
		response = call(ownerToken, params)
		var all managementSelectionResponse
		if err := json.Unmarshal(response.Body.Bytes(), &all); err != nil || response.Code != 200 || all.Total != 31 || len(all.Rows) != 31 || len(all.Eligible) != 31 {
			t.Fatal("excluded selection scope changed", response.Code, all.Total, len(all.Rows), err)
		}
		for _, row := range all.Rows {
			if strings.Contains(strings.ToLower(row.Target.Name), "kiro") {
				t.Fatal("excluded channel remained selectable")
			}
		}
		params.Set("filters", `{}`)
		if response := call(ownerToken, params); response.Code != 400 {
			t.Fatal("selection reused after exclusion changed", response.Code)
		}
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
	for _, raw := range []string{`{"excludeName":5}`, `{"excludeName":"\u0000"}`, `{"excludeName":"` + strings.Repeat("x", 2049) + `"}`} {
		if _, err := parseManagementQuery(url.Values{"filters": {raw}}); err == nil {
			t.Fatal("invalid exclusion accepted")
		}
	}
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

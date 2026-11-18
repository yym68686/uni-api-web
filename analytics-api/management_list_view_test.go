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

func TestManagementOverviewMatchesListAcrossPages(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET state='running' WHERE group_id=25 AND model='gpt-5.5';
		UPDATE console_sub_models SET result=jsonb_set(result,'{availability,terminal_status}','"missing"') WHERE group_id=51 AND model='gpt-5.5';
		UPDATE console_sub_models SET state='interrupted' WHERE group_id=61 AND model='extra-model'`); err != nil {
		t.Fatal(err)
	}
	fullCtx := context.WithValue(context.Background(), managementListViewKey{}, true)
	ctx := context.WithValue(fullCtx, managementOverviewKey{}, true)
	c := ""
	for offset := 0; offset < 61; offset += managementPageSize {
		full, err := s.readManagementQuerySnapshot(fullCtx, "owner", managementResetFilters, c, false)
		if err != nil {
			t.Fatal(err)
		}
		var got managementQueryPage
		if c == "" {
			snapshot, err := s.readManagementQuerySnapshot(ctx, "owner", managementResetFilters, c, false)
			if err != nil || !s.rememberManagementPagingPlanMode("owner", managementResetFilters, snapshot, true) {
				t.Fatal("overview directory was not retained", err)
			}
			got = snapshot.Page
		} else {
			page, found, err := s.currentManagementPagingPage(ctx, "owner", managementResetFilters, c, false)
			if err != nil || !found {
				t.Fatal("current overview missed", err)
			}
			got = page.managementQueryPage
		}
		for i := range full.Page.Rows {
			want := full.Page.Rows[i]
			managementApplyOverview(&want)
			row := got.Rows[i]
			if row.ID != want.ID || mustJSON(row.Target.ListOverview) != mustJSON(want.Target.ListOverview) ||
				mustJSON(row.Target.Models) != mustJSON(want.Target.Models) || len(row.Checks) != 0 {
				t.Fatalf("overview changed row %d at offset %d", i, offset)
			}
		}
		if len(got.Rows) == 0 || got.Offset != offset || got.Revision != full.Page.Revision ||
			len(mustJSON(got)) >= len(mustJSON(full.Page))/2 {
			t.Fatal("overview changed pagination or did not reduce wire size")
		}
		c = got.NextCursor
	}
}

func TestManagementOverviewScopedSignalsKeepFallbackAndOwnership(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth)
		VALUES ('b','other','fixture','https://b.test','fixture','');
		INSERT INTO console_sub_targets(account_id,group_id,name,platform,active,state) VALUES ('b',1,'private','openai',true,'done');
		INSERT INTO console_sub_models(account_id,group_id,model,state) VALUES ('b',1,'private-model','running')`); err != nil {
		t.Fatal(err)
	}
	wanted := []subBoundKey{{AccountID: "a", GroupID: 1}, {AccountID: "b", GroupID: 1}, {AccountID: "a", GroupID: 2}}
	a, b, c := &subTarget{}, &subTarget{}, &subTarget{}
	slots := map[string]*subTarget{qualityGroupID("a", 1): a, qualityGroupID("b", 1): b, qualityGroupID("a", 2): c}
	if ok, err := readManagementPageOverview(ctx, db, "owner", wanted, slots); err != nil || !ok || len(a.Models) != 3 || len(c.Models) != 3 || len(b.Models) != 0 {
		t.Fatalf("wrong grouped overview or ownership: ok=%t err=%v", ok, err)
	}
	a.Models, c.Models = nil, nil
	for _, change := range []string{
		`UPDATE console_management_model_signal SET valid=false WHERE account_id='a' AND group_id=2 AND model='gpt-5.5'`,
		`DELETE FROM console_management_model_signal WHERE account_id='a' AND group_id=2 AND model='gpt-5.5'`,
	} {
		if _, err := db.Exec(change); err != nil {
			t.Fatal(err)
		}
		if ok, err := readManagementPageOverview(ctx, db, "owner", wanted, slots); err != nil || ok || len(a.Models) != 0 || len(c.Models) != 0 {
			t.Fatalf("partial or invalid overview accepted: ok=%t err=%v", ok, err)
		}
	}
}

func TestManagementListViewAllPagesAndPriceContract(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	session, err := s.control.newSession(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	request := func(filters managementQueryFilters, cursor, selection, tag string, list bool) (*httptest.ResponseRecorder, managementPageResponse) {
		t.Helper()
		query := url.Values{"filters": {mustJSON(filters)}}
		if cursor != "" {
			query.Set("cursor", cursor)
			query.Set("refresh_cursor", "1")
		}
		if selection != "" {
			query.Set("selection", selection)
		}
		r := httptest.NewRequest("GET", "/v1/channel-management/query?"+query.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		r.Header.Set("Accept", managementPageV3MediaType)
		r.Header.Set("X-Console-Current-Validation", "1")
		r.Header.Set("If-None-Match", tag)
		if list {
			r.Header.Set(managementListViewHeader, managementListViewV1)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var page managementPageResponse
		if w.Code == 200 && selection == "" {
			var columns managementColumnPage
			if err := json.Unmarshal(w.Body.Bytes(), &columns); err != nil {
				t.Fatal(err)
			}
			page, err = expandManagementColumnPage(columns)
			if err != nil {
				t.Fatal(err)
			}
		}
		return w, page
	}
	var trace bytes.Buffer
	writer := log.Writer()
	log.SetOutput(&trace)
	defer log.SetOutput(writer)
	for _, filter := range []managementQueryFilters{
		managementResetFilters,
		{GroupStatus: "all", Model: "gpt-5.5", Availability: "success"},
		{GroupStatus: "all", Model: "gpt-5.5", PriceStatus: "unconfirmed"},
	} {
		cursor := ""
		for range 3 {
			trace.Reset()
			w, lean := request(filter, cursor, "", "", true)
			if w.Code != 200 || strings.Contains(trace.String(), "stage=hydrate_receipts") {
				t.Fatalf("lean page failed or read page receipts: status=%d trace=%s", w.Code, trace.String())
			}
			full, expected := request(filter, cursor, "", w.Header().Get("ETag"), false)
			if full.Code != 200 && !(full.Code == 304 && len(lean.Rows) == 0) ||
				(len(lean.Rows) > 0 && full.Header().Get("ETag") == w.Header().Get("ETag")) ||
				!strings.Contains(strings.Join(w.Header().Values("Vary"), ","), managementListViewHeader) {
				t.Fatal("price and lean representations shared an HTTP validator")
			}
			if full.Code == 304 {
				break // The two empty bodies are identical, regardless of representation.
			}
			if len(expected.Rows) > 0 &&
				(expected.Rows[0].Target.ListSummary || len(lean.Rows) == 0 || !lean.Rows[0].Target.ListSummary || w.Body.Len() >= full.Body.Len()) {
				t.Fatal("projection missing or no smaller than legacy")
			}
			managementListRows(&expected.managementQueryPage)
			if mustJSON(lean) != mustJSON(expected) {
				t.Fatalf("list changed order, filters, or visible evidence at offset %d", lean.Offset)
			}
			same, _ := request(filter, cursor, "", w.Header().Get("ETag"), true)
			if same.Code != 304 {
				t.Fatalf("matching lean validator returned %d", same.Code)
			}
			if cursor == "" {
				selection, _ := request(filter, "", lean.SelectionToken, "", true)
				if selection.Code != 200 || !bytes.Contains(selection.Body.Bytes(), []byte("management-selection-v1")) ||
					bytes.Contains(selection.Body.Bytes(), []byte("list_summary")) {
					t.Fatal("list mode leaked into signed selection", selection.Code)
				}
			}
			cursor = lean.NextCursor
			if cursor == "" {
				if filter.Model == "" && lean.Offset != 50 {
					t.Fatalf("default test did not reach the last page, offset=%d", lean.Offset)
				}
				break
			}
		}
	}
}

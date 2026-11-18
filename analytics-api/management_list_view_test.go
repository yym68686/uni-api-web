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

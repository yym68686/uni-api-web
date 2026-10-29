package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagementPagingDirectoryMatchesFullReadAndRejectsChangedEvidence(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	for _, model := range []string{"", "gpt-5.5", "extra-model"} {
		f := managementQueryFilters{GroupStatus: "all", Model: model, Sort: "asc"}
		first, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
		if err != nil {
			t.Fatal(err)
		}
		s.rememberManagementPagingPlan("owner", f, first)
		stored := s.managementComputed.pages[managementPagingKey("owner", f)]
		if first.Filtered == nil {
			t.Fatal("snapshot discarded evaluated rows")
		}
		previous := &Service{}
		recomputed := first
		recomputed.Filtered = nil
		previous.rememberManagementPagingPlan("owner", f, recomputed)
		if mustJSON(stored) != mustJSON(previous.managementComputed.pages[managementPagingKey("owner", f)]) {
			t.Fatal("hydrated snapshot changed retained ordering or proof")
		}
		if len(stored.Plan) == 0 || stored.Bytes != len(stored.Plan) {
			t.Fatal("directory not bounded")
		}
		var plan managementPagingPlan
		if err = json.Unmarshal(stored.Plan, &plan); err != nil {
			t.Fatal(err)
		}
		for _, a := range plan.Input.Accounts {
			for _, group := range a.Targets {
				if group.Models != nil {
					t.Fatal("all-model payload retained")
				}
			}
		}
		cursor := first.Page.NextCursor
		for cursor != "" {
			before := time.Now()
			got, ok, err := s.currentManagementPagingPage(ctx, "owner", f, cursor, false)
			elapsed := time.Since(before)
			if err != nil || !ok {
				t.Fatal("fresh directory missed", err)
			}
			before = time.Now()
			full, err := s.readManagementQuerySnapshot(ctx, "owner", f, cursor, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("model=%q offset=%d directory=%s full=%s bytes=%d", model, got.Offset, elapsed, time.Since(before), stored.Bytes)
			got.ReadAtMS = full.Page.ReadAtMS
			want := managementPageResponse{Owner: "owner", managementQueryPage: full.Page, Accounts: managementAccountHeaders(full.View.Input.Accounts), Context: managementContextForPage(full.View, full.Page, f.Model), SelectionToken: encodeManagementCursor(s.control.key, "owner", managementPageCursor{Version: 1, Revision: full.Page.Revision, Filter: tokenHash(mustJSON(f))})}
			if mustJSON(got) != mustJSON(want) {
				t.Fatal("page lost complete fields/facets or ordering", model, got.Offset)
			}
			cursor = got.NextCursor
		}
		if _, ok, err := s.currentManagementPagingPage(ctx, "foreign", f, first.Page.NextCursor, false); err != nil || ok {
			t.Fatal("foreign directory exposed")
		}
		// Requests decode their own copy; concurrent hydration cannot mutate the
		// shared directory or leak rows from another page.
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if got, ok, err := s.currentManagementPagingPage(ctx, "owner", f, first.Page.NextCursor, false); err != nil || !ok || got.Offset != 25 {
					t.Error("parallel page", err)
				}
			}()
		}
		wg.Wait()
		if string(s.managementComputed.pages[managementPagingKey("owner", f)].Plan) != string(stored.Plan) {
			t.Fatal("shared plan mutated")
		}
		if _, err = s.control.db.Exec(`UPDATE console_sub_models SET message=message||'changed' WHERE group_id=61 AND model='extra-model'`); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.currentManagementPagingPage(ctx, "owner", f, first.Page.NextCursor, false); err != nil || ok {
			t.Fatal("off-page changed evidence reused", err)
		}
	}
}

func TestManagementPagingCanonicalizesCursorForFirstPage(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	filters := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5", Sort: "asc"}
	first, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
	if err != nil {
		t.Fatal(err)
	}
	s.rememberManagementPagingPlan("owner", filters, first)
	firstCursor := encodeManagementCursor(s.control.key, "owner", managementPageCursor{
		Version:  1,
		Offset:   0,
		Revision: first.Page.Revision,
		Filter:   tokenHash(mustJSON(filters)),
	})
	page, ok, err := s.currentManagementPagingPage(ctx, "owner", filters, firstCursor, false)
	if err != nil || !ok || page.Offset != 0 {
		t.Fatal("first page cursor was not accepted", err, ok, page.Offset)
	}
	canonical := managementComputedKey("owner", filters, "")
	if _, ok, err := s.currentComputedManagementPage(ctx, "owner", canonical); err != nil || !ok {
		t.Fatal("cursor-first page was not retained under the initial-page key", err, ok)
	}
	refreshKey := managementComputedKey("owner", filters, firstCursor+"\x00refresh")
	if _, ok, _ := s.currentComputedManagementPage(ctx, "owner", refreshKey); ok {
		t.Fatal("normal cursor unexpectedly populated the refresh generation")
	}
}

func TestManagementPagingHTTPReusesCanonicalFirstPageCursor(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	session, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	filters := `{"groupStatus":"all","model":"gpt-5.5","sort":"asc"}`
	request := func(cursor string, rebase bool) *httptest.ResponseRecorder {
		t.Helper()
		query := url.Values{"filters": {filters}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if rebase {
			query.Set("refresh_cursor", "1")
		}
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query?"+query.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	call := func(cursor string, rebase bool) managementPageResponse {
		t.Helper()
		w := request(cursor, rebase)
		if w.Code != http.StatusOK {
			t.Fatalf("query failed: %d %s", w.Code, w.Body.String())
		}
		var page managementPageResponse
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	first := call("", false)
	if first.NextCursor == "" {
		t.Fatal("fixture did not produce a next cursor")
	}
	second := call(first.NextCursor, true)
	if second.PreviousCursor == "" {
		t.Fatal("fixture did not produce a previous cursor")
	}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	for _, rebase := range []bool{false, true} {
		logs.Reset()
		back := call(second.PreviousCursor, rebase)
		if back.Offset != 0 || mustJSON(back.Rows) != mustJSON(first.Rows) {
			t.Fatal("cursor first page changed public values", rebase)
		}
		if !strings.Contains(logs.String(), "management_current_computation verified=true") || strings.Contains(logs.String(), "management_paging_current verified=true") {
			t.Fatal("cursor first page was rehydrated instead of reusing its verified computation", rebase, logs.String())
		}
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET name='new committed name' WHERE group_id=1`); err != nil {
		t.Fatal(err)
	}
	current := call("", false)
	if current.Revision == first.Revision {
		t.Fatal("fixture did not change the current revision")
	}
	if w := request(second.PreviousCursor, false); w.Code != http.StatusConflict {
		t.Fatalf("old first-page cursor bypassed the revision fence: %d %s", w.Code, w.Body.String())
	}
	logs.Reset()
	if rebased := call(second.PreviousCursor, true); rebased.Revision != current.Revision || mustJSON(rebased.Rows) != mustJSON(current.Rows) {
		t.Fatal("refresh first-page cursor did not return current values")
	}
	if !strings.Contains(logs.String(), "management_current_computation verified=true") {
		t.Fatal("refresh cursor did not reuse the current canonical first page", logs.String())
	}
}

func TestManagementRefreshCursorKeepsPositionButNeverRebasesFullSelection(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	f := managementQueryFilters{GroupStatus: "all"}
	first, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.readManagementQuerySnapshot(ctx, "owner", f, first.Page.NextCursor, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.control.db.Exec(`UPDATE console_sub_targets SET name='edited' WHERE group_id=30`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.readManagementQuerySnapshot(ctx, "owner", f, first.Page.NextCursor, false); !errors.Is(err, errManagementRevision) {
		t.Fatal("legacy cursor became unfenced", err)
	}
	fresh, err := s.readManagementQuerySnapshotMode(ctx, "owner", f, first.Page.NextCursor, false, true)
	if err != nil || fresh.Page.Offset != 25 || fresh.Page.Revision == first.Page.Revision {
		t.Fatal("rebase lost page", err)
	}
	s.rememberManagementPagingPlan("owner", f, fresh)
	got, ok, err := s.currentManagementPagingPage(ctx, "owner", f, first.Page.NextCursor, true)
	if err != nil || !ok || got.Offset != 25 || got.Revision != fresh.Page.Revision {
		t.Fatal("directory rebase", err)
	}
	if _, _, err = s.currentManagementPagingPage(ctx, "owner", f, first.Page.NextCursor, false); !errors.Is(err, errManagementRevision) {
		t.Fatal("rebase weakened legacy cursor", err)
	}
	// Removing the final page clamps to the last existing page, not page one.
	if _, err = s.control.db.Exec(`DELETE FROM console_sub_targets WHERE group_id>49`); err != nil {
		t.Fatal(err)
	}
	last, err := s.readManagementQuerySnapshotMode(ctx, "owner", f, second.Page.NextCursor, false, true)
	if err != nil || last.Page.Offset != 25 || last.Page.Total != 49 || len(last.Page.Rows) != 24 {
		t.Fatal("last-page removal", err, last.Page.Offset, last.Page.Total)
	}
	ownerToken, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	call := func(values url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/channel-management/query?"+values.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: ownerToken})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	for _, values := range []url.Values{
		{"refresh_cursor": {"1"}},
		{"cursor": {first.Page.NextCursor}, "refresh_cursor": {"invalid"}},
		{"selection": {first.Page.NextCursor}, "refresh_cursor": {"1"}},
		{"cursor": {"forged"}, "refresh_cursor": {"1"}},
	} {
		if w := call(values); w.Code != 400 {
			t.Fatal("invalid position accepted", w.Code)
		}
	}
	w := call(url.Values{"filters": {mustJSON(f)}, "cursor": {first.Page.NextCursor}, "refresh_cursor": {"1"}})
	var p managementPageResponse
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || w.Code != 200 || p.Offset != 25 {
		t.Fatal("HTTP same-page refresh", w.Code, w.Body.String())
	}
	// A strict client must not consume the response stored for a rebase request.
	w = call(url.Values{"filters": {mustJSON(f)}, "cursor": {first.Page.NextCursor}})
	if w.Code != 409 {
		t.Fatal("rebase computation reused for strict client", w.Code)
	}
}

func TestManagementHydratedPagingResponseIsRetainedWithCurrentProof(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	first, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil {
		t.Fatal(err)
	}
	s.rememberManagementPagingPlan("owner", f, first)
	for _, rebase := range []bool{false, true} {
		cursor := first.Page.NextCursor
		page, ok, err := s.currentManagementPagingPage(ctx, "owner", f, cursor, rebase)
		if err != nil || !ok || page.Offset != managementPageSize {
			t.Fatal("paging fixture failed", err)
		}
		if page.preparedColumns == nil {
			t.Fatal("hydrated pagination discarded its already encoded columns")
		}
		if rebase {
			cursor += "\x00refresh"
		}
		key := managementComputedKey("owner", f, cursor)
		cached, ok, err := s.currentComputedManagementPage(ctx, "owner", key)
		if err != nil || !ok {
			t.Fatal("completed pagination not retained", err)
		}
		cached.ReadAtMS = page.ReadAtMS
		if mustJSON(cached) != mustJSON(page) {
			t.Fatal("retained page differs from fully hydrated response")
		}
		if _, ok, err = s.currentComputedManagementPage(ctx, "foreign", managementComputedKey("foreign", f, cursor)); err != nil || ok {
			t.Fatal("foreign paging response exposed", err)
		}
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET message='new evidence' WHERE group_id=61`); err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{first.Page.NextCursor, first.Page.NextCursor + "\x00refresh"} {
		if _, ok, err := s.currentComputedManagementPage(ctx, "owner", managementComputedKey("owner", f, cursor)); err != nil || ok {
			t.Fatal("retained pagination accepted changed off-page evidence", err)
		}
	}
}

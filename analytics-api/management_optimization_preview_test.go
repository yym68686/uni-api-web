package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestManagementOptimizationPreviewReadsFreshCompleteFilteredSnapshotWithoutExecuting(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	filters := managementQueryFilters{GroupStatus: "available", Model: "gpt-5.5", Sort: "asc"}
	old, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
	if err != nil {
		t.Fatal(err)
	}
	token := encodeManagementCursor(s.control.key, "owner", managementPageCursor{Version: 1, Revision: old.Page.Revision, Filter: tokenHash(mustJSON(filters))})
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','987') WHERE group_id=61 AND model='gpt-5.5';
 INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('foreign','foreign','PRIVATE-SITE','https://foreign.test','private@example.test','');
 INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,active,state) VALUES('foreign',1,'PRIVATE-TARGET','openai',1,true,'done')`); err != nil {
		t.Fatal(err)
	}
	ownerToken, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	call := func(path, session string, params url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/"+path+"?"+params.Encode(), nil)
		if session != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
			t.Fatal("preview acquired cache validators")
		}
		return w
	}
	params := url.Values{"filters": {mustJSON(filters)}}
	if got := call("optimization-preview", "", params); got.Code != http.StatusUnauthorized {
		t.Fatal("anonymous preview accepted", got.Code)
	}
	stale := url.Values{"filters": {mustJSON(filters)}, "selection": {token}}
	if got := call("query", ownerToken, stale); got.Code != http.StatusConflict {
		t.Fatal("batch detection's old selection fence weakened", got.Code)
	}
	response := call("optimization-preview", ownerToken, params)
	var preview managementOptimizationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil || response.Code != 200 {
		t.Fatal("fresh preview rejected after old page changed", response.Code, err, response.Body.String())
	}
	if preview.Format != "management-optimization-preview-v1" || preview.Owner != "owner" || preview.Filters != filters || preview.Revision == old.Page.Revision || preview.Total != 61 || len(preview.Rows) != 61 || len(preview.Eligible) != 61 {
		t.Fatal("preview did not read complete current filter scope", preview.Total, len(preview.Rows))
	}
	if strings.Contains(response.Body.String(), "PRIVATE") || strings.Contains(response.Body.String(), "private@example") || strings.Contains(response.Body.String(), "selection_token") {
		t.Fatal("preview exposed foreign data or execution permission")
	}
	full, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", true)
	if err != nil || len(full.Rows) != len(preview.Rows) {
		t.Fatal("complete evidence read failed", err)
	}
	for i, row := range preview.Rows {
		// Selected-model summaries intentionally omit other models' results.
		// The selected model and group-level capabilities must remain exact.
		other := full.Rows[i]
		if row.ID != other.ID || mustJSON(row.Selected) != mustJSON(other.Selected) || !reflect.DeepEqual(row.Target.ToolUse, other.Target.ToolUse) || !reflect.DeepEqual(row.Target.Compaction, other.Target.Compaction) || !reflect.DeepEqual(row.Target.QualityCheck, other.Target.QualityCheck) {
			t.Fatal("preview changed selected model or capability evidence", row.ID)
		}
	}
	found := false
	for _, row := range preview.Rows {
		if row.Target.GroupID == 61 {
			found = row.Selected != nil && row.Selected.Result != nil && row.Selected.Result.Availability.TTFT != nil && *row.Selected.Result.Availability.TTFT == 987
		}
	}
	if !found {
		t.Fatal("preview reused stale off-page model evidence")
	}
	filtered := filters
	filtered.Search = "G61"
	response = call("optimization-preview", ownerToken, url.Values{"filters": {mustJSON(filtered)}})
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil || response.Code != 200 || preview.Total != 1 || len(preview.Rows) != 1 || preview.Rows[0].Target.GroupID != 61 || preview.Filters != filtered {
		t.Fatal("preview lost requested filters", response.Code, err)
	}
	for _, invalid := range []url.Values{nil, {"filters": {"{}"}, "selection": {token}}, {"filters": {"{}"}, "cursor": {token}}, {"filters": {"{}", "{}"}}, {"filters": {`{"unknown":"x"}`}}} {
		if got := call("optimization-preview", ownerToken, invalid); got.Code != 400 {
			t.Fatal("preview accepted an execution token or invalid filter", got.Code)
		}
	}
	var count int
	if err := s.control.db.QueryRow(`SELECT (SELECT count(*) FROM console_sub_check_queue)+(SELECT count(*) FROM console_quality_group_links)+(SELECT count(*) FROM console_configured_channels)+(SELECT count(*) FROM console_control_snapshots)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("preview persisted a job, route or derived state", count, err)
	}
}

func TestManagementOptimizationPreviewWithoutModelKeepsAllModelEvidence(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	filters := managementQueryFilters{GroupStatus: "available"}
	preview, err := s.readManagementQuerySnapshotWithPreview(ctx, "owner", filters, "", false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", true)
	if err != nil || preview.Page.Revision != full.Page.Revision || !reflect.DeepEqual(preview.Rows, full.Rows) || len(preview.Rows) != 61 {
		t.Fatal("all-model optimization preview lost off-page model evidence", err)
	}
}

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestManagementOptimizationPreviewReadsTierEvidenceOnConsecutiveRequests(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	// A new model summary contains service_tiers, which v1 predicate
	// projections intentionally reject. A legacy/missing selected receipt
	// must also read its current typed source, without loading other models.
	now := time.Now().Unix()
	if _, err := s.control.db.Exec(`UPDATE console_sub_models
 SET result=jsonb_set(result,'{service_tiers}',jsonb_build_object(
 'fast',jsonb_build_object('status','supported','checked_at',$1::bigint),
 'ultrafast',jsonb_build_object('status','not_honored','checked_at',$1::bigint)))
 WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"Input_Price":2.5,"output_price":3}' WHERE id='a-1-gpt-5.5';
 DELETE FROM console_management_model_signal WHERE group_id=2 AND model='gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if _, compact, err := readManagementCompactRevision(ctx, s.control.db, "owner"); err != nil || compact {
		t.Fatal("fixture did not invalidate the global proof", compact, err)
	}
	token, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	handler := s.Handler()
	call := func(filters managementQueryFilters, total int) managementOptimizationResponse {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/optimization-preview?"+url.Values{"filters": {mustJSON(filters)}}.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		r.Header.Set("Accept-Encoding", "br, gzip")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("preview returned %d, encoding=%q: %s\n%s", w.Code, w.Header().Get("Content-Encoding"), w.Body.String(), output.String())
		}
		reader, err := gzip.NewReader(w.Body)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		var preview managementOptimizationResponse
		if err := json.NewDecoder(reader).Decode(&preview); err != nil || preview.Total != total || len(preview.Rows) != total || preview.Filters != filters {
			t.Fatal("preview response was not complete and decodable", err, preview.Total, len(preview.Rows))
		}
		check := preview.Rows[0].Selected
		if check == nil || check.Result == nil || check.Result.ServiceTiers["fast"].Status != "supported" ||
			check.Result.ServiceTiers["ultrafast"].Status != "not_honored" ||
			check.Result.Availability.Usage == nil || check.Result.Availability.Usage.InputPrice == nil ||
			*check.Result.Availability.Usage.InputPrice != 2.5 ||
			preview.Rows[0].Target.SummaryModel != filters.Model {
			t.Fatal("preview omitted selected model or current receipt evidence", check)
		}
		for _, row := range preview.Rows {
			for _, model := range row.Target.Models {
				if model.Model == "extra-model" && model.Result != nil {
					t.Fatal("preview unexpectedly hydrated unrelated model evidence", row.ID)
				}
			}
		}
		return preview
	}
	filters := managementQueryFilters{GroupStatus: "available", Model: "gpt-5.5", Availability: "success"}
	first := call(filters, 61)
	second := call(filters, 61)
	if first.Revision != second.Revision || !reflect.DeepEqual(first.Rows, second.Rows) {
		t.Fatal("second read did not return the same consistent preview")
	}
	filters.Fast, filters.Ultrafast = "supported", "not_honored"
	call(filters, 1)
	logs := output.String()
	if strings.Count(logs, "management_typed_preview directory_models=183 evidence_models=122") != 3 ||
		strings.Contains(logs, "model_scoped=false") || strings.Contains(logs, "management_rows_read stage=page_models ") {
		t.Fatal("preview triggered an owner-wide evidence scan", logs)
	}
}

func TestManagementOptimizationPreviewKeepsSelectedProjectionAcrossLargeDirectory(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	// Match a full-site preview, not a 25-row display page: the directory is
	// compact when dictionary-packed, but exceeds the 8 MiB expanded SQL input
	// budget when all groups are hydrated together.
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,active,state)
 SELECT 'a',g,'G'||g,'openai',g,true,'done' FROM generate_series(62,842) g;
 INSERT INTO console_sub_models(account_id,group_id,model,state,message,result)
 SELECT 'a',g,m,'done','model message',jsonb_build_object('model',m,'checked_at',g,
 'availability',jsonb_build_object('status','success','protocol','responses','model_match','match','response_model',m,'ttft_ms',123))
 FROM generate_series(1,842) g CROSS JOIN unnest(ARRAY['gpt-6.2-sol','gpt-6-astra']) m
 ON CONFLICT(account_id,group_id,model) DO NOTHING;
 INSERT INTO console_sub_models(account_id,group_id,model,state,message,result)
 SELECT 'a',g,'off-scope-'||m,'done','model message','{"availability":{"status":"success","ttft_ms":456}}'::jsonb
 FROM generate_series(1,842) g CROSS JOIN generate_series(1,240) m`); err != nil {
		t.Fatal(err)
	}
	session, err := s.control.newSession(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	filters := managementQueryFilters{GroupStatus: "available", Model: "gpt-6.2-sol", Sort: "asc"}
	r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/optimization-preview?"+url.Values{"filters": {mustJSON(filters)}}.Encode(), nil)
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
	handler := s.Handler()
	call := func() managementOptimizationResponse {
		t.Helper()
		output.Reset()
		w := httptest.NewRecorder()
		started := time.Now()
		handler.ServeHTTP(w, r.Clone(r.Context()))
		var preview managementOptimizationResponse
		if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != 200 {
			t.Fatal("large preview failed", w.Code, err, output.String())
		}
		if len(preview.Rows) != 842 || preview.Total != 842 || preview.Filters != filters {
			t.Fatal("preview truncated or changed its scope", preview.Total, len(preview.Rows))
		}
		for _, row := range preview.Rows {
			if row.Selected == nil || row.Selected.Result == nil || row.Selected.Result.Availability.TTFT == nil || *row.Selected.Result.Availability.TTFT != 123 || row.Target.SummaryModel != filters.Model || len(row.Target.Models) < 242 {
				t.Fatal("selected evidence or complete metadata missing", row.ID)
			}
			for _, model := range row.Target.Models {
				if model.Model != filters.Model && model.Model != checkModel && model.Result != nil {
					t.Fatal("large preview fell back to off-scope model results", row.ID, model.Model)
				}
			}
		}
		t.Logf("large preview: groups=%d models>=%d bytes=%d elapsed=%s", len(preview.Rows), 842*242, w.Body.Len(), time.Since(started))
		return preview
	}
	call()
	if strings.Contains(output.String(), "management_rows_read stage=page_models ") || strings.Count(output.String(), "management_selected_models ") != 34 {
		t.Fatal("large preview lost bounded selected-model reads", output.String())
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{service_tiers}',
 '{"fast":{"status":"supported","checked_at":1}}') WHERE group_id<=204 AND model='gpt-6.2-sol'`); err != nil {
		t.Fatal(err)
	}
	preview := call()
	if strings.Contains(output.String(), "model_scoped=false") ||
		strings.Contains(output.String(), "management_rows_read stage=page_models ") ||
		strings.Count(output.String(), "management_rows_read stage=preview_selected_models ") != 34 ||
		!strings.Contains(output.String(), "evidence_models=1684 ") {
		t.Fatal("large preview lost bounded typed evidence reads", output.String())
	}
	if preview.Rows[0].Selected.Result.ServiceTiers["fast"].Status != "supported" {
		t.Fatal("large preview lost service-tier evidence")
	}
}

func TestManagementOptimizationPreviewDoesNotFallBackForUnrelatedLegacyEvidence(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	// Model metadata and state remain in the directory, but this invalid
	// off-scope projection must not cause a global scan of full results.
	if _, err := s.control.db.Exec(`UPDATE console_management_model_signal SET valid=false WHERE model='extra-model'`); err != nil {
		t.Fatal(err)
	}
	if _, compact, err := readManagementCompactRevision(ctx, s.control.db, "owner"); err != nil || compact {
		t.Fatal("fixture did not disable the global compact proof", compact, err)
	}
	filters := managementQueryFilters{GroupStatus: "available", Model: "gpt-5.5", Availability: "success"}
	token, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/optimization-preview?"+url.Values{"filters": {mustJSON(filters)}}.Encode(), nil)
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var preview managementOptimizationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != 200 || len(preview.Rows) != 61 {
		t.Fatal("scoped preview failed", w.Code, err, len(preview.Rows), output.String())
	}
	logs := output.String()
	if !strings.Contains(logs, "compact_revision=false catalog_only=false model_scoped=true models=183 receipt_ids=244") || strings.Contains(logs, "model_scoped=false") {
		t.Fatal("preview fell back to an owner-wide evidence scan", logs)
	}
	for _, row := range preview.Rows {
		if row.Target.SummaryModel != filters.Model || row.Selected == nil || row.Selected.Result == nil ||
			row.Selected.Result.Availability.Usage == nil || row.Selected.Result.Availability.Usage.InputPrice == nil ||
			*row.Selected.Result.Availability.Usage.InputPrice != 2 {
			t.Fatal("preview lost selected receipt or full model metadata", row.ID)
		}
	}
}

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

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestManagementSelectedModelsKeepSelectedEvidenceAndCompleteSelection(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	session, err := s.control.newSession(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	call := func(cursor, selection, modelView, receiptView string) *httptest.ResponseRecorder {
		t.Helper()
		values := url.Values{"filters": {mustJSON(f)}}
		for k, v := range map[string]string{"cursor": cursor, "selection": selection, "model_view": modelView, "receipt_view": receiptView} {
			if v != "" {
				values.Set(k, v)
			}
		}
		r := httptest.NewRequest("GET", "/v1/channel-management/query?"+values.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	read := func(cursor, mode string) managementPageResponse {
		t.Helper()
		w := call(cursor, "", mode, "selected")
		var out managementPageResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return out
	}
	for _, cursor := range []string{"", read("", "").NextCursor} {
		full := read(cursor, "")
		for range 2 {
			slim := read(cursor, "selected")
			if slim.Revision != full.Revision || slim.Total != full.Total || slim.EligibleCount != full.EligibleCount || !reflect.DeepEqual(slim.Models, full.Models) || !reflect.DeepEqual(slim.DetectionModels, full.DetectionModels) || !reflect.DeepEqual(slim.Context, full.Context) {
				t.Fatal("projection changed predicates, facets, quality, or selection revision")
			}
			for i, row := range slim.Rows {
				want := full.Rows[i]
				if row.Target.SummaryModel != f.Model || row.Target.ReceiptModel != f.Model || row.ID != want.ID || !reflect.DeepEqual(row.Selected, want.Selected) || len(row.Checks) != len(want.Checks) || len(row.Target.Models) != len(want.Target.Models) {
					t.Fatal("selected check or complete model directory changed")
				}
				for j, check := range row.Target.Models {
					if check.Model == f.Model || check.Model == checkModel {
						if !reflect.DeepEqual(check, want.Target.Models[j]) {
							t.Fatal("selected or shared quality evidence changed")
						}
					} else if check.Result != nil || check.Model != want.Target.Models[j].Model || check.State != want.Target.Models[j].State || check.Message != want.Target.Models[j].Message {
						t.Fatal("nonselected payload retained or its metadata changed")
					}
				}
			}
		}
		fresh := read(cursor, "")
		fresh.ReadAtMS = full.ReadAtMS
		if !reflect.DeepEqual(fresh, full) {
			t.Fatal("projected response polluted legacy or receipt-only cache")
		}
		w := call("", full.SelectionToken, "", "")
		var selected managementSelectionResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &selected) != nil || len(selected.Rows) != 61 || selected.Rows[0].Target.Models[0].Result.Availability.TTFT == nil {
			t.Fatal("complete selection changed", w.Code)
		}
		if w := call("", full.SelectionToken, "selected", "selected"); w.Code != 400 {
			t.Fatal("partial full-selection accepted")
		}
	}
	for _, modes := range [][2]string{{"selected", ""}, {"other", "selected"}} {
		if w := call("", "", modes[0], modes[1]); w.Code != 400 {
			t.Fatal("invalid negotiation accepted")
		}
	}
	f.Model = ""
	if w := call("", "", "selected", "selected"); w.Code != 400 {
		t.Fatal("all-model projection accepted")
	}
}

func TestManagementSelectedModelsReadVolumeAndConservativeFallback(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result)
 SELECT 'a',g,'off-scope-'||m,'done','{"availability":{"status":"success"}}'::jsonb
 FROM generate_series(1,25) g CROSS JOIN generate_series(1,240) m`); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.WithValue(context.Background(), managementSelectedReceiptsKey{}, true), managementSelectedModelsKey{}, true)
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	page, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil || len(page.Page.Rows) != 25 {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "management_selected_models groups=25 full_groups=0 summary_rows=50 directory_rows=6075 ") {
		t.Fatal("did not remove nonselected summary reads", output.String())
	}
	// A prepared projection is never guessed from a selected model alone.
	// Without the exact directory, the existing full reader remains authoritative.
	count, projected := 0, map[string]bool{}
	projected, err = readManagementDisplayModelStream(ctx, s.control.db, "owner", []subBoundKey{{AccountID: "a", GroupID: 1}}, nil, f.Model, func(row managementPageModelRecord, trace *managementRowsTrace) error {
		if len(row.result) > 0 {
			count++
		}
		return nil
	})
	if err != nil || len(projected) != 0 || count != 243 {
		t.Fatal("unprepared reader did not preserve full results", err, count)
	}
	f.Model = "extra-model"
	page, err = s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil || page.Page.Rows[0].Target.SummaryModel != "" || page.Page.Rows[0].Target.Models[0].Result == nil {
		t.Fatal("custom-model fallback was projected", err)
	}
	if managementPageComputedKey(ctx, "owner", f, "") == managementPageComputedKey(context.WithValue(context.Background(), managementSelectedReceiptsKey{}, true), "owner", f, "") {
		t.Fatal("summary modes share a response/validator key")
	}
}

func TestManagementSelectedModelsSQLPreservesOwnershipRawFallbackAndSnapshot(t *testing.T) {
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`DELETE FROM console_sub_model_summaries WHERE account_id='a' AND group_id=1 AND model='gpt-5.5';
 UPDATE console_sub_models SET result='null'::jsonb WHERE account_id='a' AND group_id=1 AND model='gpt-6-astra';
 INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('foreign','other','private','https://private.test','private','');
 INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('foreign',1,'private','openai');
 INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES('foreign',1,'private','done','{"checked_at":999}')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	metadata := []managementPageDirectoryModel{{"a", 1, "gpt-5.5", "done", "model message"}, {"a", 1, "gpt-6-astra", "done", "model message"}, {"foreign", 1, "private", "done", ""}}
	read := func(query string, args ...any) []string {
		t.Helper()
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var row managementPageModelRecord
			if err := rows.Scan(&row.account, &row.group, &row.model, &row.state, &row.message, &row.result); err != nil {
				t.Fatal(err)
			}
			if row.model == "gpt-5.5" || row.model == checkModel {
				out = append(out, mustJSON([]any{row.account, row.group, row.model, row.state, row.message, json.RawMessage(row.result)}))
			} else {
				t.Fatal("foreign or off-scope result exposed")
			}
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		sort.Strings(out)
		return out
	}
	wanted := []subBoundKey{{AccountID: "a", GroupID: 1}, {AccountID: "foreign", GroupID: 1}}
	baseline := read(managementPageModelsSQL+` WHERE m.model=ANY($3::text[])`, "owner", mustJSON(wanted), []string{"gpt-5.5", checkModel})
	got := read(managementPageSelectedModelsSQL, "owner", mustJSON(metadata))
	if len(got) != 2 || !reflect.DeepEqual(got, baseline) {
		t.Fatal("missing summary or JSON null changed", got, baseline)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET result='{"checked_at":987}' WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read(managementPageSelectedModelsSQL, "owner", mustJSON(metadata)), baseline) {
		t.Fatal("display crossed its repeatable-read snapshot")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := readManagementDisplayModelStream(canceled, tx, "owner", wanted, nil, "gpt-5.5", func(managementPageModelRecord, *managementRowsTrace) error { return nil }); err == nil {
		t.Fatal("canceled read returned a complete projection")
	}
}

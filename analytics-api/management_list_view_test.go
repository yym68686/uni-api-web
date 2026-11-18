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
	"strings"
	"testing"
	"time"
)

func TestManagementGroupOverviewCacheAndDirtyFence(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	ctx := context.Background()
	owner, n, err := warmManagementOverviewBatch(ctx, db, "")
	if err != nil || owner != "owner" || n == 0 {
		t.Fatalf("group overview warm failed: owner=%q groups=%d err=%v", owner, n, err)
	}
	wanted := []subBoundKey{{AccountID: "a", GroupID: 1}}
	read := func() map[string]managementPageGroupOverview {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		got, err := readManagementPageOverview(ctx, tx, "owner", wanted)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	key := qualityGroupID("a", 1)
	if got := read()[key]; got.counts.Success != 3 || len(got.busy) != 0 {
		t.Fatalf("cached summary mismatch: %+v", got)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := readStoredManagementOverviews(ctx, tx, "owner", wanted, managementOverviewCatalog(managementUnique(subModels)))
	tx.Rollback()
	if err != nil || len(stored) != 1 || !reflect.DeepEqual(stored, read()) {
		t.Fatalf("did not use the stored group: %+v err=%v", stored, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE console_sub_models SET state='running' WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if got := read()[key]; got.counts.Pending != 1 || len(got.busy) != 1 || got.busy[0].Model != "gpt-5.5" {
		t.Fatalf("newer model write was hidden by cache: %+v", got)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES('a',1,'check','queued')`); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got) != 0 {
		t.Fatalf("queued group used a stale cached summary: %+v", got)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM console_sub_check_queue WHERE account_id='a' AND group_id=1`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := warmManagementOverviewBatch(ctx, db, ""); err != nil {
		t.Fatal(err)
	}
	if got := read()[key]; got.counts.Pending != 1 || len(got.busy) != 1 {
		t.Fatalf("queue removal did not rebuild its group: %+v", got)
	}
}

func TestManagementGroupOverviewWarmKeepsConcurrentDirtyWrite(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := warmManagementOverviewBatch(ctx, db, ""); err != nil {
		t.Fatal(err)
	}
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err = blocker.ExecContext(ctx, `SELECT 1 FROM console_management_group_overview WHERE account_id='a' AND group_id=1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	setState := func(state string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `UPDATE console_sub_models SET state=$1 WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`, state); err != nil {
			t.Fatal(err)
		}
	}
	setState("running")
	done := make(chan error, 1)
	go func() {
		_, _, err := warmManagementOverviewBatch(ctx, db, "")
		done <- err
	}()
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE wait_event_type='Lock' AND query LIKE 'INSERT INTO console_management_group_overview%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("builder exited without waiting on the fixture: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	setState("interrupted")
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	wanted := []subBoundKey{{AccountID: "a", GroupID: 1}}
	stored, err := readStoredManagementOverviews(ctx, tx, "owner", wanted, managementOverviewCatalog(managementUnique(subModels)))
	if err != nil || len(stored) != 0 {
		t.Fatalf("builder cleared a concurrent writer: %+v err=%v", stored, err)
	}
	got, err := readManagementPageOverview(ctx, tx, "owner", wanted)
	if err != nil || got[qualityGroupID("a", 1)].counts.Interrupted != 1 {
		t.Fatalf("foreground did not use current counts: %+v err=%v", got, err)
	}
}

func TestManagementOverviewMatchesListAcrossPages(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET state='running' WHERE group_id=25 AND model='gpt-5.5';
		UPDATE console_sub_models SET result=jsonb_set(result,'{availability,terminal_status}','"missing"') WHERE group_id=51 AND model='gpt-5.5';
		UPDATE console_sub_models SET state='interrupted' WHERE group_id=61 AND model='extra-model';
		UPDATE console_sub_models SET state='error',result=jsonb_set(result,'{availability,terminal_status}','"missing"') WHERE group_id=5 AND model='gpt-5.5';
		INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES ('a',26,'check','new-queued-model');
		UPDATE console_sub_targets t SET result=m.result FROM console_sub_models m WHERE t.account_id=m.account_id AND t.group_id=m.group_id AND m.group_id=59 AND m.model='gpt-6-astra';
		DELETE FROM console_sub_models WHERE group_id IN (59,60)`); err != nil {
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
	if got, err := readManagementPageOverview(ctx, db, "owner", wanted); err != nil || len(got) != 2 ||
		got[qualityGroupID("a", 1)].counts.Success != 3 || got[qualityGroupID("a", 2)].counts.Success != 3 {
		t.Fatalf("wrong grouped overview or ownership: groups=%d err=%v", len(got), err)
	}
	for _, change := range []string{
		`UPDATE console_management_model_signal SET valid=false WHERE account_id='a' AND group_id=2 AND model='gpt-5.5'`,
		`DELETE FROM console_management_model_signal WHERE account_id='a' AND group_id=2 AND model='gpt-5.5'`,
	} {
		if _, err := db.Exec(change); err != nil {
			t.Fatal(err)
		}
		if got, err := readManagementPageOverview(ctx, db, "owner", wanted); err != nil || len(got) != 1 || got[qualityGroupID("a", 1)].counts.Success != 3 {
			t.Fatalf("missing or invalid signal group was not isolated: groups=%d err=%v", len(got), err)
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

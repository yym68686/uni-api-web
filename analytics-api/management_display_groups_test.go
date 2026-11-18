package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestManagementDisplayGroupsAllPagesSurviveProcessReplacement(t *testing.T) {
	ctx := context.Background()
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	// The warmer and production use the same storage budget guard.
	sweeper := &Service{control: s.control, engine: s.engine}
	last, groups := subBoundKey{}, 0
	for i := 0; i < 9; i++ {
		next, n, stored, err := sweeper.warmManagementDisplayGroups(ctx, "owner", last)
		if err != nil || n > 0 && !stored {
			t.Fatalf("warm batch: n=%d stored=%t: %v", n, stored, err)
		}
		groups += n
		last = next
	}
	if groups != 61 {
		t.Fatalf("wanted 61 group summaries, got %d", groups)
	}
	f := managementQueryFilters{GroupStatus: "all", Sort: "desc"}
	copyService := &Service{control: s.control, engine: s.engine}
	compare := func(firstCursor string) {
		t.Helper()
		pages := 0
		for cursor := firstCursor; ; {
			// Diagnostics bypass the derived storage; every page must have
			// exactly the same display results as a complete live read.
			legacy := context.WithValue(ctx, managementReceiptPlanKey{}, &managementReceiptPlanRequest{})
			want, err := s.readManagementQuerySnapshot(legacy, "owner", f, cursor, false)
			if err != nil {
				t.Fatal(err)
			}
			got, err := copyService.readManagementQuerySnapshot(ctx, "owner", f, cursor, false)
			got.Page.ReadAtMS = want.Page.ReadAtMS
			if err != nil || !reflect.DeepEqual(want.Page, got.Page) || !reflect.DeepEqual(
				managementContextForPage(want.View, want.Page, ""), managementContextForPage(got.View, got.Page, "")) {
				t.Fatalf("page %d differs from original: %v", pages, err)
			}
			pages++
			cursor = got.Page.NextCursor
			if cursor == "" {
				break
			}
		}
		if pages != 3 {
			t.Fatal("not all pages were compared", pages)
		}
	}
	compare("")

	// Wrong proof, wrong owner and a durable payload with invalid gzip must
	// never count as a hit. The real paginator must still render correctly.
	read := func(owner string, group int64, unproved bool) bool {
		t.Helper()
		tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		p, _, err := s.managementReadProof(ctx, tx, owner)
		if err != nil {
			t.Fatal(err)
		}
		if unproved {
			p.DisplayVersion = ""
		}
		found, err := readManagementDisplayGroups(withManagementDisplayVersion(ctx, tx, owner, p.DisplayVersion), tx, owner, []subBoundKey{{AccountID: "a", GroupID: group}})
		if err != nil {
			t.Fatal(err)
		}
		return len(found) == 1
	}
	if !read("owner", 26, false) || read("different-owner", 26, false) || read("owner", 26, true) {
		t.Fatal("invalid projection authorization/version")
	}
	if _, err := s.control.db.Exec(`UPDATE console_management_display_groups SET body='x'::bytea WHERE group_id=26`); err != nil {
		t.Fatal(err)
	}
	if read("owner", 26, false) {
		t.Fatal("corrupt projection accepted")
	}
	compare("")
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET state='queued' WHERE group_id=26 AND model='gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if !read("owner", 25, false) || read("owner", 26, false) {
		t.Fatal("one changed group invalidated its neighbors or reused its prior model")
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"input_price":9}' WHERE id='a-27-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if !read("owner", 25, false) || read("owner", 27, false) {
		t.Fatal("receipt mutation reused the changed group or lost unrelated groups")
	}
	compare("")

	// Cache ownership is derived from live account/target rows at write time.
	if _, err := s.control.db.Exec("UPDATE " + pgx.Identifier{s.engine.historyPath, "storage_state"}.Sanitize() + " SET ingest_limit_bytes=1"); err != nil {
		t.Fatal(err)
	}
	if _, n, stored, err := sweeper.warmManagementDisplayGroups(ctx, "owner", subBoundKey{}); err != nil || n == 0 || stored {
		t.Fatal("cache wrote through storage threshold", n, stored, err)
	}
}

func TestManagementDisplayGroupPackHasNoPrivateProbeData(t *testing.T) {
	models := []subModelResult{{Model: "m", State: "done", Result: &subResult{Availability: subProbe{
		ID: "private-id", Text: "private-text", CurlToken: "private-token",
		RequestIDs: []string{"private-request"}, Message: strings.Repeat("多", 300),
		Usage: &subUsage{Status: "matched", InputPrice: usageFloat(0), CheckedAt: 123},
	}}}}
	normalizeManagementModels(models)
	pack, ok := packManagementDisplayGroup(subBoundKey{AccountID: "a", GroupID: 1}, models)
	if !ok {
		t.Fatal("group was not packed")
	}
	got, ok := pack.models(context.Background())
	if !ok || !reflect.DeepEqual(models, got) || strings.Contains(mustJSON(got), "private-") {
		t.Fatal("summary content changed or retained private evidence")
	}
	cancelled, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, ok := pack.models(cancelled); ok {
		t.Fatal("canceled query published decoded results")
	}
	pack.bound.GroupID++
	if _, ok := pack.models(context.Background()); ok {
		t.Fatal("group payload accepted for another group")
	}
}

func TestManagementDisplayCrossGroupReceiptRetainsGlobalProof(t *testing.T) {
	ctx := context.Background()
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET group_id=2 WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if _, n, stored, err := s.warmManagementDisplayGroups(ctx, "owner", subBoundKey{}); err != nil || n == 0 || !stored {
		t.Fatal(n, stored, err)
	}
	var proof string
	if err := s.control.db.QueryRow(`SELECT local_version FROM console_management_display_groups WHERE group_id=1`).Scan(&proof); err != nil || proof != "" {
		t.Fatal("cross-group receipt got a local proof", err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"input_price":19}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	p, _, err := s.managementReadProof(ctx, tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	found, err := readManagementDisplayGroups(withManagementDisplayVersion(ctx, tx, "owner", p.DisplayVersion), tx, "owner", []subBoundKey{{AccountID: "a", GroupID: 1}, {AccountID: "a", GroupID: 3}})
	if _, bad := found[qualityGroupID("a", 1)]; err != nil || bad || len(found) != 1 {
		t.Fatal("cross-group change reused stale evidence or evicted an unrelated group", err)
	}
}

func TestManagementDisplayPageBudget(t *testing.T) {
	if os.Getenv("RUN_MANAGEMENT_PAGE_BUDGET") != "1" {
		t.Skip("opt-in isolated 49-page API fixture; not a production browser measurement")
	}
	ctx := context.Background()
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,active,state)
 SELECT 'a',g,'Q'||g,'openai',g,true,'done' FROM generate_series(62,1215) g;
 INSERT INTO console_sub_models(account_id,group_id,model,state,message,result)
 SELECT 'a',g,m,'done','model message',jsonb_build_object('model',m,'checked_at',g,'verdict','pass',
 'availability',jsonb_build_object('id','a-'||g||'-'||m,'status','success','model_match','match','response_model',m))
 FROM generate_series(62,1215) g CROSS JOIN unnest(ARRAY['gpt-5.5','gpt-6-astra','extra-model']) m;
 INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result)
 SELECT 'a-'||g||'-'||m,'a',g,g,m,0,'[]','matched','{"input_price":2,"output_price":3}'
 FROM generate_series(62,1215) g CROSS JOIN unnest(ARRAY['gpt-5.5','gpt-6-astra','extra-model']) m;`); err != nil {
		t.Fatal(err)
	}
	prepareManagementRevisionsForTest(t, s.control.db)
	session, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	f := managementQueryFilters{GroupStatus: "all", Sort: "desc"}
	run := func(name string) {
		t.Helper()
		// Empty process-local caches at the start of each walk.
		live := &Service{control: s.control, engine: s.engine}
		handler := live.Handler()
		maxLatency := time.Duration(0)
		read := func(offset int) {
			t.Helper()
			v := url.Values{"filters": {mustJSON(f)}}
			if offset != 0 {
				v.Set("cursor", encodeManagementCursor(s.control.key, "owner", managementPageCursor{Version: 1, Offset: offset, Revision: "earlier-read", Filter: tokenHash(mustJSON(f))}))
				v.Set("refresh_cursor", "1")
			}
			r := httptest.NewRequest("GET", "/v1/channel-management/query?"+v.Encode(), nil)
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
			w := httptest.NewRecorder()
			start := time.Now()
			handler.ServeHTTP(w, r)
			elapsed := time.Since(start)
			maxLatency = max(maxLatency, elapsed)
			var page managementPageResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil ||
				page.Offset != offset || page.Total != 1215 || len(page.Rows) != min(25, 1215-offset) || page.SelectionToken == "" {
				t.Fatalf("%s page %d incomplete: status=%d total=%d offset=%d", name, offset/25+1, w.Code, page.Total, page.Offset)
			}
			if elapsed >= time.Second {
				t.Errorf("%s page %d exceeded 1s: %v", name, offset/25+1, elapsed)
			}
		}
		read(1200) // deep page as first request
		for offset := 0; offset < 1215; offset += 25 {
			read(offset)
		}
		read(1175) // back from final page
		t.Logf("%s 51 authenticated API reads: max=%s", name, maxLatency)
	}
	run("unprepared-display")
	for after := (subBoundKey{}); ; {
		next, n, stored, err := s.warmManagementDisplayGroups(ctx, "owner", after)
		if err != nil || n > 0 && !stored {
			t.Fatal("warm fixture", n, stored, err)
		}
		if next == (subBoundKey{}) {
			break
		}
		after = next
	}
	run("persistent-display")
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"input_price":19}' WHERE id='a-1210-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	run("updated-receipt")
}

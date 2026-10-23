package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestManagementComputedPageRequiresExactCurrentTransaction(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	prepareManagementRevisionsForTest(t, s.control.db)
	filters := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	key := managementComputedKey("owner", filters, "")
	remember := func() managementPageResponse {
		t.Helper()
		snapshot, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
		if err != nil {
			t.Fatal(err)
		}
		page := managementPageResponse{managementQueryPage: snapshot.Page, Accounts: managementAccountHeaders(snapshot.View.Input.Accounts), Context: managementContextForPage(snapshot.View, snapshot.Page, filters.Model)}
		s.rememberManagementPage(key, snapshot.Proof, page)
		return page
	}
	original := remember()
	page, ok, err := s.currentComputedManagementPage(ctx, "owner", key)
	if err != nil || !ok {
		t.Fatal("verified current computation unavailable", err)
	}
	page.ReadAtMS = original.ReadAtMS
	// JSON cloning preserves the HTTP contract. time.Time can retain distinct
	// internal Location pointers for the same UTC instant under TZ=UTC.
	if mustJSON(page) != mustJSON(original) {
		t.Fatal("retained computation changed complete wire values")
	}

	if _, ok, err := s.currentComputedManagementPage(ctx, "foreign", managementComputedKey("foreign", filters, "")); err != nil || ok {
		t.Fatal("owner computation leaked")
	}
	queries := []string{
		`UPDATE console_sub_accounts SET name='changed' WHERE id='a'`,
		`UPDATE console_sub_targets SET encrypted_key='changed' WHERE account_id='a' AND group_id=1`,
		`UPDATE console_sub_models SET message='changed' WHERE model='extra-model'`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{input_price}','42') WHERE id='q-61-gpt-5.5'`,
		`INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES('a',1,'model','gpt-5.5')`,
	}
	for _, query := range queries {
		if _, err := s.control.db.Exec(query); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.currentComputedManagementPage(ctx, "owner", key); err != nil || ok {
			t.Fatal("changed snapshot reused", query, err)
		}
		remember()
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET usage_lease_token='poll-clock',usage_lease_until=now();UPDATE console_sub_usage SET result=jsonb_set(result,'{checked_at}','12345') WHERE id='q-61-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.currentComputedManagementPage(ctx, "owner", key); err != nil || !ok {
		t.Fatal("nonpresentation clocks invalidated computation", err)
	}
	var headersBefore, headersAfter, channelsBefore, channelsAfter int64
	if err := s.control.db.QueryRow(`SELECT revision FROM console_management_view_epoch WHERE relation_name='console_sub_account_headers'`).Scan(&headersBefore); err != nil {
		t.Fatal(err)
	}
	if err := s.control.db.QueryRow(`SELECT revision FROM console_management_view_epoch WHERE relation_name='console_sub_accounts'`).Scan(&channelsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET balance='{"amount":123.45,"checked_at":99,"status":"ok"}',encrypted_auth='renewed-session' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	balanced, ok, err := s.currentComputedManagementPage(ctx, "owner", key)
	if err != nil || !ok || balanced.Accounts[0].Balance == nil || *balanced.Accounts[0].Balance.Amount != 123.45 || *balanced.Rows[0].Account.Balance.Amount != 123.45 {
		t.Fatal("current balances not overlaid", err)
	}
	if err := s.control.db.QueryRow(`SELECT revision FROM console_management_view_epoch WHERE relation_name='console_sub_account_headers'`).Scan(&headersAfter); err != nil {
		t.Fatal(err)
	}
	if err := s.control.db.QueryRow(`SELECT revision FROM console_management_view_epoch WHERE relation_name='console_sub_accounts'`).Scan(&channelsAfter); err != nil {
		t.Fatal(err)
	}
	if headersAfter == headersBefore || channelsAfter != channelsBefore {
		t.Fatal("old reader invalidation or current channel epoch changed")
	}
	full, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", true)
	if err != nil || full.Page.Revision != balanced.Revision {
		t.Fatal("balance invalidated full operation revision", err)
	}
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE console_sub_targets SET active=false`); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	if _, ok, err := s.currentComputedManagementPage(ctx, "owner", key); err != nil || !ok {
		t.Fatal("rollback invalidated computation", err)
	}
	s.managementComputed.mu.Lock()
	entry := s.managementComputed.pages[key]
	entry.Proof.Until = time.Now().Add(-time.Second)
	s.managementComputed.pages[key] = entry
	s.managementComputed.mu.Unlock()
	if _, ok, err := s.currentComputedManagementPage(ctx, "owner", key); err != nil || ok {
		t.Fatal("elapsed deadline reused", err)
	}
	remember()
	table := pgx.Identifier{s.engine.historyPath, "prices"}.Sanitize()
	if _, err = s.control.db.Exec(`INSERT INTO ` + table + `(model,input,output,source,verified,effective_at) VALUES('new-model',1,2,'manual',true,'2026-10-24')`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.currentComputedManagementPage(ctx, "owner", key); err != nil || ok {
		t.Fatal("price update reused", err)
	}
}

func TestManagementComputedHTTPIsFreshAndFilterBounded(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	prepareManagementRevisionsForTest(t, s.control.db)
	token, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	call := func(filters string) managementPageResponse {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/channel-management/query?"+url.Values{"filters": {filters}}.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("current HTTP read failed", w.Code)
		}
		var p managementPageResponse
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	first := call(`{"groupStatus":"all","model":"gpt-5.5"}`)
	second := call(`{"groupStatus":"all","model":"gpt-5.5"}`)
	if second.ReadAtMS < first.ReadAtMS || first.Revision != second.Revision || !reflect.DeepEqual(first.Rows, second.Rows) {
		t.Fatal("current repeat changed values")
	}
	if _, err = s.control.db.Exec(`UPDATE console_sub_targets SET name='new committed name' WHERE group_id=1`); err != nil {
		t.Fatal(err)
	}
	changed := call(`{"groupStatus":"all","model":"gpt-5.5"}`)
	if changed.Revision == first.Revision || changed.Rows[0].Target.Name != "new committed name" {
		t.Fatal("committed data not returned immediately")
	}
	filtered := call(`{"groupStatus":"all","model":"gpt-5.5","search":"new committed name"}`)
	if filtered.Total != 1 {
		t.Fatal("another filter reused", filtered.Total)
	}
	for i := 0; i < 6; i++ {
		s.rememberManagementPage(string(rune('a'+i)), &managementFreshProof{Stamp: "s"}, managementPageResponse{managementQueryPage: managementQueryPage{Rows: []managementQueryRow{}}})
	}
	if len(s.managementComputed.pages) > 4 {
		t.Fatal("unbounded computed pages")
	}
}

func TestManagementComputedPageStillChecksGatewayPermissionAndRevision(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	prepareManagementRevisionsForTest(t, s.control.db)
	mode := "stable"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "denied" {
			http.Error(w, "denied", 403)
			return
		}
		rev := "r1"
		if mode == "changed" {
			rev = "r2"
		}
		writeJSON(w, 200, map[string]any{"format": "management-revision-v1", "revision": rev, "config_revision": "c1"})
	}))
	defer server.Close()
	source := controlSource{sourceView: sourceView{ID: "s", Name: "S", Base: server.URL}, Key: "observer", ConfigKey: "admin"}
	if _, err := s.control.saveSource(ctx, source, false); err != nil {
		t.Fatal(err)
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	proof, _, err := s.managementReadProof(ctx, tx, "owner")
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	proof.Facts = []managementGatewayFacts{{Source: source, AtomicSnapshot: true, Revision: "r1", ConfigRevision: "c1"}}
	s.rememberManagementPage("key", &proof, managementPageResponse{managementQueryPage: managementQueryPage{Format: "management-page-v1", Rows: []managementQueryRow{}}})
	if _, ok, err := s.currentComputedManagementPage(ctx, "owner", "key"); err != nil || !ok {
		t.Fatal(err)
	}
	mode = "changed"
	if _, ok, err := s.currentComputedManagementPage(ctx, "owner", "key"); err != nil || ok {
		t.Fatal("changed gateway reused", err)
	}
	mode = "denied"
	if _, ok, err := s.currentComputedManagementPage(ctx, "owner", "key"); err == nil || ok {
		t.Fatal("revoked permission reused")
	}
}

func TestManagementComputedProofChangesInSourceTransaction(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	prepareManagementRevisionsForTest(t, s.control.db)
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	old, _, err := s.managementReadProof(ctx, tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET active=false WHERE group_id=1`); err != nil {
		t.Fatal(err)
	}
	stable, _, err := s.managementReadProof(ctx, tx, "owner")
	if err != nil || stable.Stamp != old.Stamp {
		t.Fatal("snapshot mixed", err)
	}
	fresh, _, err := s.managementReadProof(ctx, s.control.db, "owner")
	if err != nil || fresh.Stamp == old.Stamp {
		t.Fatal("write not atomically visible", err)
	}
	raw, _ := json.Marshal(stable)
	if len(raw) == 0 {
		t.Fatal("missing proof")
	}
}

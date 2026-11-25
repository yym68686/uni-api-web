package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestManagementTierSignalRepairRestoresColdAvailabilityPage(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	ctx := context.Background()
	prepareManagementRevisionsForTest(t, db)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Unix()
	exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{service_tiers}',jsonb_build_object(
 'fast',jsonb_build_object('status','supported','observed','priority','checked_at',$1::bigint),
 'ultrafast',jsonb_build_object('status','not_honored','observed','default','checked_at',$1::bigint)))
 WHERE group_id IN (1,2) AND model='gpt-5.5'`, now)
	ready := func(want bool) {
		t.Helper()
		if _, got, err := readManagementCompactRevision(ctx, db, "owner"); err != nil || got != want {
			t.Fatalf("compact proof ready=%v, want %v: %v", got, want, err)
		}
	}
	ready(true) // New probe writes must never disable the compact proof.
	before := func() string {
		t.Helper()
		var result string
		if err := db.QueryRow(`SELECT result::text FROM console_sub_models WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}()
	exec(`UPDATE console_management_model_signal SET valid=false, availability='', checked_at=0, service_tiers=NULL
 WHERE group_id IN (1,2) AND model='gpt-5.5'`)
	ready(false)
	if n, err := repairManagementTierSignals(ctx, db); err != nil || n != 2 {
		t.Fatal("legacy tier rows not repaired", n, err)
	}
	if n, err := repairManagementTierSignals(ctx, db); err != nil || n != 0 {
		t.Fatal("repair was not idempotent", n, err)
	}
	ready(true)
	var after string
	if err := db.QueryRow(`SELECT result::text FROM console_sub_models WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`).Scan(&after); err != nil || after != before {
		t.Fatal("repair modified the source record", err)
	}

	session, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	writer := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(writer)
	filters := managementQueryFilters{GroupStatus: "all", Availability: "success"}
	req := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query?"+url.Values{"filters": {mustJSON(filters)}}.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
	req.Header.Set("Accept", managementPageV3MediaType)
	req.Header.Set(managementListViewHeader, managementListViewV1)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	output := logs.String()
	if w.Code != http.StatusOK || !strings.Contains(output, "stage=database_availability_groups ") ||
		!strings.Contains(output, "compact_revision=true catalog_only=true ") ||
		strings.Contains(output, "stage=account_receipts query_ready") || strings.Contains(output, "stage=receipt_signal_rows query_ready") {
		t.Fatalf("cold page did not use scoped availability: status=%d\n%s", w.Code, output)
	}

	// A supported tier is actually a predicate, including when no model
	// filter is set. Match the complete signed selection in both cases.
	for _, model := range []string{"", "gpt-5.5"} {
		f := managementQueryFilters{GroupStatus: "all", Availability: "success", Model: model, Fast: "supported", Ultrafast: "not_honored"}
		light := context.WithValue(ctx, managementListViewKey{}, true)
		page, err := s.readManagementQuerySnapshot(light, "owner", f, "", false)
		if err != nil || page.Page.Total != 2 {
			t.Fatal("tier filter lost matching rows", model, page.Page.Total, err)
		}
		token := encodeManagementCursor(s.control.key, "owner", managementPageCursor{Version: 1, Revision: page.Page.Revision, Filter: tokenHash(mustJSON(f))})
		full, err := s.readManagementQuerySnapshot(ctx, "owner", f, token, true)
		if err != nil || full.Page.Total != 2 || full.Page.Rows[0].ID != page.Page.Rows[0].ID {
			t.Fatal("tier filter page and full selection disagree", model, full.Page.Total, err)
		}
	}
}

func TestManagementTierSignalsRejectMalformedValuesAndClearOnCredentialChange(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	db := s.control.db
	prepareManagementRevisionsForTest(t, db)
	exec := func(query string) {
		t.Helper()
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE console_sub_models SET result=result||'{"service_tiers":{"fast":{"status":"supported","checked_at":"not-an-int"}}}'
 WHERE group_id=1 AND model='gpt-5.5'`)
	if n, err := repairManagementTierSignals(ctx, db); err != nil || n != 0 {
		t.Fatal("repair must not admit invalid evidence", n, err)
	}
	if _, _, err := readManagementSignalStreams(ctx, db, "owner", true); !errors.Is(err, errManagementPredicateFallback) {
		t.Fatal("malformed tier result bypassed the typed fallback", err)
	}
	exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{service_tiers,fast,checked_at}','123') WHERE group_id=1 AND model='gpt-5.5';
 UPDATE console_sub_targets SET remote_key_id=999 WHERE group_id=1 AND account_id='a'`)
	var valid, cleared bool
	if err := db.QueryRow(`SELECT valid,service_tiers IS NULL FROM console_management_model_signal WHERE group_id=1 AND model='gpt-5.5'`).Scan(&valid, &cleared); err != nil || !valid || !cleared {
		t.Fatal("rotated credential kept tier evidence", valid, cleared, err)
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSiteRateSettingsOwnerValidationPersistenceAndViewInvalidation(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	ctx := context.Background()
	token, _ := s.control.newSession(ctx, owner)
	foreign, _ := s.control.newSession(ctx, owner+"-other")
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET state='running',message='unchanged',sync_models='["gpt-6-astra"]' WHERE id=$1;
 `, account); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,billing) VALUES($1,7,'group','openai','{"rate":0.3}')`, account); err != nil {
		t.Fatal(err)
	}
	request := func(cookie, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PATCH", "/v1/sub2api/accounts/"+account+"/settings", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		cookie, body string
		status       int
	}{
		{"", `{"rate_coefficient":0.1}`, 401}, {foreign, `{"rate_coefficient":0.1}`, 404},
		{token, `{}`, 400}, {token, `{"rate_coefficient":null}`, 400}, {token, `{"rate_coefficient":0}`, 400},
		{token, `{"rate_coefficient":-1}`, 400}, {token, `{"rate_coefficient":1000001}`, 400},
		{token, `{"rate_coefficient":"0.1"}`, 400}, {token, `{"rate_coefficient":1e999}`, 400},
	} {
		if w := request(tc.cookie, tc.body); w.Code != tc.status {
			t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
		}
	}
	epoch := func() int64 {
		var n int64
		if err := s.control.db.QueryRow(`SELECT revision FROM console_management_view_epoch WHERE relation_name='console_sub_accounts'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := epoch()
	if w := request(token, `{"rate_coefficient":0.1}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if epoch() <= before {
		t.Fatal("coefficient change did not invalidate cached management view")
	}
	before = epoch()
	if w := request(token, `{"rate_coefficient":0.1}`); w.Code != 200 || epoch() != before {
		t.Fatal("identical setting should preserve epoch", w.Code)
	}
	accounts, err := readSubAccountHeaders(ctx, s.control.db, owner, account)
	if err != nil || len(accounts) != 1 || accounts[0].RateCoefficient != .1 || accounts[0].State != "running" || accounts[0].Message != "unchanged" || len(accounts[0].SyncModels) != 1 {
		t.Fatal("setting changed sync state", accounts, err)
	}
	if err = s.readSubAccountDetailsFrom(ctx, s.control.db, owner, account, 7, "", false, true, accounts, nil); err != nil {
		t.Fatal(err)
	}
	target := accounts[0].Targets[0]
	if target.RateCoefficient != .1 || *target.Billing.Rate != .3 {
		t.Fatal("raw evidence changed or factor omitted", target)
	}
	refs, err := s.control.subChannelRefsForAccount(ctx, owner, account)
	if err != nil || len(refs) != 1 || refs[0].Name != "fixture-0.03" || *refs[0].Rate != .03 {
		t.Fatal("display label/rate not converted", refs, err)
	}
	rate := managementBillingRate(managementQueryRow{Target: target}, "")
	if rate.Rate == nil || *rate.Rate != .03 {
		t.Fatal("management rate not converted", rate)
	}
	for _, factor := range []string{"1", "0.25"} {
		if w := request(token, `{"rate_coefficient":`+factor+`}`); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	var raw float64
	s.control.db.QueryRow(`SELECT (billing->>'rate')::float8 FROM console_sub_targets WHERE account_id=$1`, account).Scan(&raw)
	if raw != .3 {
		t.Fatal("settings rewrote upstream bill", raw)
	}
}

func TestSiteRateSpendConvertsEachAccountBeforeSumAndKeepsRawReceipts(t *testing.T) {
	s, account, owner := attributionFixture(t)
	second := "second-" + randomID()[:12]
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth,rate_coefficient) SELECT $2,owner,'second','https://other.example',email,encrypted_auth,0.5 FROM console_sub_accounts WHERE id=$1`, account, second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, second) })
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET rate_coefficient=0.1 WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES($1,$2,42,7)`, second, tokenHash("business-secret")); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	a := attributedFact("scaled-one", "caller", now.Add(-time.Minute))
	b := attributedFact("scaled-two", "caller", now.Add(-time.Minute))
	b.UpstreamBase = "https://other.example"
	storeAttributedLog(t, s, account, a.EventID, "0.1", 1)
	storeAttributedLog(t, s, second, b.EventID, "0.2", 1)
	if err := s.engine.Import(context.Background(), "scaled", "v1", []Fact{a, b}); err != nil {
		t.Fatal(err)
	}
	read := func() attributedSpend {
		t.Helper()
		rows, err := s.attributedChannelSpend(context.Background(), owner, QueryFilter{KeyID: "caller"}, now.Add(-time.Hour).Unix(), now.Unix())
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		return rows[0]
	}
	for range 2 {
		out := read()
		if out.Status != "complete" || out.Amount == nil || *out.Amount != .11 || out.MatchedAmount != .11 || out.Matched != 2 || out.RateCoefficients[account] != .1 || out.RateCoefficients[second] != .5 {
			t.Fatalf("incorrect mixed-account conversion %+v", out)
		}
	}
	missing := attributedFact("missing-id", "caller", now.Add(-time.Minute))
	missing.BillingRequestIDs = nil
	if err := s.engine.Import(context.Background(), "missing", "v1", []Fact{missing}); err != nil {
		t.Fatal(err)
	}
	if out := read(); out.Amount != nil || out.MatchedAmount != .11 || out.Status != "unmatched" {
		t.Fatalf("partial cost conversion changed unknown semantics %+v", out)
	}
	var raw string
	if err := s.control.db.QueryRow(`SELECT actual_cost::text FROM console_sub_spend_logs WHERE account_id=$1`, account).Scan(&raw); err != nil {
		t.Fatal("raw receipt was converted", raw, err)
	}
	if amount, err := strconv.ParseFloat(raw, 64); err != nil || amount != .1 {
		t.Fatal("raw receipt changed", raw, err)
	}
}

func TestSiteRateBusinessKeyTotalsUseCurrentCoefficientWithoutChangingLedger(t *testing.T) {
	s, account, _, request := spendTestService(t)
	to := time.Now().Truncate(time.Second)
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"data": subSpendPage{Items: []subSpendLog{spendLog(1, to.Add(-time.Minute), "0.3")}, Page: 1, Pages: 1, PageSize: 100}})
	}))
	request("1h", to)
	runSpendBatch(t, s, account)
	for _, factor := range []float64{.1, 1, .25} {
		if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET rate_coefficient=$2 WHERE id=$1`, account, factor); err != nil {
			t.Fatal(err)
		}
		out := request("1h", to)
		if out.Amount == nil || *out.Amount != adjustedSiteAmount(.3, factor) || out.RateCoefficient != factor {
			raw, _ := json.Marshal(out)
			t.Fatal(string(raw))
		}
	}
}

func TestSiteRateManagementFilterUsesConvertedRates(t *testing.T) {
	rows := []managementQueryRow{
		{ID: "scaled", Target: subTarget{RateCoefficient: .1, Billing: &subBilling{Rate: usageFloat(.2)}, ObservedRates: map[string]subObservedRate{"m": {Rate: .3}}}},
		{ID: "raw", Target: subTarget{Billing: &subBilling{Rate: usageFloat(.1)}}},
	}
	out := filterManagementQueryRows(rows, managementQueryFilters{GroupStatus: "all", Model: "m", MaxRate: "0.03", Sort: "asc"}, managementFilterEvidence{})
	if len(out.Rows) != 1 || out.Rows[0].ID != "scaled" || len(out.Rates) != 2 || out.Rates[0] != .03 || out.Rates[1] != .1 {
		t.Fatal(out)
	}
	if got := managementBillingRate(rows[0], "m"); !got.Mismatch || *got.Rate != .03 {
		t.Fatal(got)
	}
	if *rows[0].Target.Billing.Rate != .2 || rows[0].Target.ObservedRates["m"].Rate != .3 {
		t.Fatal("display conversion mutated evidence")
	}
}

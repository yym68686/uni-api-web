package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func attributionFixture(t *testing.T) (*Service, string, string) {
	s, account, owner := subUsageTestService(t)
	s.engine = stateTestEngine(t)
	_, e := s.control.db.Exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES($1,$2,42,7)`, account, tokenHash("business-secret"))
	if e != nil {
		t.Fatal(e)
	}
	return s, account, owner
}
func attributedFact(id, key string, at time.Time) Fact {
	return Fact{Schema: 1, SourceID: "source", InstanceID: "boot", EventID: id, RequestID: id, AttemptID: id + "-r1", Kind: "billing", AtMS: at.UnixMilli(), StartedMS: at.Add(-time.Minute).UnixMilli(), KeyID: key, Provider: "shared", Model: checkModel, UpstreamModel: checkModel, Endpoint: "/v1/responses", Stream: true, UpstreamBase: "https://usage.example", UpstreamKeyHash: tokenHash("business-secret"), BillingRequestIDs: []string{"client:" + id}}
}
func storeAttributedLog(t *testing.T, s *Service, account, id, cost string, n int64) {
	t.Helper()
	_, e := s.control.db.Exec(`INSERT INTO console_sub_spend_logs(account_id,key_id,log_id,at_ms,actual_cost,request_id,model) VALUES($1,42,$2,$3,$4::numeric,$5,$6)`, account, n, time.Now().UnixMilli(), cost, "client:"+id, checkModel)
	if e != nil {
		t.Fatal(e)
	}
}
func TestAttributedSpendSplitsSharedBusinessKeyAndPreservesReferenceMetrics(t *testing.T) {
	s, account, owner := attributionFixture(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	from, to := now.Add(-time.Hour).Unix(), now.Unix()
	facts := []Fact{attributedFact("a1", "caller-a", now.Add(-time.Minute)), attributedFact("a2", "caller-a", now.Add(-time.Minute)), attributedFact("b1", "caller-b", now.Add(-time.Minute)), attributedFact("other-model", "caller-a", now.Add(-time.Minute)), attributedFact("other-source", "caller-a", now.Add(-time.Minute))}
	facts[3].Model = "other-model"
	facts[4].SourceID = "other-source"
	for i, f := range facts {
		storeAttributedLog(t, s, account, f.EventID, []string{"0.1", "0.2", "9", "11", "13"}[i], int64(i+1))
	}
	if e := s.engine.Import(ctx, "receipt-facts", "v1", facts); e != nil {
		t.Fatal(e)
	}
	f := QueryFilter{SourceID: "source", KeyID: "caller-a", Model: checkModel, Endpoint: "/v1/responses", Stream: "true"}
	rows, e := s.attributedChannelSpend(ctx, owner, f, from, to)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 1 || rows[0].Status != "complete" || *rows[0].Amount != .3 || rows[0].Total != 2 || rows[0].Matched != 2 {
		t.Fatal(rows)
	}
	f.KeyID = "caller-b"
	rows, e = s.attributedChannelSpend(ctx, owner, f, from, to)
	if e != nil || len(rows) != 1 || *rows[0].Amount != 9 {
		t.Fatal(rows, e)
	}
	// Billing observations must not add requests/attempts/tokens to analytics.
	result, e := s.engine.Query(ctx, QueryFilter{Range: "all"})
	if e != nil || len(result.Data) != 0 {
		t.Fatal(result, e)
	}
	f.KeyID = "caller-a"
	rows, e = s.attributedChannelSpend(ctx, "foreign-owner", f, from, to)
	if e != nil || len(rows) != 1 || rows[0].Amount != nil {
		t.Fatal("foreign bill leak", rows, e)
	}
}
func TestAttributedSpendOldMissingDuplicateAndUnmatchedReceiptsRemainUnknown(t *testing.T) {
	for _, mode := range []string{"old", "header_missing", "receipt_missing", "duplicate", "duplicate_other_key", "duplicate_bill"} {
		t.Run(mode, func(t *testing.T) {
			s, account, owner := attributionFixture(t)
			ctx := context.Background()
			now := time.Now().Truncate(time.Second)
			fact := attributedFact("one", "caller", now.Add(-time.Minute))
			facts := []Fact{fact}
			storeAttributedLog(t, s, account, "one", "1.2", 1)
			switch mode {
			case "old":
				facts[0].Kind = "attempt"
				facts[0].BillingRequestIDs = nil
			case "header_missing":
				facts[0].BillingRequestIDs = nil
			case "receipt_missing":
				facts[0].BillingRequestIDs = []string{"client:no-such-receipt"}
			case "duplicate", "duplicate_other_key":
				duplicate := fact
				duplicate.EventID = "duplicate"
				if mode == "duplicate_other_key" {
					duplicate.KeyID = "other-caller"
					duplicate.SourceID = "other-source"
				}
				facts = append(facts, duplicate)
			case "duplicate_bill":
				storeAttributedLog(t, s, account, "one", "2.4", 2)
			}
			if e := s.engine.Import(ctx, "facts", "v1", facts); e != nil {
				t.Fatal(e)
			}
			rows, e := s.attributedChannelSpend(ctx, owner, QueryFilter{SourceID: "source", KeyID: "caller", Model: checkModel}, now.Add(-time.Hour).Unix(), now.Unix())
			if e != nil {
				t.Fatal(e)
			}
			if len(rows) != 1 || rows[0].Amount != nil || rows[0].Status == "complete" {
				t.Fatal("unproven zero or total shown", rows)
			}
			if mode == "receipt_missing" && rows[0].Status != "pending" {
				t.Fatal(rows)
			}
		})
	}
}
func TestAttributedSpendFetchesMissingReceiptWithoutAnyInferenceCall(t *testing.T) {
	s, account, owner := attributionFixture(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	fact := attributedFact("fetched", "caller", now.Add(-time.Minute))
	if e := s.engine.Import(ctx, "facts", "v1", []Fact{fact}); e != nil {
		t.Fatal(e)
	}
	f := QueryFilter{SourceID: "source", KeyID: "caller"}
	from, to := now.Add(-time.Hour).Unix(), now.Unix()
	rows, e := s.attributedChannelSpend(ctx, owner, f, from, to)
	if e != nil || rows[0].Status != "pending" {
		t.Fatal(rows, e)
	}
	old := subHTTP
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/usage" || r.URL.Query().Get("api_key_id") != "42" {
			t.Error("unexpected or billable request", r.Method, r.URL.Path)
		}
		log := spendLog(1, now.Add(-30*time.Second), "0.37")
		log.RequestID = "client:fetched"
		log.Model = checkModel
		writeJSON(w, 200, map[string]any{"code": 0, "data": subSpendPage{Items: []subSpendLog{log}, Page: 1, Pages: 1}})
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}}
	defer func() { subHTTP = old }()
	runSpendBatch(t, s, account)
	rows, e = s.attributedChannelSpend(ctx, owner, f, from, to)
	if e != nil || len(rows) != 1 || rows[0].Status != "complete" || *rows[0].Amount != .37 {
		t.Fatal(rows, e)
	}
	// Replay immutable facts and receipts without multiplying the charge.
	if e = s.engine.Import(ctx, "facts", "v1", []Fact{fact}); e != nil {
		t.Fatal(e)
	}
	rows, e = s.attributedChannelSpend(ctx, owner, f, from, to)
	if e != nil || *rows[0].Amount != .37 {
		t.Fatal(rows, e)
	}
}
func TestBillingFactCheckpointAndLegacyCompanion(t *testing.T) {
	s, account, owner := attributionFixture(t)
	now := time.Now().Truncate(time.Second)
	billing := attributedFact("one", "key", now.Add(-time.Minute))
	completion := billing
	completion.Kind = "attempt"
	completion.EventID = "completion"
	completion.BillingRequestIDs = nil
	completion.AtMS = now.Add(-time.Second).UnixMilli()
	completion.Outcome = "success"
	if e := s.engine.Import(context.Background(), "facts", "v1", []Fact{billing, completion}); e != nil {
		t.Fatal(e)
	}
	storeAttributedLog(t, s, account, "one", "0", 1)
	rows, e := s.attributedChannelSpend(context.Background(), owner, QueryFilter{SourceID: "source"}, now.Add(-time.Hour).Unix(), now.Unix())
	if e != nil || len(rows) != 1 || rows[0].Total != 1 || rows[0].Status != "complete" || *rows[0].Amount != 0 {
		t.Fatal(rows, e)
	}
	// Correlation lists survive export and fresh-engine restore.
	objects := &fakeStateObjects{}
	cp := newCheckpointStore(objects, Config{StateBucket: "state", SourceID: "source"})
	if e = cp.save(context.Background(), s.engine); e != nil {
		t.Fatal(e)
	}
	fresh := stateTestEngine(t)
	if ok, e := cp.restore(context.Background(), fresh); e != nil || !ok {
		t.Fatal(ok, e)
	}
	s.engine = fresh
	rows, e = s.attributedChannelSpend(context.Background(), owner, QueryFilter{SourceID: "source"}, now.Add(-time.Hour).Unix(), now.Unix())
	if e != nil || len(rows) != 1 || rows[0].Amount == nil {
		t.Fatal(rows, e)
	}
}
func TestChannelSpendAPIUsesAccountAuthorizationAndExplicitTime(t *testing.T) {
	s, _, owner := attributionFixture(t)
	id := "source" + randomID()[:8]
	_, e := s.control.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: id, Name: id, Base: "https://source.test"}, Key: "test-admin"}, false)
	if e != nil {
		t.Fatal(e)
	}
	defer s.control.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
	cookie, _ := s.control.newSession(context.Background(), owner)
	q := url.Values{"source_id": {id}, "from": {strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)}, "to": {strconv.FormatInt(time.Now().Unix(), 10)}}
	for _, auth := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/v1/channel-spend?"+q.Encode(), nil)
		if auth {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		want := 401
		if auth {
			want = 200
		}
		if w.Code != want {
			t.Fatal(fmt.Sprintf("status %d: %s", w.Code, w.Body.String()))
		}
	}
}

func TestAttributedSpendUsesCompletionWindowAndRefusesReusedCallerIDs(t *testing.T) {
	s, account, owner := attributionFixture(t)
	now := time.Now().Truncate(time.Second)
	b := attributedFact("boundary", "caller", now.Add(-2*time.Minute))
	a := b
	a.Kind = "attempt"
	a.EventID = "completion"
	a.AtMS = now.Add(-10 * time.Second).UnixMilli()
	a.BillingRequestIDs = nil
	if e := s.engine.Import(context.Background(), "boundary", "v1", []Fact{b, a}); e != nil {
		t.Fatal(e)
	}
	storeAttributedLog(t, s, account, "boundary", "0.4", 1)
	rows, e := s.attributedChannelSpend(context.Background(), owner, QueryFilter{KeyID: "caller"}, now.Add(-time.Minute).Unix(), now.Unix())
	if e != nil || len(rows) != 1 || rows[0].Amount == nil || *rows[0].Amount != .4 {
		t.Fatal(rows, e)
	}
	rows, e = s.attributedChannelSpend(context.Background(), owner, QueryFilter{KeyID: "caller"}, now.Add(-3*time.Minute).Unix(), now.Add(-time.Minute).Unix())
	if e != nil || len(rows) != 0 {
		t.Fatal("billed outside completion window", rows, e)
	}
	a.EventID = "reused-id"
	a.AtMS = now.Add(-5 * time.Second).UnixMilli()
	if e = s.engine.Import(context.Background(), "reuse", "v1", []Fact{a}); e != nil {
		t.Fatal(e)
	}
	rows, e = s.attributedChannelSpend(context.Background(), owner, QueryFilter{KeyID: "caller"}, now.Add(-time.Minute).Unix(), now.Unix())
	if e != nil || len(rows) != 1 || rows[0].Status != "ambiguous" || rows[0].Amount != nil {
		t.Fatal(rows, e)
	}
}
func TestAttributedSpendSurfacesLedgerFailure(t *testing.T) {
	s, account, owner := attributionFixture(t)
	now := time.Now().Truncate(time.Second)
	if e := s.engine.Import(context.Background(), "facts", "v1", []Fact{attributedFact("missing", "caller", now.Add(-time.Minute))}); e != nil {
		t.Fatal(e)
	}
	f := QueryFilter{KeyID: "caller"}
	if _, e := s.attributedChannelSpend(context.Background(), owner, f, now.Add(-time.Hour).Unix(), now.Unix()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.control.db.Exec(`UPDATE console_sub_spend_cache SET error='not available' WHERE account_id=$1`, account); e != nil {
		t.Fatal(e)
	}
	rows, e := s.attributedChannelSpend(context.Background(), owner, f, now.Add(-time.Hour).Unix(), now.Unix())
	if e != nil || len(rows) != 1 || rows[0].Status != "error" || rows[0].Amount != nil {
		t.Fatal(rows, e)
	}
}

func TestAttributedSpendMixedMissingAndPendingBillsKeepsRefreshing(t *testing.T) {
	s, account, owner := attributionFixture(t)
	now := time.Now().Truncate(time.Second)
	legacy := attributedFact("legacy", "caller", now.Add(-time.Hour))
	legacy.Kind = "attempt"
	legacy.BillingRequestIDs = nil
	noHeader := attributedFact("timeout", "caller", now.Add(-time.Minute))
	noHeader.Status = 524
	noHeader.BillingRequestIDs = nil
	unbound := attributedFact("unbound", "caller", now.Add(-time.Minute))
	unbound.UpstreamKeyHash = tokenHash("unbound-secret")
	paid := attributedFact("paid", "caller", now.Add(-time.Minute))
	delayed := attributedFact("delayed", "caller", now.Add(-time.Minute))
	if err := s.engine.Import(context.Background(), "mixed", "v1", []Fact{legacy, noHeader, unbound, paid, delayed}); err != nil {
		t.Fatal(err)
	}
	storeAttributedLog(t, s, account, "paid", "0.25", 1)
	f := QueryFilter{SourceID: "source", KeyID: "caller", Model: checkModel}
	read := func() attributedSpend {
		rows, e := s.attributedChannelSpend(context.Background(), owner, f, now.Add(-2*time.Hour).Unix(), now.Unix())
		if e != nil || len(rows) != 1 {
			t.Fatal(rows, e)
		}
		return rows[0]
	}
	row := read()
	if row.Amount != nil || row.MatchedAmount != .25 || row.Total != 5 || row.Matched != 1 || row.Missing != 3 || row.Pending != 1 || !row.Refreshing || row.LegacyMissing != 1 || row.HeaderMissing != 1 || row.Unbound != 1 || row.MissingStatuses["524"] != 1 {
		t.Fatal(row)
	}
	storeAttributedLog(t, s, account, "delayed", "0.75", 2)
	row = read()
	if row.Amount != nil || row.MatchedAmount != 1 || row.Matched != 2 || row.Pending != 0 || row.Refreshing || row.Missing != 3 {
		t.Fatal(row)
	}
}

func TestAttributedSpendMatchesSearchReceiptsFromExistingCheckpoints(t *testing.T) {
	s, account, owner := attributionFixture(t)
	now := time.Now().Truncate(time.Second)
	fact := attributedFact("search", "caller", now.Add(-time.Minute))
	fact.Endpoint = "/v1/alpha/search"
	fact.UpstreamBase = "https://usage.example/v1/alpha/search"
	if err := s.engine.Import(context.Background(), "search", "v1", []Fact{fact}); err != nil {
		t.Fatal(err)
	}
	var normalized string
	if err := s.engine.DB.QueryRow(`SELECT upstream_base FROM facts WHERE event_id='search'`).Scan(&normalized); err != nil || normalized != "https://usage.example" {
		t.Fatal(normalized, err)
	}
	// Reproduce the existing on-disk shape, which must also work without reimport.
	if _, err := s.engine.DB.Exec(`UPDATE facts SET upstream_base='https://usage.example/v1/alpha/search' WHERE event_id='search'`); err != nil {
		t.Fatal(err)
	}
	storeAttributedLog(t, s, account, "search", "0.0012", 1)
	f := QueryFilter{SourceID: "source", KeyID: "caller", Model: checkModel, Endpoint: "/v1/alpha/search"}
	read := func() attributedSpend {
		rs, e := s.attributedChannelSpend(context.Background(), owner, f, now.Add(-time.Hour).Unix(), now.Unix())
		if e != nil || len(rs) != 1 {
			t.Fatal(rs, e)
		}
		return rs[0]
	}
	row := read()
	if row.Status != "complete" || row.Amount == nil || *row.Amount != .0012 || row.Unbound != 0 {
		t.Fatal(row)
	}
	duplicate := fact
	duplicate.EventID = "duplicate"
	duplicate.KeyID = "other-caller"
	duplicate.SourceID = "other-source"
	duplicate.UpstreamBase = "https://usage.example"
	if err := s.engine.Import(context.Background(), "duplicate", "v1", []Fact{duplicate}); err != nil {
		t.Fatal(err)
	}
	row = read()
	if row.Status != "ambiguous" || row.Amount != nil {
		t.Fatal("canonical site allowed duplicate receipt claims", row)
	}
	if subBindingSite("https://usage.example/tenant/v1/alpha/search") != "https://usage.example/tenant" {
		t.Fatal("tenant prefix lost")
	}
	if subBindingSite("https://usage.example/tenant/unknown/search") != "https://usage.example/tenant/unknown/search" {
		t.Fatal("unknown path silently removed")
	}
}

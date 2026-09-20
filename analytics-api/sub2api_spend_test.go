package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSlowSpendPagesHaveIndependentDeadlinesAndRenewLease(t *testing.T) {
	s, account, _, request := spendTestService(t)
	to := time.Now().Truncate(time.Second)
	calls := 0
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var leaseSeconds float64
		if err := s.control.db.QueryRow(`SELECT extract(epoch FROM usage_lease_until-now()) FROM console_sub_accounts WHERE id=$1`, account).Scan(&leaseSeconds); err != nil || leaseSeconds < 25 {
			t.Error("page did not renew account lease", leaseSeconds, err)
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(4200 * time.Millisecond):
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		writeJSON(w, 200, map[string]any{"data": subSpendPage{Items: []subSpendLog{spendLog(int64(page), to.Add(-time.Minute), "0.1")}, Page: page, Pages: 5, PageSize: 1000}})
	}))
	request("1h", to)
	runSpendBatch(t, s, account)
	if out := request("1h", to); calls != 5 || out.Status != "complete" || out.Amount == nil || *out.Amount != .5 {
		t.Fatal("successful slow pages exhausted a shared batch deadline", calls, out)
	}
}

func TestSpendPageCannotRenewReplacedOrExpiredLease(t *testing.T) {
	s, account, _, request := spendTestService(t)
	request("1h", time.Now().Truncate(time.Second))
	task, err := s.subClaimSpend(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("stale reader reached upstream") }))
	for _, expired := range []bool{false, true} {
		if expired {
			_, err = s.control.db.Exec(`UPDATE console_sub_accounts SET usage_lease_token=$2,usage_lease_until=now()-interval '1 second' WHERE id=$1`, account, task.lease)
		} else {
			_, err = s.control.db.Exec(`UPDATE console_sub_accounts SET usage_lease_token='replacement' WHERE id=$1`, account)
		}
		if err != nil {
			t.Fatal(err)
		}
		var page subSpendPage
		if err = s.subSpendPageGET(context.Background(), task, url.Values{}, &page); !errors.Is(err, context.Canceled) {
			t.Fatal("stale reader regained lease", err)
		}
	}
}

func spendLog(id int64, at time.Time, cost string) subSpendLog {
	amount := json.Number(cost)
	return subSpendLog{ID: id, KeyID: 42, GroupID: usageInt(7), At: at.UTC().Format(time.RFC3339Nano), Cost: &amount}
}

func spendTestService(t *testing.T) (*Service, string, string, func(string, time.Time) subSpend) {
	t.Helper()
	s, account, owner := subUsageTestService(t)
	_, err := s.control.db.Exec(`UPDATE console_sub_accounts SET created_at='2026-01-01T00:00:00Z' WHERE id=$1`, account)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,routing_key_id) VALUES($1,7,'fixture','openai',99,42)`, account)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.control.newSession(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	request := func(window string, to time.Time) subSpend {
		r := httptest.NewRequest("GET", fmt.Sprintf("/v1/sub2api/accounts/%s/groups/7/spend?range=%s&to=%d", account, window, to.Unix()), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("spend HTTP %d: %s", w.Code, w.Body.String())
		}
		var out subSpend
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	return s, account, owner, request
}

func withSpendUpstream(t *testing.T, h http.Handler) {
	t.Helper()
	up := httptest.NewServer(h)
	old := subHTTP
	u, _ := url.Parse(up.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}}
	t.Cleanup(func() { subHTTP = old; up.Close() })
}

func runSpendBatch(t *testing.T, s *Service, account string) {
	t.Helper()
	// Advance the bounded batch backoff without sleeping in tests.
	s.control.db.Exec(`UPDATE console_sub_account_spend_cache SET next_attempt=now() WHERE account_id=$1`, account)
	task, err := s.subClaimSpend(context.Background())
	if err != nil || task.account != account {
		t.Fatal(task.account, err)
	}
	if _, err := s.subClaimSpend(context.Background()); err == nil {
		t.Fatal("duplicate account reader")
	}
	s.subScanSpend(context.Background(), task)
}

func TestSubSpendUsesBusinessKeyExactWindowAndDecimalTotals(t *testing.T) {
	s, account, _, request := spendTestService(t)
	to := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	calls := 0
	logs := []subSpendLog{spendLog(1, from.Add(-time.Second), "0.7"), spendLog(2, from, "0.1"), spendLog(3, from.Add(20*time.Minute), "0.2"), spendLog(4, to, "10"), spendLog(5, to.Add(-time.Minute), "100")}
	logs[4].KeyID = 99 // the probe key must never enter business consumption
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/usage" || r.URL.Query().Get("api_key_id") != "" || r.URL.Query().Get("timezone") != "UTC" {
			t.Error("wrong bill scope", r.URL.Path, r.URL.RawQuery)
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": subSpendPage{Items: logs, Page: 1, Pages: 1, PageSize: 100}})
	}))
	if out := request("1h", to); out.Status != "pending" || out.Amount != nil {
		t.Fatal("uncached total shown", out)
	}
	runSpendBatch(t, s, account)
	out := request("1h", to)
	if out.Status != "complete" || out.Amount == nil || *out.Amount != .3 || *out.Requests != 2 || out.From != from.Unix() || out.KeyID != 42 {
		t.Fatal(out)
	}
	if out = request("5m", to); out.Status != "complete" || *out.Amount != 0 || *out.Requests != 0 {
		t.Fatal("empty complete interval is not zero", out)
	}
	if calls != 1 {
		t.Fatal("overlapping range refetched complete cache", calls)
	}
	if out = request("2h", to); out.Status != "pending" || out.Amount != nil {
		t.Fatal("narrow cache treated as full history", out)
	}
	runSpendBatch(t, s, account)
	if out = request("2h", to); out.Status != "complete" || *out.Amount != 1 || *out.Requests != 3 {
		t.Fatal(out)
	}
	if out = request("1h", to); *out.Amount != .3 {
		t.Fatal("backfill duplicated previous receipts", out)
	}
	// Corrections replace the existing receipt, including when re-reading a
	// complete interval after a refresh; they do not add a second copy.
	logs[2] = spendLog(3, from.Add(20*time.Minute), "0.4")
	s.control.db.Exec(`UPDATE console_sub_account_spend_cache SET checked_at=0 WHERE account_id=$1`, account)
	if out = request("1h", to); out.Status != "complete" || !out.Refreshing {
		t.Fatal(out)
	}
	runSpendBatch(t, s, account)
	if out = request("1h", to); math.Abs(*out.Amount-.5) > 1e-12 || *out.Requests != 2 {
		t.Fatal("correction counted twice", out)
	}
}

func TestSubSpendResumesLargeHistoryWithoutPublishingPartialSums(t *testing.T) {
	s, account, _, request := spendTestService(t)
	to := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	failPage := 3
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == failPage {
			http.Error(w, "unavailable", 503)
			return
		}
		items := []subSpendLog{}
		for i := 0; i < 100; i++ {
			id := int64((page-1)*100 + i + 1)
			items = append(items, spendLog(id, to.Add(-time.Hour).Add(time.Duration(id)*time.Second), "0.0000000001"))
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": subSpendPage{Items: items, Page: page, Pages: 7, PageSize: 100}})
	}))
	request("all", to)
	runSpendBatch(t, s, account)
	out := request("all", to)
	if out.Status != "error" || out.Amount != nil {
		t.Fatal("partial money exposed", out)
	}
	var next int
	s.control.db.QueryRow(`SELECT page FROM console_sub_account_spend_cache WHERE account_id=$1`, account).Scan(&next)
	if next != 3 {
		t.Fatal("failed page checkpoint lost", next)
	}
	failPage = 0
	// A replacement service uses the same durable cursor and rows.
	replacement := &Service{control: s.control}
	runSpendBatch(t, replacement, account)
	out = request("all", to)
	if out.Status != "complete" || out.Amount == nil || math.Abs(*out.Amount-.00000007) > 1e-15 || *out.Requests != 700 {
		t.Fatal(out)
	}
}

func TestSubSpendRejectsIncompleteLedgersAndForeignOwners(t *testing.T) {
	for _, kind := range []string{"missing_items", "missing_cost", "bad_time", "missing_key", "missing_pagination"} {
		t.Run(kind, func(t *testing.T) {
			s, account, owner, request := spendTestService(t)
			to := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
			withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := subSpendPage{Items: []subSpendLog{spendLog(1, to.Add(-time.Minute), "1")}, Page: 1, Pages: 1}
				switch kind {
				case "missing_items":
					body.Items = nil
				case "missing_cost":
					body.Items[0].Cost = nil
				case "bad_time":
					body.Items[0].At = "not-a-date"
				case "missing_key":
					body.Items[0].KeyID = 0
				case "missing_pagination":
					body.Pages = 0
					body.PageSize = 0
				}
				writeJSON(w, 200, map[string]any{"code": 0, "data": body})
			}))
			request("1h", to)
			runSpendBatch(t, s, account)
			if out := request("1h", to); out.Status != "error" || out.Amount != nil {
				t.Fatal(out)
			}
			foreign, _ := s.control.newSession(context.Background(), owner+"-other")
			r := httptest.NewRequest("GET", fmt.Sprintf("/v1/sub2api/accounts/%s/groups/7/spend?range=all", account), nil)
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: foreign})
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != 404 {
				t.Fatal("another owner read bill", w.Code, w.Body.String())
			}
		})
	}
}

func TestSubSpendSupportsAllHoursAndCalendarTimezone(t *testing.T) {
	s, _, _, request := spendTestService(t)
	zone, _ := time.LoadLocation("Asia/Shanghai")
	s.engine = &Engine{Location: zone}
	to := time.Date(2026, 9, 17, 18, 24, 37, 0, time.UTC)
	for hour := 1; hour <= 24; hour++ {
		out := request(fmt.Sprintf("%dh", hour), to)
		if out.From != to.Add(-time.Duration(hour)*time.Hour).Truncate(time.Minute).Unix() || out.To != to.Unix() {
			t.Fatal(hour, out)
		}
	}
	for _, window := range []string{"5m", "15m", "7d", "30d", "today", "week", "month", "year", "all"} {
		out := request(window, to)
		start, _ := rangeStart(window, to, zone)
		if out.From != start.Truncate(time.Minute).Unix() {
			t.Fatal(window, out)
		}
	}
	if out := request("today", to); out.From != time.Date(2026, 9, 17, 16, 0, 0, 0, time.UTC).Unix() {
		t.Fatal("calendar day used UTC instead of console timezone", out)
	}
}

func TestSubSpendAndReceiptReadersShareAccountLease(t *testing.T) {
	s, account, _, request := spendTestService(t)
	ctx := context.Background()
	result := subResult{Model: checkModel, Availability: subProbe{ID: "lease-" + randomID(), StartedAt: time.Now().Unix(), RequestIDs: []string{"id"}}}
	if err := s.subQueueUsage(ctx, account, 7, 99, &result); err != nil {
		t.Fatal(err)
	}
	request("1h", time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC))
	_, _, lease, err := s.subClaimUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.subClaimSpend(ctx); err == nil {
		t.Fatal("two readers entered one account")
	}
	s.control.db.Exec(`UPDATE console_sub_accounts SET usage_lease_until=now()-interval '1 second' WHERE id=$1`, account)
	task, err := s.subClaimSpend(ctx)
	if err != nil || task.lease == lease || strings.TrimSpace(task.lease) == "" {
		t.Fatal(task, err)
	}
	// A stale release must not remove the replacement reader's lease.
	s.control.db.Exec(`UPDATE console_sub_accounts SET usage_lease_token='',usage_lease_until=NULL WHERE id=$1 AND usage_lease_token=$2`, account, lease)
	if _, _, _, err = s.subClaimUsage(ctx); err == nil {
		t.Fatal("stale worker unlocked replacement")
	}
}

func TestAccountSpendOneDownloadServesMultipleKeysAndPreciseAttribution(t *testing.T) {
	s, account, owner := attributionFixture(t)
	now := time.Now().Truncate(time.Second)
	from, to := now.Add(-time.Hour).Unix(), now.Unix()
	_, err := s.control.db.Exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES($1,$2,43,8)`, account, tokenHash("second-secret"))
	if err != nil {
		t.Fatal(err)
	}
	a := attributedFact("one", "caller-a", now.Add(-time.Minute))
	b := attributedFact("two", "caller-b", now.Add(-time.Minute))
	b.UpstreamKeyHash = tokenHash("second-secret")
	if err = s.engine.Import(context.Background(), "multi-key", "v1", []Fact{a, b}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"caller-a", "caller-b"} {
		if _, err = s.attributedChannelSpend(context.Background(), owner, QueryFilter{KeyID: key}, from, to); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	s.control.db.QueryRow(`SELECT count(*) FROM console_sub_account_spend_cache WHERE account_id=$1`, account).Scan(&count)
	if count != 1 {
		t.Fatal("per-key cursors remain", count)
	}
	calls := 0
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Has("api_key_id") || q.Has("group_id") || q.Get("page_size") != "1000" {
			t.Error("not an account download", r.URL.RawQuery)
		}
		first := spendLog(1, now.Add(-time.Minute), "0.1")
		first.RequestID = "client:one"
		second := spendLog(2, now.Add(-time.Minute), "0.2")
		second.KeyID = 43
		second.GroupID = usageInt(8)
		second.RequestID = "client:two"
		// Other caller's logs are cached but not assigned to our two requests.
		other := spendLog(3, now.Add(-time.Minute), "900")
		other.KeyID = 99
		other.RequestID = "client:unrelated"
		writeJSON(w, 200, map[string]any{"code": 0, "data": subSpendPage{Items: []subSpendLog{first, second, other}, Page: 1, Pages: 1, PageSize: 1000}})
	}))
	runSpendBatch(t, s, account)
	for key, want := range map[string]float64{"caller-a": .1, "caller-b": .2} {
		rs, e := s.attributedChannelSpend(context.Background(), owner, QueryFilter{KeyID: key}, from, to)
		if e != nil || len(rs) != 1 || rs[0].Amount == nil || *rs[0].Amount != want {
			t.Fatal(key, rs, e)
		}
	}
	if calls != 1 {
		t.Fatal("same account downloaded for each caller", calls)
	}
	if _, e := s.subClaimSpend(context.Background()); e == nil {
		t.Fatal("complete shared cache unnecessarily queued")
	}
	s.control.db.QueryRow(`SELECT count(*) FROM console_sub_spend_logs WHERE account_id=$1`, account).Scan(&count)
	if count != 3 {
		t.Fatal("did not retain other account keys", count)
	}
}

func TestAccountSpendMigrationKeepsReceiptsButNeverClaimsAccountCoverage(t *testing.T) {
	s, account, _ := subUsageTestService(t)
	_, err := s.control.db.Exec(`INSERT INTO console_sub_spend_cache(account_id,key_id,group_id,wanted_from,wanted_to,covered_from,covered_to,requested,checked_at) VALUES($1,42,7,100,200,100,200,false,200),($1,43,8,50,300,50,300,false,300)`, account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.control.db.Exec(subSchema); err != nil {
		t.Fatal(err)
	}
	var from, to int64
	var covered bool
	if err = s.control.db.QueryRow(`SELECT wanted_from,wanted_to,covered_from IS NOT NULL OR covered_to IS NOT NULL FROM console_sub_account_spend_cache WHERE account_id=$1`, account).Scan(&from, &to, &covered); err != nil || from != 50 || to != 300 || covered {
		t.Fatal(from, to, covered, err)
	}
	s.control.db.Exec(`UPDATE console_sub_account_spend_cache SET page=6,scan_from=50,scan_to=300 WHERE account_id=$1`, account)
	if _, err = s.control.db.Exec(subSchema); err != nil {
		t.Fatal(err)
	}
	var page int
	s.control.db.QueryRow(`SELECT page FROM console_sub_account_spend_cache WHERE account_id=$1`, account).Scan(&page)
	if page != 6 {
		t.Fatal("restart reset cursor", page)
	}
}

func TestRecentReceiptsDoNotAdvanceHistoricalCoverageOrBreakResume(t *testing.T) {
	for _, recentMode := range []string{"ok", "slow", "unsupported", "invalid"} {
		t.Run(recentMode, func(t *testing.T) {
			s, account, owner := attributionFixture(t)
			now := time.Now().Truncate(time.Second)
			from, to := now.Add(-time.Hour).Unix(), now.Unix()
			fact := attributedFact("recent", "caller", now.Add(-time.Minute))
			if err := s.engine.Import(context.Background(), "recent", "1", []Fact{fact}); err != nil {
				t.Fatal(err)
			}
			read := func() attributedSpend {
				rows, err := s.attributedChannelSpend(context.Background(), owner, QueryFilter{KeyID: "caller"}, from, to)
				if err != nil || len(rows) != 1 {
					t.Fatalf("rows=%+v err=%v", rows, err)
				}
				return rows[0]
			}
			read()
			recentCalls := 0
			withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				page, _ := strconv.Atoi(q.Get("page"))
				log := spendLog(901, now.Add(-time.Minute), "0.3")
				log.RequestID = "client:recent"
				if q.Get("sort_order") == "desc" {
					recentCalls++
					if recentMode == "slow" {
						time.Sleep(3200 * time.Millisecond)
					}
					if page != 1 || q.Get("page_size") != "100" || q.Has("api_key_id") {
						t.Error("wrong recent account query", q)
					}
					if recentMode == "unsupported" {
						http.Error(w, "unsupported", 404)
						return
					}
					if recentMode == "invalid" {
						log.Cost = nil
					}
					writeJSON(w, 200, map[string]any{"data": subSpendPage{Items: []subSpendLog{log}, Page: 1, Pages: 7, PageSize: 100}})
					return
				}
				if q.Get("sort_order") != "asc" {
					t.Error("changed durable traversal", q)
				}
				items := []subSpendLog{spendLog(int64(page), now.Add(-30*time.Minute), "10")}
				if page == 7 {
					items = append(items, log)
				}
				writeJSON(w, 200, map[string]any{"data": subSpendPage{Items: items, Page: page, Pages: 7, PageSize: 100}})
			}))
			runSpendBatch(t, s, account)
			first := read()
			if recentMode == "ok" || recentMode == "slow" {
				if first.Status != "complete" || first.Amount == nil || *first.Amount != .3 {
					t.Fatal("latest exact receipt waits for old pages", first)
				}
			} else if first.Status != "pending" || first.Amount != nil {
				t.Fatal("failed recent read fabricated coverage", first)
			}
			var page int
			var noCoverage bool
			var message string
			err := s.control.db.QueryRow(`SELECT page,covered_to IS NULL,error FROM console_sub_account_spend_cache WHERE account_id=$1`, account).Scan(&page, &noCoverage, &message)
			if err != nil || page != 6 || !noCoverage || message != "" {
				t.Fatal("recent read changed full scan state", page, noCoverage, message, err)
			}
			runSpendBatch(t, s, account)
			last := read()
			if last.Status != "complete" || last.Amount == nil || *last.Amount != .3 || recentCalls != 1 {
				t.Fatal("resume/receipt dedup failed", last, recentCalls)
			}
			var count int
			s.control.db.QueryRow(`SELECT count(*) FROM console_sub_spend_logs WHERE account_id=$1 AND log_id=901`, account).Scan(&count)
			if count != 1 {
				t.Fatal("receipt stored twice", count)
			}
		})
	}
}

func TestAccountSpendClampedPageSizeAndExpandedWindowSurviveResume(t *testing.T) {
	s, account, _, request := spendTestService(t)
	to := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	calls := 0
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		calls++
		if page > 1 && q.Get("page_size") != "100" {
			t.Error("changed offset after server clamp", q)
		}
		if page == 1 {
			if err := s.queueAccountSpendWindow(context.Background(), account, to.Add(-2*time.Hour).UnixMilli(), to.UnixMilli()); err != nil {
				t.Error(err)
			}
		}
		item := spendLog(int64(page), to.Add(-time.Minute), "0.1")
		writeJSON(w, 200, map[string]any{"code": 0, "data": subSpendPage{Items: []subSpendLog{item}, Page: page, Pages: 6, PageSize: 100}})
	}))
	request("1h", to)
	runSpendBatch(t, s, account)
	var next, size int
	s.control.db.QueryRow(`SELECT page,page_size FROM console_sub_account_spend_cache WHERE account_id=$1`, account).Scan(&next, &size)
	if next != 6 || size != 100 {
		t.Fatal(next, size)
	}
	runSpendBatch(t, &Service{control: s.control}, account)
	out := request("2h", to)
	if out.Status == "complete" {
		t.Fatal("window extension lost", out)
	}
	if calls != 7 { // six durable pages plus one optional recent page
		t.Fatal("did not resume", calls)
	}
	runSpendBatch(t, s, account)
	runSpendBatch(t, s, account)
	if out = request("2h", to); out.Status != "complete" || out.Amount == nil || math.Abs(*out.Amount-.6) > 1e-9 {
		t.Fatal(out)
	}
}

func TestAccountSpendLargeResponseFallsBackWithoutSkippingOffsets(t *testing.T) {
	s, account, _, request := spendTestService(t)
	to := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_size") == "1000" {
			w.Write([]byte(strings.Repeat("x", (4<<20)+1)))
			return
		}
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("page_size") != "100" {
			t.Error(r.URL.RawQuery)
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": subSpendPage{Items: []subSpendLog{spendLog(1, to.Add(-time.Minute), "0.4")}, Page: 1, Pages: 1, PageSize: 100}})
	}))
	request("1h", to)
	runSpendBatch(t, s, account)
	var page, size int
	s.control.db.QueryRow(`SELECT page,page_size FROM console_sub_account_spend_cache WHERE account_id=$1`, account).Scan(&page, &size)
	if page != 1 || size != 100 {
		t.Fatal(page, size)
	}
	runSpendBatch(t, &Service{control: s.control}, account)
	if out := request("1h", to); out.Status != "complete" || out.Amount == nil || *out.Amount != .4 {
		t.Fatal(out)
	}
}

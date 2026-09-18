package main

import (
	"context"
	"encoding/json"
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
	s.control.db.Exec(`UPDATE console_sub_spend_cache SET next_attempt=now() WHERE account_id=$1`, account)
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
		if r.URL.Path != "/api/v1/usage" || r.URL.Query().Get("api_key_id") != "42" || r.URL.Query().Get("timezone") != "UTC" {
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
	s.control.db.Exec(`UPDATE console_sub_spend_cache SET checked_at=0 WHERE account_id=$1`, account)
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
	s.control.db.QueryRow(`SELECT page FROM console_sub_spend_cache WHERE account_id=$1`, account).Scan(&next)
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
	for _, kind := range []string{"missing_items", "missing_cost", "bad_time"} {
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

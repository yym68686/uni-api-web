package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func usageFloat(v float64) *float64 { return &v }
func usageInt(v int64) *int64       { return &v }
func usageFixture(id int64, request string) subUsageLog {
	return subUsageLog{ID: id, KeyID: 42, GroupID: usageInt(7), Model: checkModel, RequestID: request,
		InputTokens: usageInt(100), OutputTokens: usageInt(10), InputCost: usageFloat(.0005), OutputCost: usageFloat(.0003),
		TotalCost: usageFloat(.0008), ActualCost: usageFloat(.00008), Rate: usageFloat(.1), BillingMode: "token"}
}

func TestSubUsageReceiptUsesLedgerCostsAndKeepsUnknownSeparateFromFree(t *testing.T) {
	log := usageFixture(1, "client:request")
	log.CreatedAt = "private-panel-token"
	receipt := subUsageReceipt(log)
	if receipt.CreatedAt != "" {
		t.Fatal("invalid timestamp exposed upstream text")
	}
	if *receipt.InputPrice != 5 || *receipt.OutputPrice != 30 || math.Abs(*receipt.PaidOutputPrice-3) > 1e-10 || *receipt.ActualCost != .00008 {
		t.Fatalf("incorrect cost basis: %+v", receipt)
	}
	log.InputTokens = usageInt(0)
	log.OutputCost = usageFloat(0)
	log.ActualCost = usageFloat(0)
	log.Rate = usageFloat(0)
	receipt = subUsageReceipt(log)
	if receipt.InputPrice != nil || receipt.OutputPrice == nil || *receipt.OutputPrice != 0 || receipt.PaidOutputPrice == nil || *receipt.PaidOutputPrice != 0 {
		t.Fatal("missing tokens confused with free pricing", receipt)
	}
	log = usageFixture(1, "client:request")
	log.ActualCost = usageFloat(.00001) // cap/waiver cannot be allocated as a normal discount
	if got := subUsageReceipt(log); got.PaidInputPrice != nil || got.InputPrice == nil {
		t.Fatal(got)
	}
	log.BillingMode = "image"
	if got := subUsageReceipt(log); got.InputPrice != nil || got.OutputPrice != nil || got.ActualCost == nil {
		t.Fatal(got)
	}
	if subUnitPrice(usageFloat(math.Inf(1)), usageInt(10)) != nil || subUnitPrice(usageFloat(-1), usageInt(10)) != nil {
		t.Fatal("invalid price accepted")
	}
}

func TestSubUsageMatchesOnlyExactRequestAndKey(t *testing.T) {
	task := subUsageTask{id: "probe", group: 7, key: 42, ids: []string{"client:mine", "upstream"}}
	wrongKey, wrongGroup := usageFixture(1, "client:mine"), usageFixture(2, "client:mine")
	wrongKey.KeyID = 43
	wrongGroup.GroupID = usageInt(8)
	if subMatchUsage(task, []subUsageLog{wrongKey, wrongGroup, usageFixture(3, "client:other")}) != nil {
		t.Fatal("foreign request matched")
	}
	matched := subMatchUsage(task, []subUsageLog{wrongKey, usageFixture(4, "client:mine")})
	if matched == nil || matched.LogID != 4 || matched.Status != "matched" {
		t.Fatal(matched)
	}
	if got := subMatchUsage(task, []subUsageLog{usageFixture(4, "client:mine"), usageFixture(5, "upstream")}); got == nil || got.Status != "ambiguous" {
		t.Fatal("ambiguous amount accepted", got)
	}
}

func TestSubProbeCapturesRequestCorrelationWithoutExtraModelCalls(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasPrefix(r.Header.Get("X-Request-ID"), "subcheck-") {
			t.Error("missing unique request id")
		}
		w.Header().Set("X-Client-Request-ID", "site-request")
		w.Header().Set("X-Request-ID", "echo-private-key")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-one\"}}\n\n")
		subSSE(w, "21")
	}))
	defer up.Close()
	result := subRunQualityProbe(context.Background(), up.Client(), up.URL, "private-key")
	if calls.Load() != 1 || result.Availability.ID == "" || result.Availability.ID != result.Quality.ID {
		t.Fatal(result)
	}
	raw, _ := json.Marshal(result.Availability.RequestIDs)
	if !strings.Contains(string(raw), "client:site-request") || !strings.Contains(string(raw), "resp-one") || strings.Contains(string(raw), "private-key") {
		t.Fatal(string(raw))
	}
}

func subUsageTestService(t *testing.T) (*Service, string, string) {
	t.Helper()
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("u", 32))
	if err != nil {
		t.Fatal(err)
	}
	account, owner := "usage-"+randomID()[:12], "usage-owner-"+randomID()[:12]
	auth, _ := store.encrypt(`{"access_token":"expired-access","refresh_token":"refresh-secret"}`)
	if _, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'fixture','https://usage.example','fixture@example.com',$3)`, account, owner, auth); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, account); store.Close() })
	return &Service{control: store}, account, owner
}

func TestSubUsagePersistsRetriesRefreshesAndPagesWithoutReissuingProbe(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	ctx := context.Background()
	probe := subProbe{ID: "usage-" + randomID(), RequestedModel: checkModel, StartedAt: time.Now().Unix(), RequestIDs: []string{"client:mine"}, Status: "success"}
	result := subResult{Model: checkModel, Availability: probe, Quality: probe}
	if err := s.subQueueUsage(ctx, account, 7, 42, &result); err != nil {
		t.Fatal(err)
	}
	var n int
	s.control.db.QueryRow(`SELECT count(*) FROM console_sub_usage WHERE account_id=$1`, account).Scan(&n)
	if n != 1 {
		t.Fatal("quality-only request billed twice", n)
	}
	var logsReady atomic.Bool
	var refreshes, reads atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/refresh" {
			refreshes.Add(1)
			writeJSON(w, 200, map[string]any{"code": 0, "data": subAuth{Access: "current-access", Refresh: "rotated", ExpiresIn: 3600}})
			return
		}
		if r.URL.Path != "/api/v1/usage" || r.Method != "GET" {
			t.Error("extra model request or wrong path", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		reads.Add(1)
		if r.Header.Get("Authorization") == "Bearer expired-access" {
			http.Error(w, "expired", 401)
			return
		}
		if r.Header.Get("Authorization") != "Bearer current-access" {
			t.Error("wrong session")
		}
		if r.URL.Query().Get("api_key_id") != "42" || r.URL.Query().Get("timezone") != "UTC" {
			t.Error("missing key/date scope")
		}
		items := []subUsageLog{}
		pages := 1
		if logsReady.Load() {
			pages = 2
			if r.URL.Query().Get("page") == "1" {
				for i := range 100 {
					items = append(items, usageFixture(int64(i+1), fmt.Sprintf("other-%d", i)))
				}
				items[0] = usageFixture(1, "client:mine")
				items[0].KeyID = 99
			} else {
				items = append(items, usageFixture(101, "client:mine"))
			}
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{"items": items, "pages": pages}})
	}))
	defer up.Close()
	previous := subHTTP
	u, _ := url.Parse(up.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}}
	defer func() { subHTTP = previous }()
	run := func() {
		id, base, lease, err := s.subClaimUsage(ctx)
		if err != nil || id != account {
			t.Fatal(id, err)
		}
		if _, _, _, err := s.subClaimUsage(ctx); err == nil {
			t.Fatal("another replica claimed the same account")
		}
		s.subReadUsage(ctx, id, base, lease)
	}
	run()
	accounts := []subAccount{{ID: account, Targets: []subTarget{{Result: &result}}}}
	s.subAttachUsage(ctx, owner, accounts)
	if result.Availability.Usage.Status != "pending" || result.Availability.Usage.ActualCost != nil {
		t.Fatal("not-yet-written log became free/normal", result.Availability.Usage)
	}
	logsReady.Store(true)
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET next_attempt=now() WHERE account_id=$1`, account); err != nil {
		t.Fatal(err)
	}
	run()
	s.subAttachUsage(ctx, owner, accounts)
	if result.Availability.Usage.Status != "matched" || result.Quality.Usage.LogID != 101 || *result.Availability.Usage.InputPrice != 5 || refreshes.Load() != 1 || reads.Load() != 4 {
		t.Fatal(result, refreshes.Load(), reads.Load())
	}
	result.Availability.Usage = nil
	s.subAttachUsage(ctx, owner+"-other", accounts)
	if result.Availability.Usage != nil {
		t.Fatal("receipt leaked to another account owner")
	}
	if _, _, _, err := s.subClaimUsage(ctx); err == nil {
		t.Fatal("completed receipts queried again")
	}
}

func TestSubUsageUnsupportedEndpointDoesNotChangeDetection(t *testing.T) {
	s, account, _ := subUsageTestService(t)
	ctx := context.Background()
	result := subResult{Model: checkModel, Availability: subProbe{ID: "probe-" + randomID(), StartedAt: time.Now().Unix(), Status: "success", Text: "test", RequestIDs: []string{"request"}}}
	if err := s.subQueueUsage(ctx, account, 7, 42, &result); err != nil {
		t.Fatal(err)
	}
	up := httptest.NewServer(http.NotFoundHandler())
	defer up.Close()
	old := subHTTP
	u, _ := url.Parse(up.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}}
	defer func() { subHTTP = old }()
	id, base, lease, err := s.subClaimUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.subReadUsage(ctx, id, base, lease)
	var status string
	s.control.db.QueryRow(`SELECT status FROM console_sub_usage WHERE account_id=$1`, account).Scan(&status)
	if status != "unsupported" || result.Availability.Status != "success" {
		t.Fatal(status, result)
	}
}

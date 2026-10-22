package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

// A failed receipt query must not publish the probe's old embedded usage as a
// successful current read (or confirm the old summary with HTTP 304).
func TestSubAccountReceiptReadFailureIsNotCurrentData(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.control.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES($1,7,'group','openai')`, account)
	result := subResult{Model: "model", Availability: subProbe{ID: account + "-probe", Status: "success", Usage: &subUsage{Status: "pending"}}}
	exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,7,'model','done',$2)`, account, mustJSON(result))
	exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status) VALUES($1,$2,7,0,'model',0,'[]','pending')`, account+"-probe", account)
	token, err := s.control.newSession(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	get := func(since string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts?include_rates=0&account_id="+account, nil)
		r.Header.Set("X-Console-View", "summary")
		r.Header.Set("X-Console-Since", since)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	baseline := get("")
	if baseline.Code != 200 {
		t.Fatal(baseline.Code, baseline.Body.String())
	}
	version := strings.Trim(baseline.Header().Get("ETag"), `"`)
	if version == "" {
		t.Fatal("missing baseline version")
	}
	// Only this disposable database is locked. A SQL deadline leaves the HTTP
	// context usable, reproducing an interrupted query without ending the request.
	blocker, err := sql.Open("pgx", os.Getenv("TEST_CONTROL_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	lock, err := blocker.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.Exec(`LOCK TABLE console_sub_usage IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	s.control.db.SetMaxOpenConns(1)
	exec(`SET statement_timeout='100ms'`)
	defer s.control.db.Exec(`SET statement_timeout=0`)
	for _, since := range []string{"", version} {
		w := get(since)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("receipt read failed but returned HTTP %d as current data: %s", w.Code, w.Body.String())
		}
		if w.Header().Get("ETag") != "" || strings.Contains(w.Body.String(), "summary-v1") {
			t.Fatal("failed receipt read published a summary version")
		}
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	exec(`SET statement_timeout=0`)
	exec(`UPDATE console_sub_usage SET status='matched',result=$2 WHERE id=$1`, account+"-probe", mustJSON(subUsage{Status: "matched", InputPrice: usageFloat(3)}))
	recovered := get(version)
	if recovered.Code != 200 || !strings.Contains(recovered.Body.String(), `"input_price":3`) || !strings.Contains(recovered.Body.String(), `"status":"matched"`) {
		t.Fatal("fresh receipts did not replace the prior summary", recovered.Code, recovered.Body.String())
	}
}

func TestSubAttachUsagePublishesOnlyCompleteAuthorizedRead(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	ctx := context.Background()
	ids := []string{account + "-first", account + "-last"}
	for _, id := range ids {
		_, err := s.control.db.Exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES($1,$2,7,0,'model',0,'[]','pending',$3)`, id, account, mustJSON(subUsage{Status: "pending", InputPrice: usageFloat(0), InputTokens: usageInt(0)}))
		if err != nil {
			t.Fatal(err)
		}
	}
	makeAccounts := func() []subAccount {
		result := func(id string) *subResult {
			return &subResult{Availability: subProbe{ID: id, Usage: &subUsage{Status: "old"}}}
		}
		return []subAccount{{ID: account, Targets: []subTarget{{GroupID: 7, Result: result(ids[0]), Models: []subModelResult{
			{Model: "first", Result: result(ids[0])},
			{Model: "last", Result: result(ids[1])},
			{Model: "missing", Result: result(account + "-missing")},
		}}}}}
	}
	accounts := makeAccounts()
	before := mustJSON(accounts)
	if err := s.subAttachUsage(ctx, owner+"-foreign", accounts); err != nil || mustJSON(accounts) != before {
		t.Fatal("foreign receipt attached", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.subAttachUsage(canceled, owner, accounts); !errors.Is(err, context.Canceled) || mustJSON(accounts) != before {
		t.Fatal("canceled read mutated data or lost cancellation", err)
	}
	// JSONB is valid JSON, but an unusable typed receipt must not be skipped and
	// mixed with fresh rows. Test either position without depending on row order.
	for _, id := range ids {
		if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"input_price":"invalid"}' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		accounts = makeAccounts()
		if err := s.subAttachUsage(ctx, owner, accounts); err == nil || mustJSON(accounts) != before {
			t.Fatal("invalid receipt published partial data", err)
		}
		if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result=$2 WHERE id=$1`, id, mustJSON(subUsage{Status: "pending", InputPrice: usageFloat(0), InputTokens: usageInt(0)})); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.subAttachUsage(ctx, owner, accounts); err != nil {
		t.Fatal(err)
	}
	target := accounts[0].Targets[0]
	for _, result := range []*subResult{target.Result, target.Models[0].Result, target.Models[1].Result} {
		if !reflect.DeepEqual(result.Availability.Usage, &subUsage{Status: "pending", InputPrice: usageFloat(0), InputTokens: usageInt(0)}) {
			t.Fatal("latest receipt values or zero changed")
		}
	}
	if target.Models[2].Result.Availability.Usage.Status != "old" {
		t.Fatal("absent receipt fabricated")
	}
}

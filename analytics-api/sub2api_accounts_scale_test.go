package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in regression at the incident's scale, with synthetic data only. Run on
// an isolated PostgreSQL using RUN_SUB_ACCOUNTS_SCALE=1; never on production.
func TestSubAccountSummaryScale(t *testing.T) {
	if os.Getenv("RUN_SUB_ACCOUNTS_SCALE") != "1" {
		t.Skip("opt-in isolated scale fixture")
	}
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := "summary-scale-" + randomID()
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE owner=$1`, owner)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := store.db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth)
SELECT $1||'-'||a,$1,'fixture','https://fixture.test',a::text,'' FROM generate_series(0,45) a`, owner)
	exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform)
SELECT $1||'-'||(g%46),g,'group','openai' FROM generate_series(0,683) g`, owner)
	exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result)
SELECT $1||'-'||(g%46),g,'model-'||m,'done', jsonb_build_object('model','model-'||m,'checked_at',1,'verdict','pass',
'availability',jsonb_build_object('id',md5(n::text),'status','success','terminal_status','completed','protocol','codex','model_match','match','requested_model','model-'||m,'response_model','model-'||m,'ttft_ms',123,'response_created_ms',124,'duration_ms',321,'http_status',200,'text','ok','curl_token',nonce,'request_ids',jsonb_build_array(md5(n::text||'x'),md5(n::text||'y')),'usage',jsonb_build_object('status','pending','input_price',2,'output_price',10,'input_tokens',100,'output_tokens',20,'request_id',md5(n::text),'message',nonce)),
'quality',jsonb_build_object('status','skipped','text',''))
FROM (SELECT n,n%684 g,n/684 m,(SELECT string_agg(md5(n::text||':'||k),'') FROM generate_series(1,24) k) nonce FROM generate_series(0,115674) n) data`, owner)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	rows, err := store.db.QueryContext(ctx, `SELECT COALESCE((SELECT jsonb_agg(jsonb_build_object('model',m.model,'state',m.state,'message',m.message,'result',m.result) ORDER BY m.model) FROM console_sub_models m WHERE m.account_id=t.account_id AND m.group_id=t.group_id),'[]'::jsonb) FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id WHERE a.owner=$1 ORDER BY t.group_id`, owner)
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, count := 0, 0
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		oldBytes += len(raw)
		var models []subModelResult
		if err = json.Unmarshal(raw, &models); err != nil {
			t.Fatal(err)
		}
		count += len(models)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if count != 115675 {
		t.Fatal(count)
	}
	t.Logf("legacy aggregate/model decode: %s, %d bytes, %d models", time.Since(start), oldBytes, count)
	s := &Service{control: store}
	accounts := make([]subAccount, 46)
	for i := range accounts {
		accounts[i].ID = fmt.Sprintf("%s-%d", owner, i)
	}
	for group := 0; group < 684; group++ {
		accounts[group%46].Targets = append(accounts[group%46].Targets, subTarget{GroupID: int64(group)})
	}
	for _, summary := range []bool{false, true} {
		for i := range accounts {
			for j := range accounts[i].Targets {
				accounts[i].Targets[j].Models = nil
			}
		}
		start = time.Now()
		count, resultBytes, err := s.subReadModels(ctx, owner, "", 0, "", summary, accounts)
		if err != nil || count != 115675 {
			t.Fatal(count, err)
		}
		t.Logf("bulk models summary=%t: %s, result_bytes=%d", summary, time.Since(start), resultBytes)
	}
	token, _ := store.newSession(context.Background(), owner)
	for run := 1; run <= 2; run++ {
		start = time.Now()
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts", nil).WithContext(ctx)
		r.Header.Set("X-Console-View", "summary")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		t.Logf("full summary HTTP run %d: %s, %d bytes", run, time.Since(start), w.Body.Len())
		if strings.Count(w.Body.String(), `"state":"done"`) != 115675 {
			t.Fatal("models missing")
		}
	}
	start = time.Now()
	maxPart, allModels, maxBytes := time.Duration(0), 0, 0
	for _, account := range accounts {
		partCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts?include_rates=0&account_id="+account.ID, nil).WithContext(partCtx)
		r.Header.Set("X-Console-View", "summary")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		began := time.Now()
		s.Handler().ServeHTTP(w, r)
		elapsed := time.Since(began)
		stop()
		if w.Code != 200 || elapsed >= 10*time.Second {
			t.Fatal("account part exceeded safe response budget", w.Code, elapsed)
		}
		maxPart = max(maxPart, elapsed)
		maxBytes = max(maxBytes, w.Body.Len())
		allModels += strings.Count(w.Body.String(), `"state":"done"`)
	}
	if allModels != 115675 {
		t.Fatal("scoped reads dropped or duplicated models", allModels)
	}
	t.Logf("account-scoped complete refresh: %s, max_part=%s max_bytes=%d models=%d", time.Since(start), maxPart, maxBytes, allModels)
	// Simulate an upgraded installation whose historical projections do not
	// exist yet. Verify bounded progress over the full incident-sized dataset.
	exec(`DELETE FROM console_sub_model_summaries WHERE account_id IN (SELECT id FROM console_sub_accounts WHERE owner=$1)`, owner)
	start = time.Now()
	var cursor subModelSummaryCursor
	filled, batches := 0, 0
	for {
		next, n, done, err := store.backfillSubModelSummaries(context.Background(), cursor)
		if err != nil {
			t.Fatal(err)
		}
		cursor, filled, batches = next, filled+n, batches+1
		if done {
			break
		}
	}
	if filled != 115675 {
		t.Fatal("historical projection incomplete", filled)
	}
	t.Logf("bounded historical backfill (without production pauses): %s, %d rows, %d batches", time.Since(start), filled, batches)
}

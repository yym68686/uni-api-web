package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSiteSyncBatchCooldownKeepsReadyGroupsMoving(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop=%v", stop), func(t *testing.T) {
			s, id, owner := newAPITestService(t)
			ctx := context.Background()
			db := s.control.db
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := db.Exec(q, args...); err != nil {
					t.Fatal(err)
				}
			}
			ready, _ := s.newAPIGroupID(ctx, id, "ready")
			waiting, _ := s.newAPIGroupID(ctx, id, "waiting")
			enc, _ := s.control.encrypt("sk-old-fixture")
			exec(`UPDATE console_sub_accounts SET state='queued',job_kind='sync',job_id='',sync_models='["fixture-model"]',probe_limits='{"rpm_limit":480}' WHERE id=$1`, id)
			exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,encrypted_key) VALUES($1,$2,'ready','openai',11,$3) ON CONFLICT(account_id,group_id) DO UPDATE SET remote_key_id=excluded.remote_key_id,encrypted_key=excluded.encrypted_key`, id, ready, enc)
			var batches, readyCalls, waitingCalls atomic.Int32
			withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/user/self/groups":
					newAPIOK(w, map[string]any{"ready": map[string]any{"ratio": 1}, "waiting": map[string]any{"ratio": 1}})
				case "/api/user/models":
					newAPIOK(w, []string{"fixture-model", "catalog-only"})
				case "/api/token/":
					if r.Method != "GET" {
						t.Error("unexpected create")
					}
					newAPIOK(w, map[string]any{"total": 2, "items": []newAPIToken{
						{ID: 11, UserID: 7, Name: subKeyName(id, ready), Group: "ready", Status: 1, Unlimited: true},
						{ID: 12, UserID: 7, Name: subKeyName(id, waiting), Group: "waiting", Status: 1, Unlimited: true},
					}})
				case newAPIBatchKeyPath:
					if batches.Add(1) == 1 {
						w.Header().Set("Retry-After", "1159")
						w.WriteHeader(429)
						return
					}
					newAPIOK(w, map[string]any{"keys": map[int]string{12: "sk-new-fixture"}})
				case "/v1/responses":
					if r.Header.Get("Authorization") == "Bearer sk-old-fixture" {
						readyCalls.Add(1)
					} else if r.Header.Get("Authorization") == "Bearer sk-new-fixture" {
						waitingCalls.Add(1)
					} else {
						t.Error("wrong probe key")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"test\"}\n\n")
				default:
					t.Errorf("unexpected upstream %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			if !s.subWorkOne(ctx) {
				t.Fatal("not claimed")
			}
			var phase, job string
			var leaseFree bool
			var retry time.Time
			if err := db.QueryRow(`SELECT sync_phase,sync_run_id,sync_lease_until IS NULL AND job_id='' AND sync_retry_at IS NULL,sync_resume_at FROM console_sub_accounts WHERE id=$1`, id).Scan(&phase, &job, &leaseFree, &retry); err != nil {
				t.Fatal(err)
			}
			if phase != "waiting_keys" || !leaseFree || time.Until(retry) < 19*time.Minute || readyCalls.Load() != 1 || waitingCalls.Load() != 0 {
				t.Fatal(phase, leaseFree, retry, readyCalls.Load(), waitingCalls.Load())
			}
			if _, err := s.subClaimJob(ctx); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("claimed during cooldown", err)
			}
			var planned, complete int
			if err := db.QueryRow(`SELECT count(*),count(*) FILTER(WHERE state='done') FROM console_sub_models WHERE account_id=$1 AND sync_run_id=$2`, id, job).Scan(&planned, &complete); err != nil || planned != 2 || complete != 1 {
				t.Fatal(planned, complete, err)
			}
			if stop {
				token, _ := s.control.newSession(ctx, owner)
				req := httptest.NewRequest("POST", "/v1/sub2api/accounts/"+id+"/stop", nil)
				req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
				rec := httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, req)
				if rec.Code != 200 {
					t.Fatal(rec.Code, rec.Body.String())
				}
			}
			exec(`UPDATE console_sub_accounts SET sync_resume_at=now() WHERE id=$1 AND state='running'`, id)
			exec(`UPDATE console_site_detection_limits SET retry_at=now(),next_at=now()`)
			// A new Service executes the due durable job, not process memory.
			next := &Service{control: s.control}
			if got := next.subWorkOne(ctx); got == stop {
				t.Fatal("incorrect resume", got, stop)
			}
			if readyCalls.Load() != 1 || waitingCalls.Load() != map[bool]int32{true: 0, false: 1}[stop] || batches.Load() != map[bool]int32{true: 1, false: 2}[stop] {
				t.Fatal("completed model replayed or missing work", readyCalls.Load(), waitingCalls.Load(), batches.Load())
			}
		})
	}
}

func TestSiteProbeRateRejectionPreservesStoredResult(t *testing.T) {
	s, id, _ := newAPITestService(t)
	ctx := context.Background()
	db := s.control.db
	enc, _ := s.control.encrypt("sk-fixture")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE console_sub_accounts SET state='running',job_id='job',sync_run_id='job' WHERE id=$1`, id)
	exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key,remote_key_id,sync_run_id,sync_key_state) VALUES($1,912,'ready','openai',$2,77,'job','ready')`, id, enc)
	exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result,sync_run_id) VALUES($1,912,'fixture-model','queued','{"availability":{"status":"success","text":"prior"}}','job')`, id)
	var calls atomic.Int32
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":{"code":"rate_limit_exceeded"}}`)
	}))
	ctx = context.WithValue(ctx, subSyncPlanKey{}, &subSyncPlan{run: "job"})
	err := s.subTestTargets(ctx, id, "https://usage.example", "job", false)
	var pause *subSyncPause
	if !errors.As(err, &pause) || pause.phase != "waiting_models" || time.Until(pause.until) < 59*time.Second {
		t.Fatal(err, pause)
	}
	var state string
	var raw []byte
	if err := db.QueryRow(`SELECT state,result FROM console_sub_models WHERE account_id=$1 AND group_id=912`, id).Scan(&state, &raw); err != nil || state != "queued" || !strings.Contains(string(raw), "prior") {
		t.Fatal(state, string(raw), err)
	}
	_ = s.subTestTargets(ctx, id, "https://usage.example", "job", false)
	if calls.Load() != 1 {
		t.Fatal("cooldown did not prevent second upstream call", calls.Load())
	}
}

func TestSiteSyncScopeProgressUsesPlannedCombinations(t *testing.T) {
	s, id, owner := newAPITestService(t)
	db := s.control.db
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE console_sub_accounts SET state='running',job_kind='sync',job_id='job',sync_run_id='job',sync_models='["one","two"]' WHERE id=$1`, id)
	exec(`UPDATE console_sub_targets SET active=false WHERE account_id=$1`, id)
	var groups []subRemoteGroup
	for id := int64(100); id < 129; id++ {
		groups = append(groups, subRemoteGroup{ID: id})
	}
	for _, g := range groups {
		exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,active,state,sync_run_id,sync_key_state) VALUES($1,$2,'group','openai',true,'queued','job','ready')`, id, g.ID)
	}
	exec(`INSERT INTO console_channel_detection_settings(owner,scope,account_id,group_id) VALUES($1,$3,$2,100)`, owner, id, (detectionTarget{Account: id, Group: 100}).scope())
	exec(`UPDATE console_sub_targets SET state='done' WHERE account_id=$1 AND group_id=100`, id)
	ctx = context.WithValue(withSubSyncModels(ctx, []string{"one", "two"}), subSyncPlanKey{}, &subSyncPlan{run: "job"})
	if err := queueSubSyncModels(ctx, tx, id, groups, nil); err != nil {
		t.Fatal(err)
	}
	accounts, err := readSubAccountHeaders(ctx, tx, owner, id)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/v1/sub2api/accounts/progress", nil).WithContext(ctx)
	if err := readSubSyncProgress(r, tx, owner, accounts, []string{id}); err != nil {
		t.Fatal(err)
	}
	if p := accounts[0].Progress; p.Total != 56 || p.Queued != 56 || p.KeysTotal != 29 || p.KeysReady != 29 {
		t.Fatal(p)
	}
	if *accounts[0].CompletedGroups != 1 {
		t.Fatal("disabled detection group should still count as synchronized", *accounts[0].CompletedGroups)
	}
}

func TestSiteProbeRejectionVersusQuota(t *testing.T) {
	for code, want := range map[string]string{"insufficient_quota": "quota_rejected", "rate_limit_exceeded": "rate_limited", "unknown": "unclassified_429"} {
		w := httptest.NewRecorder()
		w.WriteHeader(429)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code}})
		if got := probeHTTPRejection(w.Result()); got != want {
			t.Fatal(code, got, want)
		}
	}
}

func TestSiteProbeBudgetsApplyAccountAndGroupLimits(t *testing.T) {
	s, id, _ := newAPITestService(t)
	ctx := context.Background()
	db := s.control.db
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE console_sub_accounts SET probe_limits='{"declared_rpm":100,"declared_concurrency":5,"rpm_limit":90,"concurrency":2}' WHERE id=$1`, id)
	exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,probe_rpm) VALUES($1,912,'limited','openai',30)`, id)
	b, err := s.readSiteModelBudget(ctx, id, 912)
	if err != nil || b.rpm != 80 || b.concurrency != 2 || b.groupRPM != 24 || b.adaptive {
		t.Fatal(b, err)
	}
	first, err := s.acquireSiteDetection(ctx, b, "waiting_models")
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	wait, err := s.trySiteDetection(ctx, b, "waiting_models", "blocked")
	if err != nil || wait < 1700*time.Millisecond {
		t.Fatal("group did not enforce its slower RPM", wait, err)
	}
	other := siteModelBudget{scope: siteModelScope(id + "-other"), rpm: 480, concurrency: 1}
	release, err := s.acquireSiteDetection(ctx, other, "waiting_models")
	if err != nil {
		t.Fatal("other account shared throttle", err)
	}
	release()
	exec(`UPDATE console_site_detection_limits SET next_at=now() WHERE scope=$1 OR scope=$2`, b.scope, b.group)
	second, err := s.acquireSiteDetection(ctx, b, "waiting_models")
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	exec(`UPDATE console_site_detection_limits SET next_at=now() WHERE scope=$1 OR scope=$2`, b.scope, b.group)
	wait, err = s.trySiteDetection(ctx, b, "waiting_models", "blocked")
	if err != nil || wait != 250*time.Millisecond {
		t.Fatal("in-flight slots did not cap concurrent requests", wait, err)
	}
	second()
	if wait, err = s.trySiteDetection(ctx, b, "waiting_models", "replacement"); err != nil || wait != 0 {
		t.Fatal("slot wasn't returned", wait, err)
	}
	exec(`DELETE FROM console_site_detection_slots WHERE scope=$1 AND slot='replacement'`, b.scope)
}

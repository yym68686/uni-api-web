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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func handoffFixtureExec(t *testing.T, db *sql.DB, q string, args ...any) sql.Result {
	t.Helper()
	res, err := db.Exec(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func claimHandoffFixture(t *testing.T, s *Service, id, models string) subJob {
	t.Helper()
	db := s.control.db
	handoffFixtureExec(t, db, `UPDATE console_sub_accounts SET state='queued',job_kind='sync',sync_models=$2,probe_limits='{"rpm_limit":480,"concurrency":1}' WHERE id=$1`, id, models)
	job, err := s.subClaimJob(t.Context())
	if err != nil || job.id != id || job.run == "" || job.token == job.run {
		t.Fatal("initial claim", job.id, job.run, err)
	}
	handoffFixtureExec(t, db, `UPDATE console_sub_accounts SET sync_phase='probing',sync_plan_ready=true WHERE id=$1`, id)
	return job
}

func TestSiteSyncHandoffKeepsUnsentAndDoesNotReplay(t *testing.T) {
	for _, graceful := range []bool{true, false} {
		t.Run(fmt.Sprintf("graceful=%t", graceful), func(t *testing.T) {
			s, id, _ := newAPITestService(t)
			db := s.control.db
			old := claimHandoffFixture(t, s, id, `["a-done","b-inflight","c-unsent"]`)
			key, err := s.control.encrypt("sk-handoff-fixture")
			if err != nil {
				t.Fatal(err)
			}
			handoffFixtureExec(t, db, `INSERT INTO console_sub_targets(account_id,group_id,name,platform,state,encrypted_key,remote_key_id,sync_run_id,sync_key_state)
 VALUES($1,821,'group','openai','queued',$2,31,$3,'ready')`, id, key, old.run)
			handoffFixtureExec(t, db, `INSERT INTO console_sub_models(account_id,group_id,model,state,result,sync_run_id)
 VALUES($1,821,'a-done','done','{"checked_at":123,"verdict":"preserve"}',$2),
 ($1,821,'b-inflight','queued','{"checked_at":111,"verdict":"previous"}',$2),
 ($1,821,'c-unsent','queued',NULL,$2)`, id, old.run)

			var mu sync.Mutex
			calls := map[string]int{}
			arrived := make(chan struct{})
			var first sync.Once
			withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct{ Model string }
				if r.URL.Path != "/v1/responses" || json.NewDecoder(r.Body).Decode(&req) != nil {
					t.Errorf("unexpected upstream request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				mu.Lock()
				calls[req.Model]++
				mu.Unlock()
				switch req.Model {
				case "b-inflight":
					first.Do(func() { close(arrived) })
					<-r.Context().Done()
				case "c-unsent":
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"test\"}\n\n")
				default:
					t.Errorf("completed model replayed: %q", req.Model)
					http.NotFound(w, r)
				}
			}))

			if graceful {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan struct{})
				go func() { defer close(done); s.subRunJob(ctx, old) }()
				select {
				case <-arrived:
				case <-time.After(8 * time.Second):
					t.Fatal("in-flight probe did not reach upstream")
				}
				cancel()
				select {
				case <-done:
				case <-time.After(8 * time.Second):
					t.Fatal("old worker did not release its lease")
				}
			} else {
				if n, err := s.subClaimModel(t.Context(), id, old.token, "b-inflight", 821); err != nil || n != 1 {
					t.Fatal("prepare in-flight state", n, err)
				}
				handoffFixtureExec(t, db, `UPDATE console_sub_accounts SET sync_lease_until=now()-interval '1 second' WHERE id=$1`, id)
				if n, err := s.subClaimModel(t.Context(), id, old.token, "c-unsent", 821); err != nil || n != 0 {
					t.Fatal("expired worker could still claim", n, err)
				}
				// A mixed-version rollout must not let legacy expiry mark this plan interrupted.
				res := handoffFixtureExec(t, db, `UPDATE console_sub_accounts SET state='interrupted' WHERE id=$1 AND state='running' AND lease_until<now()`, id)
				if n, _ := res.RowsAffected(); n != 0 {
					t.Fatal("modern lease visible to old worker")
				}
				if err := s.subExpireJobs(t.Context()); err != nil {
					t.Fatal(err)
				}
			}

			var retained, sent, unsent int
			if err := db.QueryRow(`SELECT count(*) FILTER(WHERE state='done' AND result->>'checked_at'='123'),
 count(*) FILTER(WHERE state='interrupted' AND result->>'checked_at'='111'),count(*) FILTER(WHERE state='queued')
 FROM console_sub_models WHERE account_id=$1 AND sync_run_id=$2`, id, old.run).Scan(&retained, &sent, &unsent); err != nil || retained != 1 || sent != 1 || unsent != 1 {
				t.Fatal("handoff must retain only safe work", retained, sent, unsent, err)
			}
			next := &Service{control: s.control}
			resumed, err := next.subClaimJob(t.Context())
			if err != nil || resumed.run != old.run || resumed.token == old.token || resumed.phase != "handoff" {
				t.Fatal("wrong resumed lease", resumed.run, resumed.phase, err)
			}
			if _, err := s.subClaimJob(t.Context()); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("another worker claimed the same account", err)
			}
			if n, err := s.subClaimModel(t.Context(), id, old.token, "c-unsent", 821); err != nil || n != 0 {
				t.Fatal("old worker can write after handoff", n, err)
			}
			next.subRunJob(t.Context(), resumed)
			mu.Lock()
			if calls["a-done"] != 0 || calls["b-inflight"] != map[bool]int{true: 1, false: 0}[graceful] || calls["c-unsent"] != 1 {
				t.Error("unexpected paid probe counts", calls)
			}
			mu.Unlock()
			var state, message string
			if err := db.QueryRow(`SELECT state,message FROM console_sub_accounts WHERE id=$1`, id).Scan(&state, &message); err != nil ||
				state != "idle" || !strings.Contains(message, "未自动重发") {
				t.Fatal("missing partial result notice", state, message, err)
			}
		})
	}
}

func TestSiteSyncHandoffCapabilityStagesAreNotRequeued(t *testing.T) {
	s, id, _ := newAPITestService(t)
	db := s.control.db
	job := claimHandoffFixture(t, s, id, `["gpt-fixture"]`)
	for group := 900; group < 903; group++ {
		handoffFixtureExec(t, db, `INSERT INTO console_sub_targets(account_id,group_id,name,platform,state,sync_run_id,sync_key_state)
 VALUES($1,$2,'group','openai','done',$3,'ready')`, id, group, job.run)
		handoffFixtureExec(t, db, `INSERT INTO console_sub_models(account_id,group_id,model,state,result,sync_run_id)
 VALUES($1,$2,'gpt-fixture','done','{"availability":{"status":"success"}}',$3)`, id, group, job.run)
	}
	ctx := t.Context()
	for _, stage := range []string{"compaction", "tool_use"} {
		if err := s.subQueueSyncCapabilities(ctx, id, job.token, stage); err != nil {
			t.Fatal(err)
		}
		handoffFixtureExec(t, db, `UPDATE console_sub_targets SET `+stage+`_state=CASE group_id WHEN 900 THEN 'done' ELSE 'running' END
 WHERE account_id=$1 AND group_id IN (900,901)`, id)
		if stage == "tool_use" {
			handoffFixtureExec(t, db, `UPDATE console_sub_targets SET tool_use='{"models":[{"state":"done"},{"state":"running"},{"state":"queued"}]}' WHERE account_id=$1 AND group_id=901`, id)
		}
		if err := s.subSuspendSync(ctx, id, job.token); err != nil {
			t.Fatal(err)
		}
		next, err := s.subClaimJob(ctx)
		if err != nil || next.phase != stage || next.run != job.run || next.token == job.token {
			t.Fatal("capability checkpoint lost", next.phase, err)
		}
		job = next
		if err := s.subQueueSyncCapabilities(ctx, id, job.token, stage); err != nil {
			t.Fatal(err)
		}
		var done, interrupted, queued int
		if err = db.QueryRow(`SELECT count(*) FILTER(WHERE `+stage+`_state='done'),count(*) FILTER(WHERE `+stage+`_state='interrupted'),count(*) FILTER(WHERE `+stage+`_state='queued')
 FROM console_sub_targets WHERE account_id=$1 AND sync_run_id=$2`, id, job.run).Scan(&done, &interrupted, &queued); err != nil || done != 1 || interrupted != 1 || queued != 1 {
			t.Fatal(stage, done, interrupted, queued, err)
		}
	}
	var modelStates string
	if err := db.QueryRow(`SELECT (SELECT string_agg(m->>'state',',') FROM jsonb_array_elements(tool_use->'models') m) FROM console_sub_targets WHERE account_id=$1 AND group_id=901`, id).Scan(&modelStates); err != nil || modelStates != "done,interrupted,interrupted" {
		t.Fatal("stale per-model tool progress", modelStates, err)
	}
}

func TestSiteDetectionDrainBlocksNewClaims(t *testing.T) {
	s, id, _ := newAPITestService(t)
	db := s.control.db
	handoffFixtureExec(t, db, `UPDATE console_sub_accounts SET state='queued',job_kind='sync' WHERE id=$1`, id)
	var calls atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/metrics" {
			t.Errorf("not a read-only sidecar metric: %s %s", r.Method, r.URL.Path)
		}
		calls.Add(1)
		fmt.Fprintln(w, "# TYPE fugue_app_drain_prestop_requests_total counter\nfugue_app_drain_prestop_requests_total 1")
	}))
	defer agent.Close()
	s.subWorkerLoopWithDrain(t.Context(), func(ctx context.Context) bool { return subDrainRequested(ctx, agent.URL+"/metrics") })
	var state string
	if err := db.QueryRow(`SELECT state FROM console_sub_accounts WHERE id=$1`, id).Scan(&state); err != nil || state != "queued" || calls.Load() != 1 {
		t.Fatal("draining instance claimed new work", state, calls.Load(), err)
	}
}

func TestSiteDetectionCooldownExpiresWhileWaitingForLock(t *testing.T) {
	s, id, _ := newAPITestService(t)
	db := s.control.db
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	scope := "cooldown-lock:" + id
	t.Cleanup(func() { db.Exec(`DELETE FROM console_site_detection_limits WHERE scope=$1`, scope) })
	handoffFixtureExec(t, db, `INSERT INTO console_site_detection_limits(scope) VALUES($1)`, scope)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT scope FROM console_site_detection_limits WHERE scope=$1 FOR UPDATE`, scope); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := s.trySiteDetection(ctx, siteModelBudget{scope: scope, rpm: 60}, "waiting_models", "")
		result <- err
	}()
	for {
		var waiting bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid()
 AND wait_event_type='Lock' AND query LIKE '%console_site_detection_limits%' AND application_name='uni-api-control')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if !waitStartup(ctx, 10*time.Millisecond) {
			t.Fatal("probe never waited on the budget row")
		}
	}
	// Set a deadline later than the probe's pre-lock timestamp but already
	// passed when the probe can take the lock.
	if _, err := tx.Exec(`UPDATE console_site_detection_limits SET retry_at=clock_timestamp() WHERE scope=$1`, scope); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal("expired cooldown paused a live probe", err)
	}
}

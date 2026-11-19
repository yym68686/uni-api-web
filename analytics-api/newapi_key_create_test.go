package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewAPIKeyCreateBackoffSharedAcrossWorkers(t *testing.T) {
	s, id, _ := newAPITestService(t)
	const base = "https://usage.example"
	db := s.control.db
	scope := newAPIKeyCreateScope(base)
	other := &Service{control: s.control}
	ctx := context.Background()
	var calls atomic.Int32
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 3:
			w.Header().Set("Retry-After", "900")
		case 4:
			newAPIOK(w, nil)
			return
		case 6:
			w.WriteHeader(502)
			return
		}
		w.WriteHeader(429)
	}))
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	expire := func() {
		exec(`UPDATE console_site_detection_limits SET retry_at=now(),next_at=now() WHERE scope=$1`, scope)
	}
	for i, secs := range []int{30, 60, 900} {
		err := s.newAPICreateOwnedKey(ctx, id, base, "", "fixture-key", "default")
		var pause *subSyncPause
		delay := time.Duration(secs) * time.Second
		if !errors.As(err, &pause) || pause.local || pause.phase != waitingKeyCreate || time.Until(pause.until) < delay-2*time.Second || time.Until(pause.until) > delay+2*time.Second {
			t.Fatalf("attempt %d (%v): %v", i, delay, err)
		}
		// Different path, Service and account; the persisted host cooldown
		// short-circuits before reserving a creation intent or reaching HTTP.
		err = other.newAPICreateOwnedKey(ctx, "another-account", base+"/panel", "", "different", "default")
		var shadow *subSyncPause
		if !errors.As(err, &shadow) || !shadow.local || !shadow.until.Equal(pause.until) || calls.Load() != int32(i+1) {
			t.Fatalf("shared cooldown not enforced: %v (%d calls)", err, calls.Load())
		}
		expire()
	}
	gap := 20 * time.Second
	s.newAPIKeyCreateGap = &gap
	if err := s.newAPICreateOwnedKey(ctx, id, base, "", "fixture-key", "default"); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// A reservation alone (without a 429) is shared as well.
	err := other.newAPICreateOwnedKey(short, id, base, "", "next-key", "default")
	var pause *subSyncPause
	if !errors.As(err, &pause) || !pause.local || time.Until(pause.until) < 15*time.Second || calls.Load() != 4 {
		t.Fatal("shared pacing not enforced", err, calls.Load())
	}
	expire()
	err = s.newAPICreateOwnedKey(ctx, id, base, "", "next-key", "default")
	if !errors.As(err, &pause) || pause.local || time.Until(pause.until) < 28*time.Second || time.Until(pause.until) > 32*time.Second {
		t.Fatal("success did not reset the backoff", err)
	}
	expire()
	if err := s.newAPICreateOwnedKey(ctx, id, base, "", "next-key", "default"); !errors.Is(err, errNewAPIKeyCreateUnconfirmed) {
		t.Fatal(err)
	}
	expire()
	if err := s.newAPICreateOwnedKey(ctx, id, base, "", "next-key", "default"); !errors.Is(err, errNewAPIKeyCreatePending) || calls.Load() != 6 {
		t.Fatal("uncertain write replayed", err, calls.Load())
	}
}

func TestNewAPISiteSyncCreateResumesWithoutReplaying(t *testing.T) {
	s, id, owner := newAPITestService(t)
	db := s.control.db
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE console_sub_accounts SET state='queued',job_kind='sync',sync_models='["fixture-model"]',probe_limits='{"rpm_limit":480}' WHERE id=$1`, id)
	tokens := []newAPIToken{} // The key branch alone reads and writes these.
	var readyProbes, resumeProbes atomic.Int32
	var badCreates, resumeCreates atomic.Int32
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self/groups":
			newAPIOK(w, map[string]any{"a-ready": map[string]any{"ratio": 1}, "b-invalid": map[string]any{"ratio": 1}, "c-resume": map[string]any{"ratio": 1}})
		case "/api/user/models":
			newAPIOK(w, []string{"fixture-model"})
		case "/api/user/self":
			newAPIOK(w, newAPIUser{ID: 7})
		case "/api/token/":
			if r.Method == "GET" {
				newAPIOK(w, map[string]any{"total": len(tokens), "items": tokens})
				return
			}
			var v newAPIToken
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			switch v.Group {
			case "b-invalid":
				badCreates.Add(1)
				w.WriteHeader(400)
				return
			case "c-resume":
				if resumeCreates.Add(1) <= 2 {
					w.WriteHeader(429)
					return
				}
			}
			v.ID, v.UserID, v.Status, v.Key = int64(len(tokens)+1), 7, 1, "sk-"+v.Group
			tokens = append(tokens, v)
			newAPIOK(w, nil)
		case "/v1/responses":
			switch r.Header.Get("Authorization") {
			case "Bearer sk-a-ready":
				readyProbes.Add(1)
			case "Bearer sk-c-resume":
				resumeProbes.Add(1)
			default:
				t.Error("unexpected model key")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"test\"}\n\n")
		default:
			t.Errorf("unexpected upstream %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	for i := 1; i <= 2; i++ {
		if !s.subWorkOne(ctx) {
			t.Fatal("sync wasn't claimed")
		}
		a, err := readSubAccountHeaders(ctx, db, owner, id)
		if err != nil || len(a) != 1 || a[0].SyncPhase != waitingKeyCreate || a[0].SyncRetryAt <= time.Now().Unix() {
			t.Fatalf("unexpected account progress: %+v, %v", a, err)
		}
		var phase string
		var retry time.Time
		var keyPauses, modelPauses int
		if err := db.QueryRow(`SELECT sync_phase,sync_resume_at,sync_key_pause_count,sync_pause_count FROM console_sub_accounts WHERE id=$1`, id).Scan(&phase, &retry, &keyPauses, &modelPauses); err != nil {
			t.Fatal(err)
		}
		if phase != waitingKeyCreate || time.Until(retry) < time.Duration(30*i-2)*time.Second || keyPauses != i || modelPauses != 0 ||
			readyProbes.Load() != 1 || resumeProbes.Load() != 0 || badCreates.Load() != 1 {
			t.Fatal(phase, retry, keyPauses, modelPauses, readyProbes.Load(), resumeProbes.Load(), badCreates.Load())
		}
		if _, err := s.subClaimJob(ctx); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("claimed during cooldown", err)
		}
		exec(`UPDATE console_sub_accounts SET sync_resume_at=now() WHERE id=$1`, id)
		exec(`UPDATE console_site_detection_limits SET retry_at=now(),next_at=now() WHERE scope=$1`, newAPIKeyCreateScope("https://usage.example"))
		s = &Service{control: s.control, newAPIKeyCreateGap: s.newAPIKeyCreateGap}
	}
	if !s.subWorkOne(ctx) || readyProbes.Load() != 1 || resumeProbes.Load() != 1 || badCreates.Load() != 1 {
		t.Fatal("resume did not preserve completed work", readyProbes.Load(), resumeProbes.Load(), badCreates.Load())
	}
	var done, rejected int
	if err := db.QueryRow(`SELECT count(*) FILTER(WHERE state='done'),count(*) FILTER(WHERE state='interrupted')
 FROM console_sub_models WHERE account_id=$1`, id).Scan(&done, &rejected); err != nil || done != 2 || rejected != 1 {
		t.Fatal(done, rejected, err)
	}
}

func TestSiteSyncPauseBudgetsAreIndependentAndResetOnProgress(t *testing.T) {
	s, id, _ := newAPITestService(t)
	db := s.control.db
	ctx := context.Background()
	const lease = "fixture-lease"
	key := &subSyncPause{phase: waitingKeyCreate, until: time.Now().Add(time.Minute)}
	model := &subSyncPause{phase: "waiting_models", until: key.until.Add(time.Minute)}
	local := &subSyncPause{phase: waitingKeyCreate, until: key.until, local: true}
	for _, tc := range []struct {
		name                 string
		suspension           subSyncSuspension
		keyStart, modelStart int
		wantKeys, wantModels int
		wantSaved            int64
		wantPhase            string
	}{
		{"keys have their own budget", subSyncSuspension{key: key}, 3, 4, 4, 4, 1, waitingKeyCreate},
		{"models have their own budget", subSyncSuspension{model: model}, 6, 0, 6, 1, 1, "waiting_models"},
		{"overlap uses later wakeup", subSyncSuspension{key: key, model: model}, 2, 3, 3, 4, 1, "waiting_models"},
		{"both branches made progress", subSyncSuspension{key: key, model: model, keyAdvanced: true, modelAdvanced: true}, 6, 4, 1, 1, 1, "waiting_models"},
		{"a local wait doesn't charge a retry", subSyncSuspension{key: local}, 6, 4, 6, 4, 1, waitingKeyCreate},
		{"no progress eventually stops", subSyncSuspension{key: key}, 6, 0, 6, 0, 0, "preparing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE console_sub_accounts SET state='running',job_kind='sync',job_id=$2,sync_phase='preparing',
 sync_lease_until=now()+interval '5 minutes',sync_key_pause_count=$3,sync_pause_count=$4 WHERE id=$1`, id, lease, tc.keyStart, tc.modelStart); err != nil {
				t.Fatal(err)
			}
			n, err := s.subPauseSyncJob(ctx, id, lease, &tc.suspension)
			if err != nil || n != tc.wantSaved {
				t.Fatal(n, err)
			}
			var keys, models int
			var phase string
			if err := db.QueryRow(`SELECT sync_key_pause_count,sync_pause_count,sync_phase FROM console_sub_accounts WHERE id=$1`, id).Scan(&keys, &models, &phase); err != nil || keys != tc.wantKeys || models != tc.wantModels || phase != tc.wantPhase {
				t.Fatal(keys, models, phase, err)
			}
		})
	}
}

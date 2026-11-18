package main

import (
	"context"
	"testing"
	"time"
)

func TestManagementFingerprintHeartbeatEpoch(t *testing.T) {
	db := migrationTestDB(t)
	if err := migrateControlSchema(context.Background(), db, controlMigrations()); err != nil {
		t.Fatal(err)
	}
	epoch := func() int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(`SELECT revision FROM console_management_view_epoch WHERE relation_name='console_model_fingerprints'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, step := range []struct {
		query string
		bump  int64
	}{
		{`INSERT INTO console_model_fingerprints(scope,model,state,deadline) VALUES ('live','m','running',now()+interval '30s'),('expired','m','running',now()-interval '10s')`, 1},
		{`UPDATE console_model_fingerprints SET deadline=deadline+interval '5s' WHERE scope='live'`, 0},
		{`UPDATE console_model_fingerprints SET deadline=deadline+interval '5s',sample_state='{"entries":[{}]}' WHERE scope='live'`, 1},
		{`UPDATE console_model_fingerprints SET deadline=deadline-interval '1s' WHERE scope='live'`, 1},
		{`UPDATE console_model_fingerprints SET deadline=now()+interval '30s' WHERE scope='expired'`, 1},
		{`UPDATE console_model_fingerprints SET deadline=deadline+interval '2s',state=CASE WHEN scope='live' THEN 'interrupted' ELSE state END`, 1},
		{`UPDATE console_model_fingerprints SET deadline=deadline+interval '2s' WHERE scope='live'`, 1},
		{`UPDATE console_model_fingerprints SET model='new' WHERE scope='live'`, 1},
		{`DELETE FROM console_model_fingerprints WHERE scope='live'`, 1},
	} {
		before := epoch()
		if _, err := db.Exec(step.query); err != nil {
			t.Fatal(err)
		}
		if got := epoch() - before; got != step.bump {
			t.Fatalf("wanted epoch advance %d, got %d after %s", step.bump, got, step.query)
		}
	}
}

func TestManagementDeadlineOnlyMovesForwardWhileLive(t *testing.T) {
	now := time.Now()
	earlier, later := now.Add(time.Second), now.Add(2*time.Second)
	if !managementDeadlineUnchanged(earlier, later, now) ||
		managementDeadlineUnchanged(earlier, later, earlier) ||
		managementDeadlineUnchanged(later, earlier, now) ||
		managementDeadlineUnchanged(time.Time{}, later, now) ||
		managementDeadlineUnchanged(later, time.Time{}, now) {
		t.Fatal("invalid live deadline equivalence")
	}
}

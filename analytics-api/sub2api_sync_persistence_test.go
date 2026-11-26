package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func syncPlanFixture(t *testing.T) (*sql.DB, *Service, context.Context) {
	t.Helper()
	db := migrationTestDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	t.Cleanup(cancel)
	if err := migrateControlSchema(ctx, db, controlMigrations()); err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, subSyncPlanKey{}, &subSyncPlan{run: "run"})
	ctx = withSubSyncModels(ctx, []string{"gpt-5.5"})
	return db, &Service{control: &controlStore{db: db}}, ctx
}

func TestSubSyncPlanRetriesOnlyRolledBackWrites(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		attempts   int
		success    bool
	}{
		{"deadlock", "40P01", 2, true},
		{"serialization", "40001", 2, true},
		{"constraint", "23514", 1, false},
		{"exhaustion", "40P01", subSyncPlanWriteAttempts, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, service, ctx := syncPlanFixture(t)
			_, err := db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth,job_id,state)
 VALUES('fixture','owner','site','https://fixture.test','fixture','', 'job','running');
 INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('fixture',1,'old','openai'),('fixture',2,'missing','openai');
 CREATE SEQUENCE sync_write_attempt`)
			if err != nil {
				t.Fatal(err)
			}
			condition := "nextval('sync_write_attempt') = 1"
			if tc.name == "exhaustion" {
				condition = "nextval('sync_write_attempt') > 0"
			}
			// Raise after the target SQL has run. Sequence increments survive
			// rollbacks, letting the test count the actual plan attempts.
			if _, err = db.Exec(`CREATE FUNCTION fail_plan_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF ` + condition + ` THEN RAISE EXCEPTION USING ERRCODE='` + tc.code + `', MESSAGE='fixture detail'; END IF; RETURN NULL; END $$;
 CREATE TRIGGER zz_fixture_plan AFTER INSERT ON console_sub_models FOR EACH STATEMENT EXECUTE FUNCTION fail_plan_fixture()`); err != nil {
				t.Fatal(err)
			}
			upstream := 0
			panel := func(method, path string, _, _ any, _ string) error {
				upstream++
				return nil
			}
			groups := []subRemoteGroup{{ID: 1, Name: "updated", Platform: "openai", Rate: 1.2}, {ID: 3, Name: "new", Platform: "openai"}}
			err = service.subDiscoverSync(ctx, "fixture", "https://fixture.test", "job", groups, panel, false)
			if tc.success && err != nil || !tc.success && (err == nil || err.Error() != "模型保存失败") {
				t.Fatalf("unexpected sync outcome: %v", err)
			}
			var attempts, selected int
			var ready, missingActive, hasNew bool
			var name string
			if err = db.QueryRow(`SELECT
 (SELECT last_value FROM sync_write_attempt),
 (SELECT sync_plan_ready FROM console_sub_accounts WHERE id='fixture'),
 (SELECT name FROM console_sub_targets WHERE account_id='fixture' AND group_id=1),
 (SELECT active FROM console_sub_targets WHERE account_id='fixture' AND group_id=2),
 EXISTS(SELECT 1 FROM console_sub_targets WHERE account_id='fixture' AND group_id=3 AND active),
 (SELECT count(*) FROM console_sub_models WHERE account_id='fixture' AND state='queued' AND sync_run_id='run')`).
				Scan(&attempts, &ready, &name, &missingActive, &hasNew, &selected); err != nil {
				t.Fatal(err)
			}
			if attempts != tc.attempts || upstream != 2 {
				t.Fatalf("DB attempts=%d, upstream reads=%d", attempts, upstream)
			}
			if tc.success {
				if !ready || name != "updated" || missingActive || !hasNew || selected != 2 {
					t.Fatalf("wrong committed plan: %t %q %t %t %d", ready, name, missingActive, hasNew, selected)
				}
			} else if ready || name != "old" || !missingActive || hasNew || selected != 0 {
				t.Fatalf("rolled-back plan leaked: %t %q %t %t %d", ready, name, missingActive, hasNew, selected)
			}
		})
	}
}

func TestSubSyncPlanRechecksJobAfterWaitingAcrossReplicas(t *testing.T) {
	db, service, ctx := syncPlanFixture(t)
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth,job_id,state)
 VALUES('fixture','owner','site','https://fixture.test','fixture','','old-job','running')`); err != nil {
		t.Fatal(err)
	}
	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err = writer.Exec(`SELECT pg_advisory_xact_lock($1)`, subSyncPlanWriteLock); err != nil {
		t.Fatal(err)
	}
	planDone := make(chan error, 1)
	go func() {
		planDone <- service.subSaveDiscoveredSync(ctx, "fixture", "old-job", []subRemoteGroup{{ID: 1, Name: "site"}}, nil, nil, false, nil)
	}()
	for {
		var blocked bool
		if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted)`, subSyncPlanWriteLock).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-planDone:
			t.Fatalf("plan did not wait for the preceding replica: %v", err)
		default:
		}
		if !waitStartup(ctx, 5*time.Millisecond) {
			t.Fatal(ctx.Err())
		}
	}
	if _, err = writer.Exec(`UPDATE console_sub_accounts SET job_id='new-job' WHERE id='fixture'`); err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-planDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("stale plan must not proceed: %v", err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM console_sub_targets`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestSubSyncPlanEmptyDiscoveryDeactivatesOnlyOwnGroups(t *testing.T) {
	db, service, ctx := syncPlanFixture(t)
	if _, err := db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth,job_id,state)
 VALUES('mine','owner','mine','https://mine.test','fixture','','job','running'),
       ('other','owner','other','https://other.test','fixture','','other-job','running');
 INSERT INTO console_sub_targets(account_id,group_id,name,platform)
 VALUES('mine',1,'mine','openai'),('other',1,'other','openai')`); err != nil {
		t.Fatal(err)
	}
	if err := service.subSaveDiscoveredSync(ctx, "mine", "job", nil, nil, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	var active string
	if err := db.QueryRow(`SELECT string_agg(account_id, ',') FROM console_sub_targets WHERE active`).Scan(&active); err != nil || active != "other" {
		t.Fatal(active, err)
	}
	bad := []subRemoteGroup{{ID: 1, Name: "a"}, {ID: 1, Name: "b"}}
	if err := service.subSaveDiscoveredSync(ctx, "mine", "job", bad, nil, nil, false, nil); err == nil || !strings.Contains(err.Error(), "无效分组") {
		t.Fatalf("duplicate upstream ids were accepted: %v", err)
	}
}

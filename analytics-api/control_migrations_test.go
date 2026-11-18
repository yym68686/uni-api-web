package main

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func migrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	base, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "migration_" + strings.ToLower(randomID()[:12])
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = base.Exec("CREATE SCHEMA " + quoted); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		t.Fatal("test DSN must be a PostgreSQL URL")
	}
	q := u.Query()
	q.Set("search_path", quoted)
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		base.Exec("DROP SCHEMA " + quoted + " CASCADE")
		base.Close()
	})
	return db
}

func TestControlMigrationLockInventory(t *testing.T) {
	known := map[string]bool{}
	// Guard the explicit relation lock inventory against an omitted table.
	pattern := regexp.MustCompile(`(?i)(?:CREATE TABLE IF NOT EXISTS|ALTER TABLE|REFERENCES|UPDATE|INSERT INTO|FROM|JOIN)\s+(console_[a-z_]+)`)
	for _, migration := range controlMigrations() {
		clear(known)
		for _, table := range migration.Tables {
			known[table] = true
		}
		for _, match := range pattern.FindAllStringSubmatch(migration.SQL, -1) {
			if !known[match[1]] {
				t.Errorf("migration %s touches unlocked relation %s", migration.Version, match[1])
			}
		}
	}
}

func TestControlMigrationConcurrentAdoptionAndReadOnlyRestart(t *testing.T) {
	db := migrationTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Existing installations have all tables but no version ledger.
	if _, err := db.ExecContext(ctx, controlBaselineSQL); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := migrateControlSchema(ctx, db, controlMigrations()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM console_schema_migrations`).Scan(&count); err != nil || count != len(controlMigrations()) {
		t.Fatal("migration not recorded exactly once", count, err)
	}
	// An already applied startup must succeed even with an exclusive table
	// lock: replaying CREATE/ALTER or a backfill would block here.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`LOCK TABLE console_sub_accounts IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	fast, done := context.WithTimeout(ctx, 300*time.Millisecond)
	defer done()
	if err = migrateControlSchema(fast, db, controlMigrations()); err != nil {
		t.Fatal("restart repeated schema work", err)
	}
	changed := controlMigrations()
	changed[0].SQL += "\n-- changed history"
	if err = migrateControlSchema(ctx, db, changed); err == nil || !strings.Contains(err.Error(), "checksum changed") {
		t.Fatal("changed migration history accepted", err)
	}
}

func TestControlMigrationDoesNotWaitWithPartialLocks(t *testing.T) {
	db := migrationTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `CREATE TABLE accounts(id INT); CREATE TABLE usage(id INT)`); err != nil {
		t.Fatal(err)
	}
	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err = writer.Exec(`LOCK TABLE usage IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	migration := controlMigration{"test_locks", `ALTER TABLE accounts ADD COLUMN revision INT; ALTER TABLE usage ADD COLUMN revision INT;`, []string{"accounts", "usage"}}
	// The migrator takes accounts first. It must release that lock immediately
	// when usage is busy so the old writer can read accounts and commit.
	err = applyControlMigration(ctx, db, migration)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "55P03" {
		t.Fatal("expected NOWAIT rejection", err)
	}
	if _, err = writer.ExecContext(ctx, `SELECT * FROM accounts`); err != nil {
		t.Fatal("migrator retained partial locks", err)
	}
	waitCtx, stop := context.WithTimeout(ctx, 150*time.Millisecond)
	err = migrateControlSchema(waitCtx, db, []controlMigration{migration})
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("busy migration ignored cancellation", err)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = migrateControlSchema(ctx, db, []controlMigration{migration}); err != nil {
		t.Fatal("migration did not resume after writer finished", err)
	}
}

func TestControlMigrationRollsBackSchemaAndLedgerTogether(t *testing.T) {
	db := migrationTestDB(t)
	m := controlMigration{"test_rollback", `CREATE TABLE migration_partial(id INT); SELECT 1/0;`, nil}
	if err := migrateControlSchema(context.Background(), db, []controlMigration{m}); err == nil {
		t.Fatal("invalid migration succeeded")
	}
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('migration_partial') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatal("partial schema committed", exists, err)
	}
	m.SQL = `CREATE TABLE migration_partial(id INT);`
	if err := migrateControlSchema(context.Background(), db, []controlMigration{m}); err != nil {
		t.Fatal("failed migration left applied ledger record", err)
	}
}

func TestManagementDisplayEpochDoesNotBlockConcurrentModelProgress(t *testing.T) {
	db := migrationTestDB(t)
	db.SetMaxOpenConns(6)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := migrateControlSchema(ctx, db, controlMigrations()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth)
VALUES('epoch-lock','owner','fixture','https://fixture.test','fixture@example.com','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO console_sub_targets(account_id,group_id,name,platform)
VALUES('epoch-lock',1,'fixture','openai')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO console_sub_models(account_id,group_id,model,state)
VALUES('epoch-lock',1,'first','queued'),('epoch-lock',1,'second','queued')`); err != nil {
		t.Fatal(err)
	}
	first, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback()
	if _, err = first.ExecContext(ctx, `SELECT 1 FROM console_management_revision_state WHERE relation_name='console_sub_models' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	second, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback()
	var pid int
	if err = second.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, updateErr := second.ExecContext(ctx, `UPDATE console_sub_models SET state='done',result='{"availability":{"status":"success"}}' WHERE account_id='epoch-lock' AND model='second'`)
		if updateErr == nil {
			updateErr = second.Commit()
		}
		done <- updateErr
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err = db.QueryRowContext(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err = <-done:
			t.Fatalf("second writer did not wait on the existing revision lock: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("second writer never waited on the existing revision lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = first.ExecContext(ctx, `UPDATE console_sub_models SET state='running' WHERE account_id='epoch-lock' AND model='first'`); err != nil {
		t.Fatal("cache epoch blocked a concurrent model's progress", err)
	}
	if err = first.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal("waiting writer did not resume after commit", err)
		}
	case <-ctx.Done():
		t.Fatal("waiting writer did not resume after commit", ctx.Err())
	}
}

func TestManagementDisplayGlobalProofFencesLegacyCache(t *testing.T) {
	db := migrationTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	migrations := controlMigrations()
	// A rolling deployment can have a cache filled by the previous replica.
	proof := slices.IndexFunc(migrations, func(m controlMigration) bool { return m.Version == "0021_management_display_global_proof" })
	if proof < 0 {
		t.Fatal("missing display proof migration")
	}
	if err := migrateControlSchema(ctx, db, migrations[:proof]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO console_management_display_groups(owner_key,account_id,group_id,version,local_version,observed_at,raw_bytes,body)
VALUES('owner','a',1,'global-before','summary-v1:::',now(),2,'x')`); err != nil {
		t.Fatal(err)
	}
	// Reproduce the lock modes of the still-serving replica: a cache read
	// and writes to business tables/epoch rows cannot block this adoption.
	serving, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serving.Rollback()
	if _, err = serving.ExecContext(ctx, `SELECT 1 FROM console_management_display_groups;
LOCK TABLE console_management_display_epochs,console_sub_models,console_sub_usage IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	deadline, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	if err = migrateControlSchema(deadline, db, migrations); err != nil {
		t.Fatal("migration did not complete while the previous replica was serving", err)
	}
	if err = serving.Commit(); err != nil {
		t.Fatal(err)
	}
	var stale, normalized bool
	if err := db.QueryRowContext(ctx, `SELECT local_version<>'summary-v1::'||(SELECT writer::text FROM console_management_display_epochs WHERE kind='reset' AND account_id='' AND group_id=0)
FROM console_management_display_groups WHERE group_id=1`).Scan(&stale); err != nil || !stale {
		t.Fatal("legacy per-group proof remained valid", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO console_management_display_groups(owner_key,account_id,group_id,version,local_version,observed_at,raw_bytes,body)
VALUES('owner','a',2,'global-after','local-after',now(),2,'x')`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT local_version='' AND version='global-after'
FROM console_management_display_groups WHERE group_id=2`).Scan(&normalized); err != nil || !normalized {
		t.Fatal("rolling-deploy writer was not fenced by its global proof", err)
	}
}

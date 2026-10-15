package main

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"regexp"
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

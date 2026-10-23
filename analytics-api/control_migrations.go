package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Migration SQL and its lock inventory are immutable once released. Add a new
// entry for subsequent schema changes; never modify an already applied entry.
//
//go:embed migrations/0001_control.sql
var controlBaselineSQL string

//go:embed migrations/0001_control.tables
var controlBaselineTables string

//go:embed migrations/0002_fact_journal.sql
var factJournalSQL string

//go:embed migrations/0002_fact_journal.tables
var factJournalTables string

//go:embed migrations/0003_sub_model_summaries.sql
var subModelSummariesSQL string

//go:embed migrations/0003_sub_model_summaries.tables
var subModelSummariesTables string

//go:embed migrations/0004_receipt_retention.sql
var receiptRetentionSQL string

//go:embed migrations/0004_receipt_retention.tables
var receiptRetentionTables string

//go:embed migrations/0005_observed_rates.sql
var observedRatesSQL string

//go:embed migrations/0005_observed_rates.tables
var observedRatesTables string

//go:embed migrations/0006_management_signals.sql
var managementSignalsSQL string

//go:embed migrations/0006_management_signals.tables
var managementSignalsTables string

//go:embed migrations/0007_capability_summary.sql
var capabilitySummarySQL string

//go:embed migrations/0008_capability_projection.sql
var capabilityProjectionSQL string

type controlMigration struct {
	Version string
	SQL     string
	Tables  []string
}

func controlMigrations() []controlMigration {
	return []controlMigration{
		{"0001_control", controlBaselineSQL, strings.Fields(controlBaselineTables)},
		{"0002_fact_journal", factJournalSQL, strings.Fields(factJournalTables)},
		{"0003_sub_model_summaries", subModelSummariesSQL, strings.Fields(subModelSummariesTables)},
		{"0004_receipt_retention", receiptRetentionSQL, strings.Fields(receiptRetentionTables)},
		{"0005_observed_rates", observedRatesSQL, strings.Fields(observedRatesTables)},
		{"0006_management_signals", managementSignalsSQL, strings.Fields(managementSignalsTables)},
		{"0007_capability_summary", capabilitySummarySQL, nil},
		{"0008_capability_projection", capabilityProjectionSQL, []string{"console_sub_targets", "console_sub_capability_summaries"}},
	}
}

func (m controlMigration) checksum() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(m.SQL+"\n"+strings.Join(m.Tables, "\n"))))
}

const controlMigrationLock = 8395013

var errMigrationBusy = errors.New("control schema migration lock busy")

// Already migrated startups perform only reads. In particular, they do not
// repeat CREATE/ALTER statements or data backfills while old replicas write.
func migrateControlSchema(ctx context.Context, db *sql.DB, migrations []controlMigration) error {
	for _, migration := range migrations {
		if applied, err := controlMigrationApplied(ctx, db, migration); err != nil {
			return err
		} else if applied {
			continue
		}
		for attempt := 0; ; attempt++ {
			err := applyControlMigration(ctx, db, migration)
			if err == nil {
				break
			}
			var pg *pgconn.PgError
			if !errors.Is(err, errMigrationBusy) && !(errors.As(err, &pg) && pg.Code == "55P03") {
				return fmt.Errorf("control migration %s: %w", migration.Version, err)
			}
			// The failed transaction (including every acquired table lock) has
			// rolled back before sleeping. Old DML can finish in either order.
			delay := time.Duration(100+attempt%5*50) * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("control migration %s waiting for writers: %w", migration.Version, ctx.Err())
			case <-timer.C:
			}
		}
	}
	return nil
}

func controlMigrationApplied(ctx context.Context, db *sql.DB, m controlMigration) (bool, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('console_schema_migrations') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return false, err
	}
	return readControlMigration(ctx, db, m)
}

type migrationReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readControlMigration(ctx context.Context, db migrationReader, m controlMigration) (bool, error) {
	var checksum string
	err := db.QueryRowContext(ctx, `SELECT checksum FROM console_schema_migrations WHERE version=$1`, m.Version).Scan(&checksum)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if checksum != m.checksum() {
		return false, fmt.Errorf("control migration %s checksum changed; add a new version", m.Version)
	}
	return true, nil
}

func applyControlMigration(ctx context.Context, db *sql.DB, m controlMigration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var acquired bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, controlMigrationLock).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return errMigrationBusy
	}
	// A bound also covers catalog/sequence locks not represented in Tables.
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='250ms'`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS console_schema_migrations (
 version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	if applied, err := readControlMigration(ctx, tx, m); err != nil || applied {
		return err
	}
	// Advisory locks only serialize new migrators, not older application DML.
	// Obtain the complete relation lock set WITHOUT WAITING before any schema
	// or backfill work. If even one relation is busy, rollback all locks. This
	// prevents the accounts -> usage / usage -> accounts deadlock on adoption.
	tables := append([]string(nil), m.Tables...)
	sort.Strings(tables)
	for _, table := range tables {
		var exists bool
		identifier := pgx.Identifier{table}.Sanitize()
		if err = tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, identifier).Scan(&exists); err != nil {
			return err
		}
		if exists {
			if _, err = tx.ExecContext(ctx, "LOCK TABLE "+identifier+" IN ACCESS EXCLUSIVE MODE NOWAIT"); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO console_schema_migrations(version,checksum) VALUES($1,$2)`, m.Version, m.checksum()); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		log.Printf("event=control_schema_migrated version=%s checksum=%s", m.Version, m.checksum())
	}
	return err
}

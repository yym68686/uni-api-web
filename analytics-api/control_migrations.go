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

//go:embed migrations/0009_management_revisions.sql
var managementRevisionsSQL string

//go:embed migrations/0010_management_view_epoch.sql
var managementViewEpochSQL string

//go:embed migrations/0010_management_view_epoch.tables
var managementViewEpochTables string

//go:embed migrations/0011_management_account_headers.sql
var managementAccountHeadersSQL string

//go:embed migrations/0012_fingerprint_samples.sql
var fingerprintSamplesSQL string

//go:embed migrations/0013_site_rate_coefficient.sql
var siteRateCoefficientSQL string

//go:embed migrations/0014_channel_detection.sql
var channelDetectionSQL string

//go:embed migrations/0015_site_recharge.sql
var siteRechargeSQL string

//go:embed migrations/0016_fingerprint_catalog.sql
var fingerprintCatalogSQL string

//go:embed migrations/0017_managed_configuration.sql
var managedConfigurationSQL string

//go:embed migrations/0019_fingerprint_preferences.sql
var fingerprintPreferencesSQL string

//go:embed migrations/0020_management_display_groups.sql
var managementDisplayGroupsSQL string

//go:embed migrations/0021_management_display_global_proof.sql
var managementDisplayGlobalProofSQL string

//go:embed migrations/0021_management_display_global_proof.tables
var managementDisplayGlobalProofTables string

//go:embed migrations/0022_site_detection_runs.sql
var siteDetectionRunsSQL string

//go:embed migrations/0023_site_detection_targets.sql
var siteDetectionTargetsSQL string

//go:embed migrations/0024_site_detection_models.sql
var siteDetectionModelsSQL string

//go:embed migrations/0025_site_detection_handoff.sql
var siteDetectionHandoffSQL string

//go:embed migrations/0026_management_overview_models.sql
var managementOverviewModelsSQL string

//go:embed migrations/0027_management_overview_signals.sql
var managementOverviewSignalsSQL string

//go:embed migrations/0028_management_fingerprint_heartbeat.sql
var managementFingerprintHeartbeatSQL string

//go:embed migrations/0029_management_overview_targets.sql
var managementOverviewTargetsSQL string

//go:embed migrations/0030_management_overview_queue.sql
var managementOverviewQueueSQL string

//go:embed migrations/0031_site_key_create_backoff.sql
var siteKeyCreateBackoffSQL string

//go:embed migrations/0032_site_key_pause_budget.sql
var siteKeyPauseBudgetSQL string

//go:embed migrations/0033_management_overview_outcome.sql
var managementOverviewOutcomeSQL string

//go:embed migrations/0034_fingerprint_previous_result.sql
var fingerprintPreviousResultSQL string

var managementRevisionTables = []string{"console_sub_models", "console_management_model_signal", "console_sub_usage", "console_management_receipt_signal", "console_management_revision_state"}

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
		{"0009_management_revisions", managementRevisionsSQL, managementRevisionTables},
		{"0010_management_view_epoch", managementViewEpochSQL, strings.Fields(managementViewEpochTables)},
		{"0011_management_account_headers", managementAccountHeadersSQL, []string{"console_management_view_epoch", "console_sub_accounts"}},
		{"0012_fingerprint_samples", fingerprintSamplesSQL, []string{"console_model_fingerprints"}},
		{"0013_site_rate_coefficient", siteRateCoefficientSQL, []string{"console_sub_accounts", "console_management_view_epoch"}},
		{"0014_channel_detection", channelDetectionSQL, nil},
		{"0015_site_recharge", siteRechargeSQL, []string{"console_sub_accounts", "console_recharge_jobs"}},
		{"0016_fingerprint_catalog", fingerprintCatalogSQL, []string{"console_fingerprint_catalog"}},
		{"0017_managed_configuration", managedConfigurationSQL, []string{"console_sources", "console_managed_config_versions", "console_managed_config_operations", "console_managed_config_state"}},
		{"0019_fingerprint_preferences", fingerprintPreferencesSQL, []string{"console_configured_checks", "console_fingerprint_preferences", "console_fingerprint_upstream_catalog"}},
		{"0020_management_display_groups", managementDisplayGroupsSQL, []string{"console_management_display_epochs", "console_sub_models", "console_sub_model_summaries", "console_management_model_signal", "console_sub_usage", "console_management_receipt_signal"}},
		{"0021_management_display_global_proof", managementDisplayGlobalProofSQL, strings.Fields(managementDisplayGlobalProofTables)},
		// Independent additive changes avoid requiring an idle moment across
		// all three busy source tables during a rolling upgrade.
		{"0022_site_detection_runs", siteDetectionRunsSQL, []string{"console_sub_accounts", "console_site_detection_limits", "console_site_detection_slots"}},
		{"0023_site_detection_targets", siteDetectionTargetsSQL, []string{"console_sub_targets"}},
		{"0024_site_detection_models", siteDetectionModelsSQL, []string{"console_sub_models"}},
		{"0025_site_detection_handoff", siteDetectionHandoffSQL, []string{"console_sub_accounts"}},
		{"0026_management_overview_models", managementOverviewModelsSQL, []string{"console_management_overview_dirty", "console_management_overview_reset", "console_sub_models"}},
		{"0027_management_overview_signals", managementOverviewSignalsSQL, []string{"console_management_model_signal"}},
		{"0028_management_fingerprint_heartbeat", managementFingerprintHeartbeatSQL, []string{"console_model_fingerprints", "console_management_view_epoch"}},
		{"0029_management_overview_targets", managementOverviewTargetsSQL, []string{"console_sub_targets"}},
		{"0030_management_overview_queue", managementOverviewQueueSQL, []string{"console_sub_check_queue"}},
		{"0031_site_key_create_backoff", siteKeyCreateBackoffSQL, []string{"console_site_detection_limits"}},
		{"0032_site_key_pause_budget", siteKeyPauseBudgetSQL, []string{"console_sub_accounts"}},
		{"0033_management_overview_outcome", managementOverviewOutcomeSQL, []string{"console_sub_models", "console_management_overview_reset"}},
		{"0034_fingerprint_previous_result", fingerprintPreviousResultSQL, []string{"console_model_fingerprints"}},
	}
}

func (m controlMigration) checksum() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(m.SQL+"\n"+strings.Join(m.Tables, "\n"))))
}

func (m controlMigration) lockMode(table string) string {
	if strings.Contains(m.Version, "_management_overview_") {
		return "SHARE ROW EXCLUSIVE"
	}
	if m.Version == "0028_management_fingerprint_heartbeat" && table == "console_management_view_epoch" {
		return "ROW EXCLUSIVE"
	}
	if m.Version == "0021_management_display_global_proof" {
		// Installing the compatibility trigger needs SHARE ROW EXCLUSIVE on
		// the disposable cache. The reset row needs only the normal INSERT lock;
		// replacing an existing trigger function does not alter source relations.
		// Keep the original inventory/checksum while allowing live reads and
		// source writes to proceed during this rollout.
		if table == "console_management_display_groups" {
			return "SHARE ROW EXCLUSIVE"
		}
		return "ROW EXCLUSIVE"
	}
	return "ACCESS EXCLUSIVE"
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
			if _, err = tx.ExecContext(ctx, "LOCK TABLE "+identifier+" IN "+m.lockMode(table)+" MODE NOWAIT"); err != nil {
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

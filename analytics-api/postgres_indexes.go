package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const usageIndexDefinition = "(source_id,input_tokens,at_ms) WHERE kind NOT IN ('billing','trace') AND input_tokens IS NOT NULL"
const receiptIndexDefinition = "USING gin(billing_request_ids) WHERE kind='billing'"
const billingProviderIndexDefinition = "(source_id,key_id,model,provider,at_ms)"

var errBillingProviderIndexHeadroom = errors.New("billing provider index deferred for storage headroom; wait for bounded FIFO maintenance")

func usageIndexChecksum() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(usageIndexDefinition)))
}

func (e *Engine) readIndexesReady(ctx context.Context) error {
	var ready bool
	err := e.DB.QueryRowContext(ctx, `SELECT coalesce((SELECT indisvalid FROM pg_index WHERE indexrelid=to_regclass(?)),false) AND EXISTS(SELECT 1 FROM schema_versions WHERE version='0002' AND checksum=?)`, e.historyPath+".facts_long_usage", usageIndexChecksum()).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("analytics read index requires explicit preparation")
	}
	return nil
}

// Empty databases can initialize without a history scan. Existing databases
// prepare indexes explicitly, outside application startup, before activation.
func (e *Engine) initializeEmptyReadIndexes(ctx context.Context) error {
	if e.readIndexesReady(ctx) == nil {
		return nil
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395018)"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(8395015)"); err != nil {
		return err
	}
	var populated bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM facts)").Scan(&populated); err != nil {
		return err
	}
	if populated {
		return nil
	}
	if _, err = tx.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS facts_long_usage ON facts "+usageIndexDefinition); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS facts_receipt_ids ON facts "+receiptIndexDefinition); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_versions(version,checksum) VALUES('0002',?) ON CONFLICT DO NOTHING", usageIndexChecksum()); err != nil {
		return err
	}
	return tx.Commit()
}

// PostgreSQL cannot build a partitioned parent index CONCURRENTLY. Build each
// leaf concurrently, then attach it using short metadata operations. This never
// rewrites facts or changes account/configuration tables.
func (e *Engine) prepareReadIndexes(ctx context.Context) error {
	if err := e.prepareFactIndex(ctx, "facts_long_usage", "long_usage", usageIndexDefinition); err != nil {
		return err
	}
	_, err := e.DB.ExecContext(ctx, "INSERT INTO schema_versions(version,checksum) VALUES('0002',?) ON CONFLICT DO NOTHING", usageIndexChecksum())
	if err != nil {
		return err
	}
	return e.readIndexesReady(ctx)
}

func (e *Engine) prepareFactIndex(ctx context.Context, parent, suffix, definition string) error {
	return e.preparePartitionedFactIndex(ctx, parent, suffix, definition, "facts")
}

// Billing facts are written transactionally with request facts. This additive
// index is prepared after projection activation, so a provider-scoped billing
// read can seek the selected channel instead of scanning every model row.
func (e *Engine) prepareBillingProviderIndex(ctx context.Context) error {
	return e.preparePartitionedFactIndex(ctx, "billing_facts_provider_scope", "provider_scope", billingProviderIndexDefinition, "billing_facts")
}

func (e *Engine) preparePartitionedFactIndex(ctx context.Context, parent, suffix, definition, table string) error {
	conn, err := e.DB.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked bool
	if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(8395018)").Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return errors.New("analytics index preparation already running")
	}
	defer func() {
		call, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(call, "SELECT pg_advisory_unlock(8395018)")
	}()
	if table == "billing_facts" {
		var projectionReady bool
		if err = conn.QueryRowContext(ctx, "SELECT COALESCE((SELECT value='true' FROM meta WHERE name='billing_projection_complete'),false)").Scan(&projectionReady); err != nil {
			return err
		}
		if !projectionReady {
			return errors.New("billing provider index requires the completed billing projection")
		}
		var ready bool
		if err = conn.QueryRowContext(ctx, `SELECT COALESCE((SELECT indisvalid FROM pg_index WHERE indexrelid=to_regclass($1)),false)`, parent).Scan(&ready); err != nil {
			return err
		}
		if ready {
			return nil
		}
		// The production database is deliberately budgeted below the physical
		// volume limit. Estimate this additive B-tree conservatively from the
		// projected heap before starting any DDL and leave room for live writes.
		// If ordinary FIFO has not reclaimed enough space, keep serving reads and
		// ingestion unchanged and defer explicit preparation.
		var databaseBytes, heapBytes, ingestLimit int64
		if err = conn.QueryRowContext(ctx, `SELECT pg_database_size(current_database()),
 COALESCE((SELECT sum(pg_relation_size(h.inhrelid)) FROM pg_inherits h WHERE h.inhparent='billing_facts'::regclass),0),
 ingest_limit_bytes FROM storage_state WHERE singleton`).Scan(&databaseBytes, &heapBytes, &ingestLimit); err != nil {
			return err
		}
		const reserve = int64(64 << 20)
		if databaseBytes+heapBytes+reserve >= ingestLimit {
			return errBillingProviderIndexHeadroom
		}
	}
	if _, err = conn.ExecContext(ctx, "SET statement_timeout='60s'; SET lock_timeout='1s'; SET maintenance_work_mem='32MB'"); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS "+parent+" ON ONLY "+table+" "+definition); err != nil {
		return err
	}
	partitionQuery := "SELECT start_ms,name FROM fact_partitions ORDER BY start_ms"
	if table == "billing_facts" {
		partitionQuery = "SELECT 0::BIGINT,c.relname FROM pg_inherits h JOIN pg_class c ON c.oid=h.inhrelid WHERE h.inhparent='billing_facts'::regclass ORDER BY c.relname"
	}
	rows, err := conn.QueryContext(ctx, partitionQuery)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var at int64
		var name string
		if err = rows.Scan(&at, &name); err != nil {
			rows.Close()
			return err
		}
		valid := name == fmt.Sprintf("facts_%d", at)
		if table == "billing_facts" {
			partitionAt, parseErr := strconv.ParseInt(strings.TrimPrefix(name, "billing_"), 10, 64)
			valid = parseErr == nil && name == fmt.Sprintf("billing_%d", partitionAt)
		}
		if !valid {
			rows.Close()
			return errors.New("invalid fact partition")
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		var attached bool
		if err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_inherits h JOIN pg_index i ON i.indexrelid=h.inhrelid WHERE h.inhparent=$1::regclass AND i.indrelid=$2::regclass AND i.indisvalid)`, parent, name).Scan(&attached); err != nil {
			return err
		}
		if attached {
			continue
		}
		index := name + "_" + suffix
		// Concurrent builds may wait for old snapshots or autovacuum without
		// blocking normal reads/writes. Keep the short limit for metadata swaps,
		// but allow this nonblocking wait to finish within the maintenance budget.
		if _, err = conn.ExecContext(ctx, "SET lock_timeout='60s'"); err != nil {
			return err
		}
		var invalid bool
		if err = conn.QueryRowContext(ctx, "SELECT coalesce((SELECT NOT indisvalid FROM pg_index WHERE indexrelid=to_regclass($1)),false)", index).Scan(&invalid); err != nil {
			return err
		}
		if invalid {
			if _, err = conn.ExecContext(ctx, "DROP INDEX CONCURRENTLY "+index); err != nil {
				return err
			}
		}
		if _, err = conn.ExecContext(ctx, "CREATE INDEX CONCURRENTLY IF NOT EXISTS "+index+" ON "+name+" "+definition); err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, "SET lock_timeout='1s'"); err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, "ALTER INDEX "+parent+" ATTACH PARTITION "+index); err != nil {
			return err
		}
		fmt.Printf("{\"phase\":\"fact_index_partition_ready\",\"index\":%q,\"partition\":%q}\n", parent, name)
	}
	var valid bool
	if err = conn.QueryRowContext(ctx, "SELECT indisvalid FROM pg_index WHERE indexrelid=$1::regclass", parent).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errors.New("fact index is not valid")
	}
	return nil
}

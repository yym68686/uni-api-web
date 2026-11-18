package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"time"
)

type storedManagementOverview struct {
	Counts managementOverview `json:"counts"`
	Legacy bool               `json:"legacy"`
	Busy   []subModelResult   `json:"busy"`
}

func managementOverviewCatalog(builtins []string) string {
	return tokenHash("management-group-overview-v1\x00" + mustJSON(builtins))
}

// Cache admission and exact fallback run in the same repeatable-read
// transaction as page hydration. A dirty row and its source changes become
// visible together. Queue membership is deliberately read live.
func readStoredManagementOverviews(ctx context.Context, tx *sql.Tx, owner string, wanted []subBoundKey, catalog string) (map[string]managementPageGroupOverview, error) {
	trace := newManagementRowsTraceDerivedDecode(ctx, "page_overview_stored")
	defer trace.finish()
	accounts, groups := managementDisplayIDs(wanted)
	rows, err := tx.QueryContext(ctx, `SELECT o.account_id,o.group_id,o.body
 FROM unnest($2::text[],$3::bigint[]) w(account_id,group_id)
 JOIN console_management_group_overview o USING(account_id,group_id)
 JOIN console_sub_accounts a ON a.id=w.account_id
 WHERE a.owner=$1 AND o.catalog=$4 AND o.body IS NOT NULL
 AND o.generation=(SELECT generation FROM console_management_overview_reset WHERE singleton)
 AND (SELECT complete FROM console_management_projection_state WHERE singleton)
 AND NOT EXISTS (SELECT 1 FROM console_management_overview_dirty d WHERE d.account_id=w.account_id AND d.group_id=w.group_id)
 AND NOT EXISTS (SELECT 1 FROM console_sub_check_queue q WHERE q.account_id=w.account_id AND q.group_id=w.group_id)`, owner, accounts, groups, catalog)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	trace.queryReady()
	out := make(map[string]managementPageGroupOverview, len(wanted))
	for trace.next(rows) {
		var key subBoundKey
		var raw []byte
		if err := rows.Scan(&key.AccountID, &key.GroupID, &raw); err != nil {
			return nil, err
		}
		trace.counted(len(raw))
		var body storedManagementOverview
		if err := json.Unmarshal(raw, &body); err != nil || body.Busy == nil {
			continue
		}
		out[qualityGroupID(key.AccountID, key.GroupID)] = managementPageGroupOverview{body.Counts, body.Legacy, body.Busy}
	}
	return out, rows.Err()
}

const managementOverviewWarmBatch = 32

// Only one replica warms at a time. Building and clearing dirty markers must
// use the same RR snapshot: a model update which commits during the build
// writes a different xid marker that this DELETE cannot see or remove.
func warmManagementOverviewBatch(ctx context.Context, db *sql.DB, afterOwner string) (string, int, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	var locked, complete bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(8395026),complete FROM console_management_projection_state WHERE singleton`).Scan(&locked, &complete); err != nil || !locked || !complete {
		return "", 0, err
	}
	var owner string
	if err = tx.QueryRowContext(ctx, `SELECT owner FROM (SELECT DISTINCT owner FROM console_sub_accounts) owners
 ORDER BY (owner <= $1),owner LIMIT 1`, afterOwner).Scan(&owner); err != nil {
		if err == sql.ErrNoRows {
			return "", 0, nil
		}
		return "", 0, err
	}
	catalog := managementOverviewCatalog(managementUnique(subModels))
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT generation FROM console_management_overview_reset WHERE singleton`).Scan(&generation); err != nil {
		return owner, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT t.account_id,t.group_id FROM console_sub_targets t
 JOIN console_sub_accounts a ON a.id=t.account_id
 LEFT JOIN console_management_group_overview o USING(account_id,group_id)
 WHERE a.owner=$1 AND (o.account_id IS NULL OR o.generation<>$2 OR o.catalog<>$3
 OR EXISTS (SELECT 1 FROM console_management_overview_dirty d WHERE d.account_id=t.account_id AND d.group_id=t.group_id))
 ORDER BY t.account_id,t.group_id LIMIT $4`, owner, generation, catalog, managementOverviewWarmBatch)
	if err != nil {
		return owner, 0, err
	}
	wanted := make([]subBoundKey, 0, managementOverviewWarmBatch)
	for rows.Next() {
		var key subBoundKey
		if err = rows.Scan(&key.AccountID, &key.GroupID); err != nil {
			break
		}
		wanted = append(wanted, key)
	}
	rows.Close()
	if err != nil {
		return owner, 0, err
	}
	if err = rows.Err(); err != nil {
		return owner, 0, err
	}
	if len(wanted) == 0 {
		return owner, 0, nil
	}
	overviews, err := computeManagementPageOverview(ctx, tx, owner, wanted, true)
	if err != nil {
		return owner, 0, err
	}
	type storedRow struct {
		subBoundKey
		Body *storedManagementOverview `json:"body"`
	}
	batch := make([]storedRow, len(wanted))
	for i, key := range wanted {
		batch[i].subBoundKey = key
		if v, ok := overviews[qualityGroupID(key.AccountID, key.GroupID)]; ok {
			batch[i].Body = &storedManagementOverview{v.counts, v.legacy, v.busy}
		}
	}
	raw, err := json.Marshal(batch)
	if err != nil {
		return owner, 0, err
	}
	// A NULL body marks an unsupported group, not a usable empty summary.
	if _, err = tx.ExecContext(ctx, `INSERT INTO console_management_group_overview(account_id,group_id,generation,catalog,body)
 SELECT account_id,group_id,$2,$3,body FROM jsonb_to_recordset($1::jsonb) AS x(account_id text,group_id bigint,body jsonb)
 ON CONFLICT(account_id,group_id) DO UPDATE SET generation=excluded.generation,catalog=excluded.catalog,body=excluded.body`, string(raw), generation, catalog); err != nil {
		return owner, 0, err
	}
	accounts, groups := managementDisplayIDs(wanted)
	if _, err = tx.ExecContext(ctx, `DELETE FROM console_management_overview_dirty d USING unnest($1::text[],$2::bigint[]) w(account_id,group_id)
 WHERE d.account_id=w.account_id AND d.group_id=w.group_id`, accounts, groups); err != nil {
		return owner, 0, err
	}
	return owner, len(wanted), tx.Commit()
}

func (s *Service) managementOverviewLoop(ctx context.Context) {
	owner := ""
	pruneAt := time.Now().Add(time.Minute)
	for ctx.Err() == nil {
		if s.managementReaders.Load() != 0 {
			if !waitStartup(ctx, time.Second) {
				return
			}
			continue
		}
		stats := s.control.db.Stats()
		if stats.MaxOpenConnections > 0 && stats.InUse > stats.MaxOpenConnections/2 {
			if !waitStartup(ctx, time.Second) {
				return
			}
			continue
		}
		step, stop := s.interruptibleManagementWarm(ctx, 3*time.Second)
		nextOwner, n, err := warmManagementOverviewBatch(step, s.control.db, owner)
		if nextOwner != "" {
			owner = nextOwner
		}
		if err == nil && !time.Now().Before(pruneAt) {
			pruneAt = time.Now().Add(time.Minute)
			err = pruneManagementOverviews(step, s.control.db)
		}
		stop()
		if err != nil || n > 0 {
			log.Printf("management_overview_warm groups=%d class=%s", n, databaseErrorClass(err))
		}
		delay := 2 * time.Second
		if n == managementOverviewWarmBatch && err == nil {
			delay = time.Second
		}
		if !waitStartup(ctx, delay) {
			return
		}
	}
}

// Keep deleted accounts/groups from retaining markers or disposable summaries.
// Limit the maintenance write size independently of how many groups vanished.
func pruneManagementOverviews(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `WITH gate AS MATERIALIZED (SELECT pg_try_advisory_xact_lock(8395026) AS locked), dead AS (
 SELECT d.ctid FROM console_management_overview_dirty d
 WHERE (SELECT locked FROM gate) AND NOT EXISTS (SELECT 1 FROM console_sub_targets t WHERE t.account_id=d.account_id AND t.group_id=d.group_id)
 LIMIT 512
 ), removed AS (DELETE FROM console_management_overview_dirty d USING dead WHERE d.ctid=dead.ctid),
 stale AS (
 SELECT o.ctid FROM console_management_group_overview o
 WHERE (SELECT locked FROM gate) AND NOT EXISTS (SELECT 1 FROM console_sub_targets t WHERE t.account_id=o.account_id AND t.group_id=o.group_id)
 LIMIT 128
 )
 DELETE FROM console_management_group_overview o USING stale WHERE o.ctid=stale.ctid`)
	return err
}

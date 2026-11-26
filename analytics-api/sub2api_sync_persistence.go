package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"
)

const subSyncPlanWriteLock = 8395043
const subSyncPlanWriteAttempts = 5

// Only one local waiter uses a database connection for the cross-replica lock.
// Discovery, preparing the upstream keys and probing models do not take it.
type subSyncPlanWriteGate struct {
	once sync.Once
	slot chan struct{}
}

func (g *subSyncPlanWriteGate) acquire(ctx context.Context) (func(), error) {
	g.once.Do(func() { g.slot = make(chan struct{}, 1) })
	select {
	case g.slot <- struct{}{}:
		return func() { <-g.slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type subSyncGroupWrite struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Platform string  `json:"platform"`
	Channel  string  `json:"channel"`
	Rate     float64 `json:"rate"`
	ProbeRPM *int    `json:"probe_rpm"`
}

func (s *Service) subSaveDiscoveredSync(ctx context.Context, id, job string, groups []subRemoteGroup, labels map[int64]string, models map[int64][]string, newAPI bool, limits any) error {
	rows := make([]subSyncGroupWrite, 0, len(groups))
	groupIDs := make([]int64, 0, len(groups))
	seen := make(map[int64]bool, len(groups))
	for _, g := range groups {
		if g.ID <= 0 || len(g.Name) > 512 || seen[g.ID] {
			return errors.New("站点返回了无效分组")
		}
		seen[g.ID] = true
		groupIDs = append(groupIDs, g.ID)
		rows = append(rows, subSyncGroupWrite{g.ID, g.Name, g.Platform, labels[g.ID], g.Rate, positiveProbeLimit(g.RPM)})
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		return errors.New("站点返回了无效分组")
	}
	release, err := s.control.syncPlanWrites.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	// Retry the entire local transaction only when PostgreSQL confirms it
	// aborted. A lost connection at COMMIT is ambiguous and is not replayed.
	for attempt := 1; ; attempt++ {
		stage, err := s.subWriteSyncPlan(ctx, id, job, string(payload), groupIDs, groups, models, newAPI, limits)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		class := databaseErrorClass(err)
		if class != "database_transaction" || attempt >= subSyncPlanWriteAttempts {
			log.Printf("sub_sync_write_failed stage=%s class=%s attempt=%d", stage, class, attempt)
			switch stage {
			case "queue_models":
				return errors.New("模型保存失败")
			case "record_observations":
				return errors.New("分组观察记录保存失败")
			case "on_demand_plan":
				return errors.New("保存按需检测计划失败")
			default:
				return errors.New("分组保存失败")
			}
		}
		log.Printf("sub_sync_write_retry stage=%s class=%s attempt=%d", stage, class, attempt)
		delay := (25 * time.Millisecond) << (attempt - 1)
		if !waitStartup(ctx, delay+rand.N(delay)) {
			return ctx.Err()
		}
	}
}

func (s *Service) subWriteSyncPlan(ctx context.Context, id, job, groupRows string, groupIDs []int64, groups []subRemoteGroup, models map[int64][]string, newAPI bool, limits any) (string, error) {
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return "begin_plan", err
	}
	defer tx.Rollback()
	// Take the same lock before touching any rows on every replica. Group
	// platform changes and model enrollment can both update the shared
	// management revisions through triggers, in different orders.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, subSyncPlanWriteLock); err != nil {
		return "lock_plan", err
	}
	var activeID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM console_sub_accounts WHERE id=$1 AND job_id=$2
 AND (COALESCE(sync_lease_until,lease_until) IS NULL OR COALESCE(sync_lease_until,lease_until)>clock_timestamp()) FOR UPDATE`, id, job).Scan(&activeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "lock_account", context.Canceled
	}
	if err != nil {
		return "lock_account", err
	}
	run := ""
	if plan := subPlan(ctx); plan != nil {
		run = plan.run
	}
	// Keep present groups active and batch the writes. Do not advance the
	// shared target revision once just to undo those updates immediately.
	if _, err = tx.ExecContext(ctx, `INSERT INTO console_sub_targets(account_id,group_id,name,platform,channel,rate,state,sync_run_id,sync_key_state,probe_rpm)
 SELECT $1,g.id,g.name,g.platform,g.channel,g.rate,'queued',$2,'preparing',g.probe_rpm
 FROM jsonb_to_recordset($3::jsonb) AS g(id bigint,name text,platform text,channel text,rate double precision,probe_rpm int)
 ORDER BY g.id
 ON CONFLICT(account_id,group_id) DO UPDATE SET name=excluded.name,platform=excluded.platform,channel=excluded.channel,
 rate=excluded.rate,active=true,state='queued',message='',sync_run_id=excluded.sync_run_id,sync_key_state=excluded.sync_key_state,probe_rpm=excluded.probe_rpm`, id, run, groupRows); err != nil {
		return "upsert_groups", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE console_sub_targets SET active=false WHERE account_id=$1 AND active AND NOT (group_id=ANY($2::bigint[]))`, id, groupIDs); err != nil {
		return "deactivate_missing_groups", err
	}
	if err = queueSubSyncModels(ctx, tx, id, groups, models); err != nil {
		return "queue_models", err
	}
	if newAPI && len(groups) > 0 {
		if err = recordNewAPIGroupObservations(ctx, tx, id); err != nil {
			return "record_observations", err
		}
		billing := make(map[string]subBilling, len(groups))
		for _, g := range groups {
			billing[strconv.FormatInt(g.ID, 10)] = syncGroupBilling(g, nil, true)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE console_sub_targets t
 SET state='done',sync_key_state='not_needed',billing=b.value
 FROM jsonb_each($3::jsonb) b WHERE t.account_id=$1 AND t.active AND t.sync_run_id=$2 AND t.group_id::text=b.key
 AND NOT EXISTS(SELECT 1 FROM console_sub_models m WHERE m.account_id=t.account_id AND m.group_id=t.group_id AND m.sync_run_id=$2 AND m.state='queued')`,
			id, run, mustJSON(billing)); err != nil {
			return "on_demand_plan", err
		}
	}
	if subPlan(ctx) != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE console_sub_accounts SET sync_plan_ready=true,sync_phase='preparing',
 probe_limits=CASE WHEN $3::jsonb IS NULL THEN probe_limits ELSE probe_limits||$3::jsonb END
 WHERE id=$1 AND job_id=$2`, id, job, limits); err != nil {
			return "mark_plan_ready", err
		}
	}
	return "commit_plan", tx.Commit()
}

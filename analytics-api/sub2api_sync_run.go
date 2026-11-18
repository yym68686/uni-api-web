package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
)

type subSyncPlanKey struct{}

type subSyncPlan struct {
	run         string
	initialized bool
	wake        chan struct{}
}

func subPlan(ctx context.Context) *subSyncPlan {
	plan, _ := ctx.Value(subSyncPlanKey{}).(*subSyncPlan)
	return plan
}

type subSyncPause struct {
	phase string
	until time.Time
}

func (p *subSyncPause) Error() string { return "等待上游限流冷却" }

func syncRatePause(err error, phase string) *subSyncPause {
	var pause *subSyncPause
	if errors.As(err, &pause) {
		return pause
	}
	var remote *subRemoteError
	if !errors.As(err, &remote) || remote.Status != http.StatusTooManyRequests {
		return nil
	}
	delay := remote.RetryAfter
	if delay <= 0 {
		delay = 30 * time.Second
	}
	return &subSyncPause{phase, time.Now().Add(delay)}
}

func (p *subSyncPlan) notify() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// A batch holds one account lease. Key preparation and one draining model
// dispatcher run independently, with one current-job fence for both writers.
func (s *Service) subRunSync(ctx context.Context, id, base, job, run, encrypted string, syncOnly, resumed bool) error {
	plan := &subSyncPlan{run: run, wake: make(chan struct{}, 1)}
	if err := s.control.db.QueryRowContext(ctx, `UPDATE console_sub_accounts SET sync_resume_at=NULL,
 sync_phase=CASE WHEN NOT sync_plan_ready THEN 'discovering' WHEN EXISTS(SELECT 1 FROM console_sub_targets
 WHERE account_id=$1 AND active AND sync_run_id=$3 AND sync_key_state='preparing') THEN 'preparing' ELSE 'probing' END
 WHERE id=$1 AND job_id=$2 AND state='running' AND sync_lease_until>clock_timestamp() RETURNING sync_plan_ready`, id, job, run).Scan(&plan.initialized); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, subSyncPlanKey{}, plan)
	modelDone := make(chan error, 1)
	if !syncOnly {
		go func() {
			for range plan.wake {
				if err := s.subTestTargets(ctx, id, base, job, false); err != nil {
					s.subPublishSyncPause(ctx, id, job, syncRatePause(err, "waiting_models"))
					modelDone <- err
					return
				}
			}
			// A buffered wake may have coalesced several ready groups; draining
			// once after the producer closes also covers the last commit.
			err := s.subTestTargets(ctx, id, base, job, false)
			s.subPublishSyncPause(ctx, id, job, syncRatePause(err, "waiting_models"))
			modelDone <- err
		}()
		if resumed && plan.initialized {
			plan.notify()
		}
	}
	keysErr := s.subSynchronize(ctx, id, base, job, encrypted)
	keyPause := syncRatePause(keysErr, "waiting_keys")
	s.subPublishSyncPause(ctx, id, job, keyPause)
	close(plan.wake)
	var modelErr error
	if !syncOnly {
		modelErr = <-modelDone
	}
	modelPause := syncRatePause(modelErr, "waiting_models")
	if keysErr != nil && keyPause == nil {
		return keysErr
	}
	if modelErr != nil && modelPause == nil {
		return modelErr
	}
	// If both branches are blocked, don't consume one retry for each
	// overlapping cooldown; wait until both can make progress.
	if keyPause != nil && modelPause != nil && keyPause.until.Before(modelPause.until) {
		return modelPause
	}
	if keyPause != nil {
		return keyPause
	}
	if modelPause != nil {
		return modelPause
	}
	return nil
}

func (s *Service) subPublishSyncPause(ctx context.Context, id, job string, pause *subSyncPause) {
	if pause == nil {
		return
	}
	// The phase is visible while the other branch is still running, but no
	// replica can resume the job until the current worker releases its lease.
	_, _ = s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET sync_phase=$3,sync_resume_at=$4
 WHERE id=$1 AND job_id=$2 AND state='running' AND (sync_resume_at IS NULL OR sync_resume_at<$4)`, id, job, pause.phase, pause.until)
}

func (s *Service) subPendingKeyGroups(ctx context.Context, id, run string) ([]subRemoteGroup, error) {
	rows, err := s.control.db.QueryContext(ctx, `SELECT group_id,name,platform,rate FROM console_sub_targets
 WHERE account_id=$1 AND active AND sync_run_id=$2 AND sync_key_state='preparing' ORDER BY group_id`, id, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []subRemoteGroup
	for rows.Next() {
		var g subRemoteGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Platform, &g.Rate); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func syncGroupBilling(g subRemoteGroup, rates map[string]float64, known bool) subBilling {
	if known && !g.Peak {
		rate := g.Rate
		if personal, ok := rates[strconv.FormatInt(g.ID, 10)]; ok {
			rate = personal
		}
		if rate >= 0 {
			return subBilling{Rate: &rate, Source: "panel", CheckedAt: time.Now().Unix()}
		}
	}
	return subBilling{Source: "unavailable"}
}

func (s *Service) subSaveSyncKey(ctx context.Context, id, job string, group int64, key subRemoteKey, billing subBilling) error {
	enc, err := s.control.encrypt(key.Key)
	if err != nil {
		return errors.New("测试 key 加密失败")
	}
	n, err := s.subProbeWrite(ctx, `UPDATE console_sub_targets t SET remote_key_id=$3,encrypted_key=$4,billing=$6,
 sync_key_state='ready',state=CASE WHEN $7 OR EXISTS(SELECT 1 FROM console_channel_detection_settings d JOIN console_sub_accounts a ON a.owner=d.owner WHERE a.id=t.account_id AND d.account_id=t.account_id AND d.group_id=t.group_id) THEN 'done' ELSE state END
 WHERE account_id=$1 AND group_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$5 AND state='running'
 AND (sync_lease_until IS NULL OR sync_lease_until>clock_timestamp()) FOR SHARE)`, id, group, key.ID, enc, job, mustJSON(billing), subSyncMetadataOnly(ctx))
	if err != nil {
		return errors.New("测试 key 保存失败")
	}
	if n != 1 {
		return context.Canceled
	}
	if p := subPlan(ctx); p != nil {
		p.notify()
	}
	return nil
}

func (s *Service) subSyncKeyFailed(ctx context.Context, id, job string, group int64, message string) error {
	_, err := s.subProbeWrite(ctx, `WITH failed AS (
 UPDATE console_sub_targets SET state='error',sync_key_state='error',message=$4
 WHERE account_id=$1 AND group_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$3
 AND (sync_lease_until IS NULL OR sync_lease_until>clock_timestamp()) FOR SHARE)
 RETURNING group_id,sync_run_id)
 UPDATE console_sub_models SET state='interrupted',message='测试密钥不可用，未发出检测'
 WHERE account_id=$1 AND (group_id,sync_run_id) IN (SELECT group_id,sync_run_id FROM failed) AND state='queued'`, id, group, job, message)
	return err
}

func (s *Service) subFinishSyncKeys(ctx context.Context, id, job string, failures map[string]int) error {
	message := subTestKeyFailureSummary(failures)
	_, err := s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET synced_at=$3,
 message=CASE WHEN $4<>'' THEN $4 WHEN EXISTS(SELECT 1 FROM console_sub_targets WHERE account_id=$1 AND sync_run_id=console_sub_accounts.sync_run_id AND sync_key_state='error') THEN '部分分组测试密钥不可用，详情见表格' ELSE '' END,
 sync_phase=CASE WHEN sync_run_id<>'' AND sync_resume_at IS NULL THEN 'probing' ELSE sync_phase END WHERE id=$1 AND job_id=$2`, id, job, time.Now().Unix(), message)
	return err
}

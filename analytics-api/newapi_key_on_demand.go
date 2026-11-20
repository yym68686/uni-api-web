package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Fingerprint jobs have their own worker lease. Take the account lease only
// for provisioning so an explicit fingerprint can use a metadata-only group
// without racing a sync, an import, or another worker creating the same Key.
func (s *Service) subPrepareNewAPIFingerprintKey(ctx context.Context, account, base string, groupID int64) error {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	job := randomID()
	var previous string
	err := s.control.db.QueryRowContext(ctx, `WITH claim AS (
 SELECT a.id,a.state FROM console_sub_accounts a JOIN console_sub_targets t ON t.account_id=a.id
 WHERE a.id=$1 AND a.base=$2 AND a.provider_kind='newapi' AND a.state NOT IN ('running','queued')
 AND t.group_id=$3 AND t.active AND t.encrypted_key='' FOR UPDATE OF a
 ) UPDATE console_sub_accounts a SET state='running',job_id=$4,job_kind='fingerprint_key',lease_until=now()+interval '60 seconds'
 FROM claim WHERE a.id=claim.id RETURNING claim.state`, account, base, groupID, job).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		// A concurrent job might have finished provisioning since the caller read.
		var ready bool
		if readErr := s.control.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id
 WHERE a.id=$1 AND a.base=$2 AND t.group_id=$3 AND t.active AND t.encrypted_key<>'')`, account, base, groupID).Scan(&ready); readErr == nil && ready {
			return nil
		}
		return errors.New("账号正在处理其他任务或分组已变化，请稍后重新提交指纹检测")
	}
	if err != nil {
		return errors.New("无法锁定账号以准备检测 Key")
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_, _ = s.control.db.ExecContext(cleanup, `UPDATE console_sub_accounts SET state=$3,job_id='',job_kind='',lease_until=NULL
 WHERE id=$1 AND job_id=$2`, account, job, previous)
	}()
	groups, err := s.newAPIGroups(ctx, account, base)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if group.ID == groupID {
			if err := s.subSyncNewAPIKeys(ctx, account, base, job, []subRemoteGroup{group}, nil, true, true); err != nil {
				return err
			}
			var ready bool
			var message string
			if err := s.control.db.QueryRowContext(ctx, `SELECT encrypted_key<>'',message FROM console_sub_targets
 WHERE account_id=$1 AND group_id=$2 AND active`, account, groupID).Scan(&ready, &message); err == nil && ready {
				return nil
			}
			if message != "" {
				return errors.New(message)
			}
			return errors.New("检测 Key 尚不可用")
		}
	}
	return errors.New("上游分组已不可用，未创建检测 Key")
}

// Metadata-only and disabled-group syncs no longer pre-create a token just
// in case. A later explicit probe uses the same bounded key producer while
// holding the ordinary account job lease.
func (s *Service) subPrepareNewAPIManualKeys(ctx context.Context, account, base, job, kind string) error {
	rows, err := s.control.db.QueryContext(ctx, `SELECT t.group_id
 FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id
 WHERE a.id=$1 AND a.job_id=$2 AND a.provider_kind='newapi' AND t.active AND t.encrypted_key=''
 AND CASE $3 WHEN 'compaction' THEN t.compaction_state='queued'
 WHEN 'tool_use' THEN t.tool_use_state='queued' ELSE t.state='queued' END
 ORDER BY t.group_id`, account, job, kind)
	if err != nil {
		return err
	}
	var pending []int64
	for rows.Next() {
		var group int64
		if err := rows.Scan(&group); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, group)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(pending) == 0 {
		return err
	}
	groups, err := s.newAPIGroups(ctx, account, base)
	if err != nil {
		return err
	}
	byID := make(map[int64]subRemoteGroup, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
	}
	needed := make([]subRemoteGroup, 0, len(pending))
	for _, id := range pending {
		if group, ok := byID[id]; ok {
			needed = append(needed, group)
		} else if err := s.subNewAPIManualKeyFailed(ctx, account, job, id, "上游分组已不可用，未创建检测 Key"); err != nil {
			return err
		}
	}
	err = s.subSyncNewAPIKeys(ctx, account, base, job, needed, nil, true, true)
	if syncRatePause(err, "waiting_keys") != nil {
		return errors.New("准备检测 Key 时站点正在限流，请在冷却后重新提交检测")
	}
	return err
}

func (s *Service) subNewAPIManualKeyFailed(ctx context.Context, account, job string, group int64, message string) error {
	_, err := s.subProbeWrite(ctx, `WITH failed AS (
 UPDATE console_sub_targets SET state='error',sync_key_state='error',message=$4,
 compaction_state=CASE WHEN compaction_state='queued' THEN 'interrupted' ELSE compaction_state END,
 tool_use_state=CASE WHEN tool_use_state='queued' THEN 'interrupted' ELSE tool_use_state END
 WHERE account_id=$1 AND group_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$3
 AND state='running' AND lease_until>clock_timestamp() FOR SHARE) RETURNING group_id
 ) UPDATE console_sub_models SET state='interrupted',message='检测 Key 不可用，未发出检测'
 WHERE account_id=$1 AND group_id IN (SELECT group_id FROM failed) AND state='queued'`, account, group, job, message)
	return err
}

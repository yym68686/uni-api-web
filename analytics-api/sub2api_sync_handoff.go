package main

import (
	"context"
	"errors"
	"log"
	"time"
)

const subSyncLeasePrefix = "site-sync:"

// A new worker gets a new lease token, but keeps the same model plan. Queued
// work is safe to take over. Running work is intentionally conservative: an
// upstream request may have been accepted even if there is no result yet.
// A legacy binary does not know how to resume these jobs (sync_retry_at is
// NULL), so it cannot consume a handoff using the wrong plan/lease semantics.
func (s *Service) subSuspendSync(ctx context.Context, id, token string) error {
	_, err := s.subProbeWrite(ctx, `WITH suspended AS (
 UPDATE console_sub_accounts a SET job_id='',sync_lease_until=NULL,
 sync_resume_at=greatest(clock_timestamp(),sync_resume_at),sync_retry_at=NULL,
 sync_phase=CASE WHEN sync_phase IN ('compaction','tool_use') OR sync_resume_at>clock_timestamp() THEN sync_phase ELSE 'handoff' END,
 sync_handoff_partial=sync_handoff_partial OR EXISTS(SELECT 1 FROM console_sub_models m
   WHERE m.account_id=a.id AND m.sync_run_id=a.sync_run_id AND m.state='running') OR EXISTS(
   SELECT 1 FROM console_sub_targets t WHERE t.account_id=a.id AND
   (a.sync_phase='compaction' AND t.compaction_state='running' OR a.sync_phase='tool_use' AND t.tool_use_state='running'))
 WHERE state='running' AND job_kind='sync' AND (
   $1<>'' AND id=$1 AND job_id=$2 OR $1='' AND starts_with(job_id,$3) AND sync_lease_until<=clock_timestamp())
 RETURNING id,sync_run_id,sync_phase
 ), capabilities AS (
 UPDATE console_sub_targets t SET
 compaction_state=CASE WHEN b.sync_phase='compaction' AND t.compaction_state='running' THEN 'interrupted' ELSE t.compaction_state END,
 tool_use_state=CASE WHEN b.sync_phase='tool_use' AND t.tool_use_state='running' THEN 'interrupted' ELSE t.tool_use_state END,
 tool_use=CASE WHEN b.sync_phase='tool_use' AND t.tool_use_state='running' AND jsonb_typeof(t.tool_use->'models')='array'
 THEN jsonb_set(t.tool_use,'{models}',COALESCE((SELECT jsonb_agg(CASE WHEN m.value->>'state' IN ('queued','running')
   THEN jsonb_set(m.value,'{state}','"interrupted"') ELSE m.value END ORDER BY m.n)
   FROM jsonb_array_elements(t.tool_use->'models') WITH ORDINALITY m(value,n)),'[]'::jsonb)) ELSE t.tool_use END
 FROM suspended b WHERE t.account_id=b.id AND
 (b.sync_phase='compaction' AND t.compaction_state='running' OR b.sync_phase='tool_use' AND t.tool_use_state='running')
 ) UPDATE console_sub_models m SET state='interrupted',message='服务切换时请求可能已发出，未自动重发'
 FROM suspended b WHERE m.account_id=b.id AND m.sync_run_id=b.sync_run_id AND m.state='running'`, id, token, subSyncLeasePrefix)
	return err
}

var errSubLeaseLost = errors.New("site detection lease lost")

const subJobLeaseDuration = 30 * time.Second

func (s *Service) subRenewJobLease(ctx context.Context, id, job string, cancel context.CancelCauseFunc) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastConfirmed := time.Now()
	failed := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			attemptedAt := time.Now()
			renew, done := context.WithDeadline(ctx, minTime(attemptedAt.Add(2*time.Second), lastConfirmed.Add(subJobLeaseDuration)))
			res, err := s.control.db.ExecContext(renew, `UPDATE console_sub_accounts SET
 lease_until=CASE WHEN sync_lease_until IS NULL THEN clock_timestamp()+$3*interval '1 second' END,
 sync_lease_until=CASE WHEN sync_lease_until IS NOT NULL THEN clock_timestamp()+$3*interval '1 second' END
 WHERE id=$1 AND job_id=$2 AND state='running' AND COALESCE(sync_lease_until,lease_until)>clock_timestamp()`, id, job, int(subJobLeaseDuration/time.Second))
			done()
			if err == nil {
				var n int64
				n, err = res.RowsAffected()
				if err == nil && n == 0 {
					cancel(errSubLeaseLost)
					return
				}
			}
			if err == nil {
				lastConfirmed, failed = attemptedAt, false
			} else if ctx.Err() == nil {
				if !failed {
					log.Printf("event=site_detection_lease_retry class=%s", databaseErrorClass(err))
					failed = true
				}
				if time.Since(lastConfirmed) >= subJobLeaseDuration {
					cancel(errSubLeaseLost)
					return
				}
			}
		}
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

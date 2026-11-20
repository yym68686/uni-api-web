package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

const waitingKeyCreate = "waiting_key_create"

var errNewAPIKeyCreatePending = errors.New("上次创建结果尚未确认，已重查；不会重复创建 key")
var errNewAPIKeyCreateUnconfirmed = errors.New("创建 key 的结果未确认，下次同步将先重查，不会重复创建")

func newAPIKeyCreateScope(base string) string { return "key_create:" + fingerprintSite(base) }

// Both test-key entry points share the host budget, even across accounts and
// replicas. Acquire it before inserting the intent: a local wait is not a write
// whose outcome could be uncertain.
func (s *Service) newAPICreateOwnedKey(ctx context.Context, account, base, job, name, group string) error {
	gap := 5 * time.Second
	if s.newAPIKeyCreateGap != nil {
		gap = *s.newAPIKeyCreateGap
	}
	scope := newAPIKeyCreateScope(base)
	release, err := s.acquireSiteDetection(ctx, siteModelBudget{scope: scope, minGap: gap, concurrency: 1}, waitingKeyCreate)
	if err != nil {
		var pause *subSyncPause
		if errors.As(err, &pause) {
			// Another request already established this cooldown, or the local
			// reservation isn't due. There was no new rejection for this run.
			return &subSyncPause{phase: pause.phase, until: pause.until, local: true}
		}
		return err
	}
	defer release()

	var intent time.Time
	err = s.control.db.QueryRowContext(ctx, `INSERT INTO console_site_key_creates(account_id,name)
 SELECT $1,$2 WHERE $3='' OR EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$3 AND state='running'
 AND (sync_lease_until IS NULL OR sync_lease_until>clock_timestamp()) FOR SHARE)
 ON CONFLICT DO NOTHING RETURNING created_at`, account, name, job).Scan(&intent)
	if errors.Is(err, sql.ErrNoRows) {
		return errNewAPIKeyCreatePending
	}
	if err != nil {
		return err
	}

	err = s.newAPICall(ctx, account, base, "POST", "/api/token/", map[string]any{
		"name": name, "group": group, "unlimited_quota": true, "expired_time": -1,
		"remain_quota": 0, "model_limits_enabled": false, "model_limits": "",
		"allow_ips": "", "auto_ban": false, "auto_groups": []string{},
	}, nil)
	var remote *subRemoteError
	// Save feedback even if the worker is already shutting down, and before
	// releasing the slot. Keep the intent on timeouts, unknown outcomes and 5xx.
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err == nil {
		if strings.HasPrefix(name, "uni-console-check-"+account+"-") {
			if _, saveErr := s.control.db.ExecContext(saveCtx, `UPDATE console_site_key_creates
 SET acknowledged_at=clock_timestamp(),acknowledged_base=$4 WHERE account_id=$1 AND name=$2 AND created_at=$3`,
				account, name, intent, base); saveErr != nil {
				// The key can still be used, but an unproven key is not eligible for automatic reclamation.
				log.Printf("event=site_test_key_ownership_ack_failed class=%s", databaseErrorClass(saveErr))
			}
		}
		if _, saveErr := s.control.db.ExecContext(saveCtx, `UPDATE console_site_detection_limits
 SET create_rate_failures=0 WHERE scope=$1 AND create_rate_failures<>0`, scope); saveErr != nil {
			log.Printf("event=site_key_create_backoff_reset_failed class=%s", databaseErrorClass(saveErr))
		}
		return nil
	}
	if !errors.As(err, &remote) || remote.Status < 400 || remote.Status >= 500 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errNewAPIKeyCreateUnconfirmed
	}
	var pause *subSyncPause
	if remote.Status == http.StatusTooManyRequests {
		var until time.Time
		hintMS := remote.RetryAfter.Milliseconds()
		if remote.RetryAfter%time.Millisecond > 0 {
			hintMS++
		}
		// No usable Retry-After: 30s, 1m, 2m, 4m, 8m, then 10m.
		// A positive server hint is never shortened. Bound stale feedback, not
		// a server-declared cooldown.
		if saveErr := s.control.db.QueryRowContext(saveCtx, `WITH prior AS MATERIALIZED (
 SELECT LEAST(6,CASE WHEN learned_at>clock_timestamp()-interval '24 hours' THEN create_rate_failures+1 ELSE 1 END) AS streak
 FROM console_site_detection_limits WHERE scope=$1 FOR UPDATE
 ) UPDATE console_site_detection_limits l SET create_rate_failures=prior.streak,learned_at=clock_timestamp(),
 retry_at=GREATEST(l.retry_at,clock_timestamp()+CASE WHEN $2::bigint>0 THEN $2*interval '1 millisecond'
 ELSE LEAST(600,30*(1<<(prior.streak-1)))*interval '1 second' END)
 FROM prior WHERE l.scope=$1 RETURNING l.retry_at`, scope, hintMS).Scan(&until); saveErr != nil {
			return saveErr
		}
		pause = &subSyncPause{phase: waitingKeyCreate, until: until}
	}
	if _, deleteErr := s.subProbeWrite(saveCtx, `DELETE FROM console_site_key_creates
 WHERE account_id=$1 AND name=$2 AND created_at=$3`, account, name, intent); deleteErr != nil {
		return deleteErr
	}
	if pause != nil {
		return pause
	}
	return err
}

package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func newAPIOperation(method, path string) string {
	u, _ := url.Parse(path)
	if u == nil {
		return "站点接口"
	}
	switch {
	case u.Path == "/api/log/self":
		return "账单读取"
	case u.Path == "/api/user/self/groups":
		return "分组读取"
	case u.Path == "/api/user/self":
		return "账号读取"
	case u.Path == "/api/token/" && method == "POST":
		return "密钥创建"
	case strings.HasPrefix(u.Path, "/api/token/") && method == "DELETE":
		return "检测密钥清理"
	case method == "POST" && newAPIKeyReadPath(u.Path):
		return "密钥读取"
	case strings.HasPrefix(u.Path, "/api/token/"):
		return "密钥列表/状态读取"
	default:
		return "站点接口"
	}
}

func newAPIKeyReadPath(path string) bool {
	return path == newAPIBatchKeyPath || (strings.HasPrefix(path, "/api/token/") && strings.HasSuffix(path, "/key"))
}

func newAPIRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

// Rate limits are shared by the upstream IP, not by console account/group.
// Serialize log-read reservations across accounts and rolling replicas. At
// most 90 log calls per 3 minutes leaves room for manual management under New
// API's documented default 180/3-minute global limit. Never skip refund checks.
func (s *Service) newAPIWaitLog(ctx context.Context, base string) error {
	gap := 2 * time.Second
	if s.newAPILogGap != nil {
		gap = *s.newAPILogGap
	}
	if gap <= 0 {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	site := strings.ToLower(u.Scheme + "://" + u.Host)
	var waitMS float64
	err = s.control.db.QueryRowContext(ctx, `INSERT INTO console_site_api_schedule(site,next_log_at) VALUES($1,clock_timestamp()+$2*interval '1 millisecond') ON CONFLICT(site) DO UPDATE SET next_log_at=greatest(console_site_api_schedule.next_log_at,clock_timestamp())+$2*interval '1 millisecond' RETURNING extract(epoch FROM (next_log_at-clock_timestamp()))*1000-$2`, site, float64(gap)/float64(time.Millisecond)).Scan(&waitMS)
	if err != nil {
		return err
	}
	wait := time.Duration(max(0, waitMS)) * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= wait {
		return &subRemoteError{Status: 429, Operation: "账单读取", RetryAfter: wait}
	}
	if !waitStartup(ctx, wait) {
		return ctx.Err()
	}
	return nil
}

func (s *Service) newAPIReadCall(ctx context.Context, account, base, method, path string, auth *subAuth, body, out any) error {
	u, _ := url.Parse(path)
	logRead := method == "GET" && u != nil && u.Path == "/api/log/self"
	keyRead := method == "POST" && u != nil && newAPIKeyReadPath(u.Path)
	sharedKeyBudget := keyRead && s.control != nil && s.control.db != nil
	if sharedKeyBudget {
		if e := s.siteDetectionCooldown(ctx, siteKeyScope(base), "waiting_keys"); e != nil {
			var pause *subSyncPause
			if subPlan(ctx) == nil && errors.As(e, &pause) {
				return &subRemoteError{Status: 429, Operation: "密钥读取", RetryAfter: time.Until(pause.until)}
			}
			return e
		}
		if e := s.reserveLegacyKeyRead(ctx, base); e != nil {
			if subPlan(ctx) != nil {
				return e
			}
			var pause *subSyncPause
			if errors.As(e, &pause) {
				return &subRemoteError{Status: 429, Operation: "密钥读取", RetryAfter: time.Until(pause.until)}
			}
			return e
		}
	}
	if logRead {
		if err := s.newAPIWaitLog(ctx, base); err != nil {
			return err
		}
	}
	err := newAPIJSON(ctx, base, method, path, auth, body, out)
	var remote *subRemoteError
	if !errors.As(err, &remote) {
		return err
	}
	// No query strings, bodies, tokens or upstream text enter diagnostics.
	log.Printf("event=newapi_remote_error account_id=%s operation=%q status=%d upstream_http_status=%d token_limit_reached=%t retry_after_ms=%d request_id=%q", account, remote.Operation, remote.Status, remote.upstreamHTTPStatus, remote.tokenLimitReached, remote.RetryAfter.Milliseconds(), consoleRequestID(ctx))
	if remote.Status != 429 {
		return err
	}
	if logRead {
		return err
	} // Durable worker cursor/backoff handles log reads.
	if sharedKeyBudget {
		pause := syncRatePause(err, "waiting_keys")
		if e := s.saveSiteDetectionCooldown(ctx, siteKeyScope(base), pause.until, false); e != nil {
			return e
		}
		if subPlan(ctx) != nil {
			return pause
		}
	}
	if subPlan(ctx) != nil {
		// A confirmed rejection of a create is distinct from a lost response.
		// Keep its HTTP status available to the durable creation-intent owner.
		return err
	}
	read := method == "GET" || keyRead
	if !read {
		return err
	} // Never replay a create/update/delete operation.
	delay := remote.RetryAfter
	if delay <= 0 {
		delay = 10 * time.Second
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= delay+time.Second {
		return err
	}
	if !waitStartup(ctx, delay) {
		return ctx.Err()
	}
	// One bounded retry only; long Retry-After values are respected, not capped.
	err = newAPIJSON(ctx, base, method, path, auth, body, out)
	if sharedKeyBudget && errors.As(err, &remote) && remote.Status == 429 {
		pause := syncRatePause(err, "waiting_keys")
		if e := s.saveSiteDetectionCooldown(ctx, siteKeyScope(base), pause.until, false); e != nil {
			return e
		}
	}
	return err
}

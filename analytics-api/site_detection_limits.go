package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const newAPIBatchKeyPath = "/api/token/batch/keys"

type subProbeLimits struct {
	RPM                  *int   `json:"rpm_limit,omitempty"`
	Concurrency          *int   `json:"concurrency,omitempty"`
	DeclaredRPM          *int   `json:"declared_rpm,omitempty"`
	DeclaredConcurrency  *int   `json:"declared_concurrency,omitempty"`
	EffectiveRPM         int    `json:"effective_rpm,omitempty"`
	EffectiveConcurrency int    `json:"effective_concurrency,omitempty"`
	EffectiveSource      string `json:"source,omitempty"`
}

func positiveProbeLimit(n *int) *int {
	if n == nil || *n <= 0 {
		return nil
	}
	return n
}

func probeCeiling(declared, manual *int, fallback, ceiling int) int {
	value := fallback
	if manual != nil && *manual > 0 {
		value = min(ceiling, *manual)
	}
	if declared != nil && *declared > 0 {
		// Cap before multiplication; remote JSON may contain any valid int.
		headroom := max(1, min(ceiling*2, *declared)*4/5)
		if manual != nil && *manual > 0 {
			headroom = min(headroom, *manual)
		}
		value = headroom
	}
	return min(ceiling, value)
}

func siteKeyScope(base string) string      { return "keys:" + fingerprintSite(base) }
func siteModelScope(account string) string { return "model:" + account }

func (s *Service) siteDetectionCooldown(ctx context.Context, scope, phase string) error {
	var retryAt time.Time
	err := s.control.db.QueryRowContext(ctx, `SELECT retry_at FROM console_site_detection_limits WHERE scope=$1 AND retry_at>now()`, scope).Scan(&retryAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return &subSyncPause{phase, retryAt}
}

func (s *Service) saveSiteDetectionCooldown(ctx context.Context, scope string, retryAt time.Time, adaptive bool) error {
	_, err := s.control.db.ExecContext(ctx, `INSERT INTO console_site_detection_limits(scope,retry_at,learned_rpm)
 VALUES($1,$2,CASE WHEN $3 THEN 15 ELSE 0 END) ON CONFLICT(scope) DO UPDATE SET
 retry_at=greatest(console_site_detection_limits.retry_at,excluded.retry_at),
 success_streak=0,learned_rpm=CASE WHEN $3 THEN greatest(4,CASE WHEN console_site_detection_limits.learned_rpm=0 THEN 30 ELSE console_site_detection_limits.learned_rpm END/2) ELSE console_site_detection_limits.learned_rpm END,
 learned_at=now()`, scope, retryAt, adaptive)
	return err
}

type siteModelBudget struct {
	scope, group               string
	rpm, concurrency, groupRPM int
	minGap                     time.Duration
	adaptive                   bool
}

func (s *Service) readSiteModelBudget(ctx context.Context, id string, group int64) (siteModelBudget, error) {
	b := siteModelBudget{scope: siteModelScope(id)}
	var raw []byte
	var groupRPM sql.NullInt64
	err := s.control.db.QueryRowContext(ctx, `SELECT a.probe_limits,t.probe_rpm FROM console_sub_accounts a
 LEFT JOIN console_sub_targets t ON t.account_id=a.id AND t.group_id=$2 WHERE a.id=$1`, id, group).Scan(&raw, &groupRPM)
	if err != nil {
		return b, err
	}
	var limits subProbeLimits
	if err := json.Unmarshal(raw, &limits); err != nil {
		return b, err
	}
	b.rpm = probeCeiling(limits.DeclaredRPM, limits.RPM, 30, 480)
	b.concurrency = probeCeiling(limits.DeclaredConcurrency, limits.Concurrency, 2, 8)
	b.adaptive = positiveProbeLimit(limits.DeclaredRPM) == nil && positiveProbeLimit(limits.RPM) == nil
	if groupRPM.Valid && groupRPM.Int64 > 0 {
		g := int(groupRPM.Int64)
		b.group, b.groupRPM = b.scope+":"+strconv.FormatInt(group, 10), probeCeiling(&g, nil, 30, 480)
		// Sub2API group RPM overrides the account fallback on the upstream,
		// but the console deliberately retains an account-wide safety budget.
	}
	return b, nil
}

func (s *Service) reserveLegacyKeyRead(ctx context.Context, base string) error {
	// Leave a margin below New API's default 20 reads in 20 minutes.
	// Custom operator limits can still require a Retry-After cooldown.
	gap := 75 * time.Second
	if s.newAPIKeyGap != nil {
		gap = *s.newAPIKeyGap
	}
	if gap <= 0 {
		return nil
	}
	_, err := s.acquireSiteDetection(ctx, siteModelBudget{scope: siteKeyScope(base), minGap: gap}, "waiting_keys")
	return err
}

// Serializes decisions, not network I/O. A short DB transaction reserves
// cadence and an expiring in-flight slot shared by rolling replicas.
func (s *Service) acquireSiteDetection(ctx context.Context, b siteModelBudget, phase string) (func(), error) {
	slot := ""
	if b.concurrency > 0 {
		slot = randomID()
	}
	for {
		wait, err := s.trySiteDetection(ctx, b, phase, slot)
		if err != nil {
			return nil, err
		}
		if wait <= 0 {
			break
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= wait {
			return nil, &subSyncPause{phase, time.Now().Add(wait)}
		}
		if !waitStartup(ctx, wait) {
			return nil, ctx.Err()
		}
	}
	if slot == "" {
		return func() {}, nil
	}
	return func() {
		ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_, _ = s.control.db.ExecContext(ctx, `DELETE FROM console_site_detection_slots WHERE scope=$1 AND slot=$2`, b.scope, slot)
	}, nil
}

func (s *Service) trySiteDetection(ctx context.Context, b siteModelBudget, phase, slot string) (time.Duration, error) {
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	due := time.Time{}
	rpm := b.rpm
	lock := func(scope string, adaptive bool) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO console_site_detection_limits(scope) VALUES($1) ON CONFLICT DO NOTHING`, scope)
		if err != nil {
			return err
		}
		var retry, next time.Time
		var cooling, fresh bool
		var learned int
		// Evaluate cooldown after the row lock is acquired. A timestamp
		// captured before waiting can already be stale on a busy account.
		if err := tx.QueryRowContext(ctx, `WITH locked AS MATERIALIZED (
 SELECT retry_at,next_at,learned_rpm,learned_at FROM console_site_detection_limits WHERE scope=$1 FOR UPDATE
 ) SELECT retry_at,next_at,learned_rpm,retry_at>clock_timestamp(),learned_at>clock_timestamp()-interval '24 hours' FROM locked`, scope).Scan(&retry, &next, &learned, &cooling, &fresh); err != nil {
			return err
		}
		if cooling {
			return &subSyncPause{phase, retry}
		}
		if adaptive && fresh && learned > 0 {
			rpm = min(90, learned)
		}
		if next.After(due) {
			due = next
		}
		return nil
	}
	if err := lock(b.scope, b.adaptive); err != nil {
		return 0, err
	}
	if b.group != "" {
		if err := lock(b.group, false); err != nil {
			return 0, err
		}
	}
	if due.After(time.Now()) {
		return time.Until(due), nil
	}
	if slot != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM console_site_detection_slots WHERE scope=$1 AND until_at<=now()`, b.scope); err != nil {
			return 0, err
		}
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM console_site_detection_slots WHERE scope=$1`, b.scope).Scan(&active); err != nil {
			return 0, err
		}
		if active >= b.concurrency {
			return 250 * time.Millisecond, nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO console_site_detection_slots(scope,slot,until_at) VALUES($1,$2,now()+interval '5 minutes')`, b.scope, slot); err != nil {
			return 0, err
		}
	}
	setNext := func(scope string, gap time.Duration) error {
		_, err := tx.ExecContext(ctx, `UPDATE console_site_detection_limits SET next_at=clock_timestamp()+$2*interval '1 millisecond' WHERE scope=$1`, scope, gap.Milliseconds())
		return err
	}
	gap := b.minGap
	if gap == 0 {
		gap = time.Minute / time.Duration(max(1, rpm))
	}
	if err := setNext(b.scope, gap); err != nil {
		return 0, err
	}
	if b.group != "" {
		if err := setNext(b.group, time.Minute/time.Duration(b.groupRPM)); err != nil {
			return 0, err
		}
	}
	return 0, tx.Commit()
}

type siteProbeTransport struct {
	s            *Service
	base         http.RoundTripper
	budget       siteModelBudget
	account, job string
	timeout      time.Duration
}

func (s *Service) siteProbeClient(ctx context.Context, base *http.Client, id string, group int64, job string) (*http.Client, error) {
	budget, err := s.readSiteModelBudget(ctx, id, group)
	if err != nil {
		return nil, err
	}
	client := *base
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = siteProbeTransport{s, transport, budget, id, job, base.Timeout}
	// Time waiting for a request slot must not consume the network timeout.
	client.Timeout = 0
	return &client, nil
}

type siteProbeBody struct {
	io.ReadCloser
	once sync.Once
	done func()
}

func (b *siteProbeBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.done)
	return err
}

type siteProbeRejection struct {
	kind  string
	pause *subSyncPause
}

func (e *siteProbeRejection) Error() string { return e.kind }
func (e *siteProbeRejection) Unwrap() error {
	if e.pause == nil {
		return nil
	}
	return e.pause
}

func probeHTTPRejection(response *http.Response) string {
	switch response.StatusCode {
	case 401, 403:
		return "auth_rejected"
	case 402:
		return "quota_rejected"
	case 429:
	default:
		return ""
	}
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(raw))
	// Use upstream error codes and a few unambiguous messages only for
	// classification. Never store or log arbitrary upstream text.
	var envelope struct {
		Code, Message string
		Error         struct{ Code, Type, Message string }
	}
	_ = json.Unmarshal(raw, &envelope)
	hint := strings.ToLower(strings.Join([]string{envelope.Code, envelope.Message, envelope.Error.Code, envelope.Error.Type, envelope.Error.Message}, " "))
	for _, quota := range []string{"quota", "insufficient balance", "余额不足", "额度不足", "额度已用尽"} {
		if strings.Contains(hint, quota) {
			return "quota_rejected"
		}
	}
	for _, rate := range []string{"rate_limit", "rate limit", "request limit", "too many requests", "concurrency", "限流", "每分钟", "並發", "并发", "频率", "请求数限制", "請求數限制", "请求次数"} {
		if strings.Contains(hint, rate) {
			return "rate_limited"
		}
	}
	if newAPIRetryAfter(response.Header.Get("Retry-After"), time.Now()) > 0 {
		return "rate_limited"
	}
	return "unclassified_429"
}

func (t siteProbeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	release, err := t.s.acquireSiteDetection(ctx, t.budget, "waiting_models")
	if err != nil {
		return nil, err
	}
	if t.job != "" {
		var live bool
		err = t.s.control.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_sub_accounts
 WHERE id=$1 AND job_id=$2 AND state='running'
 AND (sync_lease_until IS NULL OR sync_lease_until>clock_timestamp()))`, t.account, t.job).Scan(&live)
		if err != nil || !live {
			release()
			if err != nil {
				return nil, err
			}
			return nil, context.Canceled
		}
	}
	cancel := func() {}
	if t.timeout > 0 {
		var requestCtx context.Context
		requestCtx, cancel = context.WithTimeout(ctx, t.timeout)
		req = req.WithContext(requestCtx)
	}
	response, err := t.base.RoundTrip(req)
	if err != nil {
		cancel()
		release()
		return nil, err
	}
	switch kind := probeHTTPRejection(response); kind {
	case "":
		response.Body = &siteProbeBody{ReadCloser: response.Body, done: func() {
			cancel()
			release()
			if t.budget.adaptive && response.StatusCode == http.StatusOK {
				saveCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
				defer done()
				_, _ = t.s.control.db.ExecContext(saveCtx, `UPDATE console_site_detection_limits SET
 learned_rpm=CASE WHEN learned_at < now()-interval '24 hours' THEN 30
 WHEN success_streak>=23 THEN least(90,coalesce(nullif(learned_rpm,0),30)+4) ELSE learned_rpm END,
 success_streak=CASE WHEN success_streak>=23 OR learned_at < now()-interval '24 hours' THEN 0 ELSE success_streak+1 END,
 learned_at=now() WHERE scope=$1 AND retry_at<=now()`, t.budget.scope)
			}
		}}
		return response, nil
	default:
		response.Body.Close()
		cancel()
		var pause *subSyncPause
		if kind == "rate_limited" {
			delay := newAPIRetryAfter(response.Header.Get("Retry-After"), time.Now())
			if delay <= 0 {
				delay = 30 * time.Second
			}
			pause = &subSyncPause{"waiting_models", time.Now().Add(delay)}
			saveCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
			err = t.s.saveSiteDetectionCooldown(saveCtx, t.budget.scope, pause.until, t.budget.adaptive)
			done()
			if err != nil {
				release()
				return nil, err
			}
		}
		release()
		return nil, &siteProbeRejection{kind, pause}
	}
}

func applySiteProbeRejection(out *subProbe, err error) bool {
	var rejection *siteProbeRejection
	var pause *subSyncPause
	switch {
	case errors.As(err, &rejection):
		out.Status = rejection.kind
		out.pause = rejection.pause
		switch rejection.kind {
		case "auth_rejected":
			out.Message = "上游拒绝测试凭据或访问权限"
		case "quota_rejected":
			out.Message = "上游余额或配额不足"
		default:
			out.Message = "上游拒绝或限制本次请求，保留上次检测结论"
			out.HTTPStatus = 429
		}
	case errors.As(err, &pause):
		out.Status, out.Message, out.pause = "rate_limited", "等待上游限流冷却，未发出请求", pause
	default:
		return false
	}
	return true
}

func isSiteBlockedProbe(p subProbe) bool {
	return p.Status == "rate_limited" || p.Status == "unclassified_429" || p.Status == "auth_rejected" || p.Status == "quota_rejected"
}

func siteBlockedProbe(result subResult) *subProbe {
	for _, p := range []subProbe{result.Availability, result.Quality} {
		if isSiteBlockedProbe(p) {
			return &p
		}
	}
	return nil
}

func (s *Service) recordSiteBlockedModel(ctx context.Context, id, job string, group, keyID int64, model, history string, result subResult, blocked *subProbe, resumable bool) error {
	state := "interrupted"
	// If availability already ran, replaying the combined item would repeat
	// a paid request. Keep it in receipts, and leave the prior outcome intact.
	if result.Availability.Status == "success" {
		result.Quality.ID, result.Quality.RequestIDs = "", nil
		_ = s.subQueueUsage(ctx, id, group, keyID, &result)
	} else if resumable && blocked.pause != nil {
		state = "queued"
	}
	if model == checkModel {
		if err := s.control.finishQuality(history, "skipped", false, time.Now().Unix(), nil); err != nil {
			return err
		}
	}
	_, err := s.subProbeWrite(ctx, `UPDATE console_sub_models SET state=$5,message=$6
 WHERE account_id=$1 AND group_id=$2 AND model=$3 AND state='running'
 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$4 AND state='running'
 AND (sync_lease_until IS NULL OR sync_lease_until>clock_timestamp()))`, id, group, model, job, state, blocked.Message)
	if err != nil {
		return err
	}
	if blocked.pause != nil {
		return blocked.pause
	}
	return nil
}

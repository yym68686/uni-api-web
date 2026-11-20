package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type newAPIImportSourceContext struct{}

func (s *Service) clearNewAPIPresentObservations(ctx context.Context, account, base string, present []string) error {
	_, err := s.control.db.ExecContext(ctx, `UPDATE console_sub_targets t SET newapi_missing_since=NULL,newapi_last_missing_at=NULL
 FROM console_site_groups g,console_sub_accounts a WHERE t.account_id=$1 AND t.newapi_missing_since IS NOT NULL
 AND a.id=t.account_id AND a.base=$2 AND g.account_id=t.account_id AND g.id=t.group_id AND g.remote_key=ANY($3::text[])`,
		account, base, present)
	return err
}

func recordNewAPIGroupObservations(ctx context.Context, tx *sql.Tx, account string) error {
	_, err := tx.ExecContext(ctx, `UPDATE console_sub_targets SET
 newapi_missing_since=CASE WHEN active THEN NULL ELSE COALESCE(newapi_missing_since,statement_timestamp()) END,
 newapi_last_missing_at=CASE WHEN active THEN NULL ELSE statement_timestamp() END
 WHERE account_id=$1 AND (NOT active OR newapi_missing_since IS NOT NULL)`, account)
	return err
}

// The panel returns no ID on POST success. Associate an acknowledged creation
// with the exact token we adopted from the inventory, inside the creation
// window. Never backfill ownership from a name or an uncertain POST alone.
func (s *Service) rememberNewAPITestKey(ctx context.Context, account, base, job string, group int64, token newAPIToken, user int64) {
	if token.UserID <= 0 || token.UserID != user || token.Created <= 0 {
		return
	}
	_, err := s.control.db.ExecContext(ctx, `UPDATE console_site_key_creates c
 SET owned_key_id=$4,owned_user_id=$5,owned_created_at=$6
 WHERE c.account_id=$1 AND c.acknowledged_base=$2 AND c.name=$3 AND c.owned_key_id=0
 AND to_timestamp($6::bigint) BETWEEN date_trunc('second',c.created_at)-interval '10 seconds'
                                  AND c.acknowledged_at+interval '10 seconds'
 AND EXISTS(SELECT 1 FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id
 WHERE t.account_id=$1 AND t.group_id=$7 AND t.remote_key_id=$4 AND a.job_id=$8 AND a.base=$2 AND t.active)`,
		account, base, subKeyName(account, group), token.ID, user, token.Created, group, job)
	if err != nil {
		log.Printf("event=site_test_key_ownership_bind_failed class=%s", databaseErrorClass(err))
	}
}

type newAPIReclaimCandidate struct {
	group, id, user, created int64
	remote, encrypted        string
	missing                  time.Time
	lastUsed                 int64
}

// The same predicates apply both during selection and with the account row
// fenced immediately before sending DELETE. Historical facts are only read.
const newAPICleanupCandidates = `
 SELECT t.group_id,t.remote_key_id,c.owned_user_id,c.owned_created_at,g.remote_key,t.encrypted_key,t.newapi_missing_since,
 GREATEST(COALESCE((SELECT max(u.started_at) FROM console_sub_usage u WHERE u.account_id=$1 AND u.key_id=t.remote_key_id),0),
          COALESCE((SELECT max(l.at_ms)/1000 FROM console_sub_spend_logs l WHERE l.account_id=$1 AND l.key_id=t.remote_key_id),0))
 FROM console_sub_targets t JOIN console_site_groups g ON g.account_id=t.account_id AND g.id=t.group_id
 JOIN console_site_key_creates c ON c.account_id=t.account_id AND c.name='uni-console-check-'||t.account_id||'-'||t.group_id
 WHERE t.account_id=$1 AND c.acknowledged_base=$2 AND NOT t.active AND t.remote_key_id>0 AND t.encrypted_key<>''
 AND c.owned_key_id=t.remote_key_id AND c.owned_user_id>0 AND c.owned_created_at>0 AND c.acknowledged_at IS NOT NULL
 AND t.newapi_last_missing_at >= t.newapi_missing_since+interval '7 days'
 AND t.state NOT IN ('queued','running') AND t.compaction_state NOT IN ('queued','running') AND t.tool_use_state NOT IN ('queued','running')
 AND NOT EXISTS(SELECT 1 FROM console_sub_targets r WHERE r.account_id=$1 AND
   (r.routing_key_id=t.remote_key_id OR r.group_id<>t.group_id AND r.remote_key_id=t.remote_key_id))
 AND NOT EXISTS(SELECT 1 FROM console_sub_models m WHERE m.account_id=$1 AND m.group_id=t.group_id AND m.state IN ('queued','running'))
 AND NOT EXISTS(SELECT 1 FROM console_sub_check_queue q WHERE q.account_id=$1 AND q.group_id=t.group_id)
 AND NOT EXISTS(SELECT 1 FROM console_model_fingerprints f WHERE f.account_id=$1 AND f.group_id=t.group_id AND f.state IN ('queued','running'))
 AND NOT EXISTS(SELECT 1 FROM console_sub_usage u WHERE u.account_id=$1 AND (u.key_id=t.remote_key_id OR u.group_id=t.group_id)
   AND u.status IN ('pending','missing'))
 AND NOT EXISTS(SELECT 1 FROM console_newapi_key_reclaims r WHERE r.account_id=$1 AND r.base=$2 AND r.remote_key_id=t.remote_key_id
   AND r.state IN ('deleted','rejected'))
 AND ($3::bigint=0 OR t.remote_key_id=$3)`

func readNewAPIReclaimCandidates(ctx context.Context, db controlQueryDB, account, base string, key int64) ([]newAPIReclaimCandidate, error) {
	rows, err := db.QueryContext(ctx, newAPICleanupCandidates, account, base, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []newAPIReclaimCandidate
	for rows.Next() {
		var c newAPIReclaimCandidate
		if err := rows.Scan(&c.group, &c.id, &c.user, &c.created, &c.remote, &c.encrypted, &c.missing, &c.lastUsed); err != nil {
			return nil, err
		}
		if c.lastUsed < c.missing.Unix() {
			candidates = append(candidates, c)
		}
	}
	return candidates, rows.Err()
}

func (c newAPIReclaimCandidate) matches(account string, token newAPIToken) bool {
	return token.ID == c.id && token.UserID == c.user && token.Name == subKeyName(account, c.group) &&
		token.Group == c.remote && token.Created == c.created && token.Accessed >= token.Created && token.Accessed < c.missing.Unix()
}

// An inventory hit anywhere is a veto, even for a locally disabled channel.
// Check live effective providers too, including temporary routes that the
// configured-channel inventory intentionally filters out.
func (s *Service) newAPIReclaimRouteClear(ctx context.Context, tx *sql.Tx, sources []controlSource, secret string) (bool, error) {
	var referenced bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_configured_channels WHERE key_hashes ? $1 OR key_hashes ? $2)`,
		tokenHash(secret), tokenHash(strings.TrimPrefix(secret, "sk-"))).Scan(&referenced)
	if err != nil || referenced {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT encrypted_snapshot FROM console_control_snapshots WHERE encrypted_snapshot<>''
 UNION ALL SELECT encrypted_snapshot FROM console_control_bootstraps
 UNION ALL SELECT v.encrypted_bundle FROM console_managed_config_state s JOIN console_managed_config_versions v
 ON s.source_id=v.source_id AND v.revision IN (s.published_revision,s.draft_revision)`)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var encrypted string
		if err := rows.Scan(&encrypted); err != nil {
			rows.Close()
			return false, err
		}
		raw, err := s.control.decrypt(encrypted)
		if err != nil || !json.Valid([]byte(raw)) {
			rows.Close()
			return false, errors.New("无法核对已保存的路由配置")
		}
		if strings.Contains(raw, strings.TrimPrefix(secret, "sk-")) {
			rows.Close()
			return false, nil
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, src := range sources {
		if src.configKeyError != nil {
			return false, src.configKeyError
		}
		if src.ConfigKey != "" {
			src.Key = src.ConfigKey
		}
		// A static config fallback cannot prove that temporary routes are absent.
		res, _, err := subGateway(ctx, src, http.MethodGet, "/v1/channel-settings/providers", nil)
		var providers []configuredProvider
		if err != nil || decodeMap(res["providers"], &providers) != nil || providers == nil {
			return false, errors.New("无法核对来源的实时路由引用")
		}
		for _, provider := range providers {
			for _, key := range providerKeys(provider.API) {
				if newAPIKeyValue(key) == secret {
					return false, nil
				}
			}
		}
	}
	return true, nil
}

// Prevent another console operation from adding or restoring a route between
// the final route check and DELETE. All locks share one short transaction;
// contention skips cleanup rather than waiting while another import is active.
func (s *Service) newAPIReclaimRouteLocks(ctx context.Context, tx *sql.Tx) ([]controlSource, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM console_sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	held, _ := ctx.Value(newAPIImportSourceContext{}).(string)
	sources := make([]controlSource, 0, len(ids))
	for _, id := range ids {
		if id != held {
			var locked bool
			if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, controlLockID(id)).Scan(&locked); err != nil || !locked {
				return nil, errControlsBusy
			}
		}
		src, err := s.control.scanControlSource(tx.QueryRowContext(ctx, `SELECT `+controlSourceColumns+` FROM console_sources WHERE id=$1`, id))
		if err != nil {
			return nil, err
		}
		sources = append(sources, src)
	}
	return sources, nil
}

type newAPIReclaimError struct {
	cause   error
	message string
}

func (e *newAPIReclaimError) Error() string { return e.message }
func (e *newAPIReclaimError) Unwrap() error { return e.cause }

func (s *Service) newAPICreateWithReclaim(ctx context.Context, account, base, job, name, group string) error {
	err := s.newAPICreateOwnedKey(ctx, account, base, job, name, group)
	var remote *subRemoteError
	if job == "" || !errors.As(err, &remote) || !remote.tokenLimitReached || remote.requiresBrowser() {
		return err
	}
	scope := job
	if p := subPlan(ctx); p != nil && p.run != "" {
		scope = p.run
	}
	reclaimed, cleanupErr := s.newAPIReclaimOne(ctx, account, base, job, scope, group)
	if cleanupErr != nil {
		return &newAPIReclaimError{err, "站点账号已达到令牌数量上限；" + cleanupErr.Error()}
	}
	if !reclaimed {
		return &newAPIReclaimError{err, "站点账号已达到令牌数量上限，未找到可安全清理的检测 Key，或本轮已清理过；请在原站核对令牌"}
	}
	// Exactly one create retry with the same pacing, durable intent and
	// uncertain-write handling. Never recursively reclaim on another rejection.
	return s.newAPICreateOwnedKey(ctx, account, base, job, name, group)
}

func (s *Service) newAPIReclaimOne(parent context.Context, account, base, job, scope, wantedGroup string) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	var already bool
	if err := s.control.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_newapi_key_reclaims
 WHERE account_id=$1 AND attempt_scope=$2)`, account, scope).Scan(&already); err != nil || already {
		return false, err
	}
	candidates, err := readNewAPIReclaimCandidates(ctx, s.control.db, account, base, 0)
	if err != nil {
		return false, errors.New("无法核对检测 Key 的本地使用记录")
	}
	var pendingScope string
	var pendingGroup, pendingKey int64
	err = s.control.db.QueryRowContext(ctx, `SELECT attempt_scope,group_id,remote_key_id FROM console_newapi_key_reclaims
 WHERE account_id=$1 AND base=$2 AND state='pending'`, account, base).Scan(&pendingScope, &pendingGroup, &pendingKey)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, errors.New("无法核对上次清理结果")
	}
	if pendingKey == 0 && len(candidates) == 0 {
		return false, nil
	}
	auth, _, err := s.newAPIAuthAt(ctx, account, base)
	if err != nil || auth.UserID <= 0 {
		return false, errors.New("无法验证站点账号，未清理任何 Key")
	}
	var groups map[string]newAPIGroup
	if err := s.newAPICall(ctx, account, base, http.MethodGet, "/api/user/self/groups", nil, &groups); err != nil {
		return false, errors.New("无法复核站点分组，未清理任何 Key")
	}
	if _, ok := groups[wantedGroup]; !ok {
		return false, errors.New("目标分组当前不可用，未清理任何 Key")
	}
	present := make([]string, 0, len(groups))
	for name := range groups {
		present = append(present, name)
	}
	if err := s.clearNewAPIPresentObservations(ctx, account, base, present); err != nil {
		return false, errors.New("无法保存最新分组状态，未清理任何 Key")
	}
	inventory, err := s.newAPIReadTokenInventory(ctx, account, base, true)
	if err != nil {
		return false, errors.New("无法完整复核站点令牌，未清理任何 Key")
	}
	tokens, names := map[int64]newAPIToken{}, map[string]int{}
	for _, token := range inventory {
		tokens[token.ID] = token
		names[token.Name]++
	}
	if pendingKey != 0 {
		// A new run may reconcile an old attempt but must not replay DELETE.
		if _, exists := tokens[pendingKey]; exists {
			return false, errors.New("上次清理结果尚未确认，未再次删除；请在原站核对")
		}
		// Merely reconcile here: a new capacity rejection was received even
		// with this key already absent, so it is not evidence of a free slot.
		return false, s.finishNewAPIReclaim(ctx, account, base, pendingScope, pendingGroup, pendingKey)
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := max(candidates[i].lastUsed, tokens[candidates[i].id].Accessed), max(candidates[j].lastUsed, tokens[candidates[j].id].Accessed)
		if a == b {
			return candidates[i].id < candidates[j].id
		}
		return a < b
	})
	routes, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("无法锁定路由状态，未清理任何 Key")
	}
	defer routes.Rollback()
	sources, err := s.newAPIReclaimRouteLocks(ctx, routes)
	if err != nil {
		return false, errors.New("路由状态无法核对或正在修改，未清理任何 Key")
	}
	for _, c := range candidates {
		token := tokens[c.id]
		if _, available := groups[c.remote]; available || c.user != auth.UserID || !c.matches(account, token) || names[token.Name] != 1 {
			continue
		}
		plain, err := s.control.decrypt(c.encrypted)
		secret := newAPIKeyValue(plain)
		if err != nil || secret == "" {
			continue
		}
		clear, err := s.newAPIReclaimRouteClear(ctx, routes, sources, secret)
		if err != nil {
			return false, errors.New("无法核对路由引用，未清理任何 Key")
		}
		if clear {
			return s.sendNewAPIReclaim(ctx, account, base, job, scope, c, auth, secret)
		}
	}
	return false, nil
}

func (s *Service) sendNewAPIReclaim(ctx context.Context, account, base, job, scope string, c newAPIReclaimCandidate, auth subAuth, secret string) (bool, error) {
	path := "/api/token/" + strconv.FormatInt(c.id, 10)
	var fresh newAPIToken
	// Use the same validated account on the last direct lookup and DELETE.
	if err := newAPIJSON(ctx, base, http.MethodGet, path, &auth, nil, &fresh); err != nil || !c.matches(account, fresh) {
		return false, errors.New("检测 Key 的远端身份或使用记录已变动，未删除")
	}
	actual := newAPIKeyValue(fresh.Key)
	if actual == "" {
		var data struct {
			Key string `json:"key"`
		}
		if err := s.newAPICall(ctx, account, base, http.MethodPost, path+"/key", nil, &data); err != nil {
			return false, errors.New("无法确认检测 Key 与站点原件一致，未删除")
		}
		actual = newAPIKeyValue(data.Key)
	}
	if actual != secret {
		return false, errors.New("检测 Key 与站点原件不一致，未删除")
	}
	// Some New API builds initialize accessed_time but do not update it on
	// every model call. Require a clean upstream usage window as well.
	q := url.Values{"p": {"1"}, "page_size": {"1"}, "token_name": {fresh.Name},
		"start_timestamp": {strconv.FormatInt(c.missing.Unix(), 10)}}
	var usage struct {
		Total *int        `json:"total"`
		Items []newAPILog `json:"items"`
	}
	if err := s.newAPICall(ctx, account, base, http.MethodGet, "/api/log/self?"+q.Encode(), nil, &usage); err != nil ||
		usage.Total == nil || *usage.Total != 0 || usage.Items == nil || len(usage.Items) != 0 {
		return false, errors.New("检测 Key 存在近期用量或无法确认站点用量，未删除")
	}
	n, err := s.subProbeWrite(ctx, `INSERT INTO console_newapi_key_reclaims(account_id,base,attempt_scope,group_id,remote_key_id)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, account, base, scope, c.group, c.id)
	if err != nil || n != 1 {
		return false, errors.New("无法登记本次清理，未发送删除")
	}
	sent := false
	defer func() {
		if !sent {
			s.setNewAPIReclaimState(account, scope, "not_sent")
		}
	}()
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("无法锁定账号，未发送删除")
	}
	defer tx.Rollback()
	var locked string
	// Fence stop/re-login, probe promotion and usage claim before sending a
	// destructive request. The lock is held only for the bounded DELETE.
	err = tx.QueryRowContext(ctx, `SELECT id FROM console_sub_accounts WHERE id=$1 AND base=$2 AND job_id=$3
 AND state='running' AND provider_kind='newapi'
 AND (CASE WHEN job_kind='sync' THEN COALESCE(sync_lease_until,lease_until) ELSE lease_until END)>clock_timestamp()+interval '7 seconds'
 AND (usage_lease_until IS NULL OR usage_lease_until<=clock_timestamp()) FOR UPDATE NOWAIT`, account, base, job).Scan(&locked)
	if err != nil {
		return false, errors.New("账号任务或账单状态已变化，未发送删除")
	}
	candidates, err := readNewAPIReclaimCandidates(ctx, tx, account, base, c.id)
	if err != nil || len(candidates) != 1 || !c.same(candidates[0]) {
		return false, errors.New("检测 Key 的本地使用状态已变化，未发送删除")
	}
	var referenced bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_configured_channels WHERE key_hashes ? $1)`, tokenHash(secret)).Scan(&referenced); err != nil || referenced {
		return false, errors.New("检测 Key 的路由引用已变化，未发送删除")
	}
	if ctx.Err() != nil {
		return false, errors.New("清理超时，未发送删除")
	}
	if err := s.siteDetectionCooldown(ctx, newAPIKeyCreateScope(base), waitingKeyCreate); err != nil {
		return false, errors.New("站点正在冷却或无法核对冷却状态，未发送删除")
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	sent = true
	err = newAPIJSON(writeCtx, base, http.MethodDelete, path, &auth, nil, nil)
	cancel()
	_ = tx.Rollback()
	if err != nil {
		var remote *subRemoteError
		if errors.As(err, &remote) && remote.upstreamHTTPStatus >= 400 && remote.upstreamHTTPStatus < 500 && remote.upstreamHTTPStatus != 404 {
			s.setNewAPIReclaimState(account, scope, "rejected")
			if pause := syncRatePause(remote, waitingKeyCreate); pause != nil {
				writeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				if err := s.saveSiteDetectionCooldown(writeCtx, newAPIKeyCreateScope(base), pause.until, false); err != nil {
					log.Printf("event=site_test_key_reclaim_cooldown_failed class=%s", databaseErrorClass(err))
				}
				cancel()
				return false, errors.New("清理触发站点限流，已停止本轮重试")
			}
			return false, errors.New("站点拒绝清理检测 Key，未重试创建")
		}
		return false, errors.New("检测 Key 删除结果待核对，不会重放删除或重试创建")
	}
	inventory, err := s.newAPIReadTokenInventory(ctx, account, base, true)
	if err != nil {
		return false, errors.New("无法确认检测 Key 已删除，未重试创建")
	}
	for _, token := range inventory {
		if token.ID == c.id {
			return false, errors.New("站点令牌清单仍有该 Key，未重试创建")
		}
	}
	if err := s.finishNewAPIReclaim(ctx, account, base, scope, c.group, c.id); err != nil {
		return false, err
	}
	log.Printf("event=site_test_key_reclaimed account_id=%s group_id=%d remote_key_id=%d request_id=%q", account, c.group, c.id, consoleRequestID(ctx))
	return true, nil
}

func (c newAPIReclaimCandidate) same(other newAPIReclaimCandidate) bool {
	return c.group == other.group && c.id == other.id && c.user == other.user && c.created == other.created &&
		c.encrypted == other.encrypted && c.remote == other.remote && c.missing.Equal(other.missing) && c.lastUsed == other.lastUsed
}

func (s *Service) setNewAPIReclaimState(account, scope, state string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := s.control.db.ExecContext(ctx, `UPDATE console_newapi_key_reclaims SET state=$3,resolved_at=now()
 WHERE account_id=$1 AND attempt_scope=$2 AND state='pending'`, account, scope, state)
	if err != nil {
		log.Printf("event=site_test_key_reclaim_audit_failed class=%s", databaseErrorClass(err))
	}
}

func (s *Service) finishNewAPIReclaim(ctx context.Context, account, base, scope string, group, key int64) error {
	var saved int
	err := s.control.db.QueryRowContext(ctx, `WITH completed AS (
 UPDATE console_newapi_key_reclaims SET state='deleted',resolved_at=now()
 WHERE account_id=$1 AND attempt_scope=$3 AND base=$2 AND state='pending' AND group_id=$4 AND remote_key_id=$5 RETURNING account_id
 ), retired_key AS (
 UPDATE console_sub_targets SET remote_key_id=0,encrypted_key='',sync_key_state=''
 WHERE account_id IN (SELECT account_id FROM completed) AND group_id=$4 AND remote_key_id=$5
 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND base=$2)
 ), released_name AS (
 DELETE FROM console_site_key_creates WHERE account_id IN (SELECT account_id FROM completed)
 AND name=$6 AND acknowledged_base=$2 AND owned_key_id=$5
 ) SELECT 1 FROM completed`, account, base, scope, group, key, subKeyName(account, group)).Scan(&saved)
	if err != nil {
		return errors.New("清理结果保存失败，未重试创建")
	}
	return nil
}

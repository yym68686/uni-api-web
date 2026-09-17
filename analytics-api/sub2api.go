package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const subSchema = `
CREATE TABLE IF NOT EXISTS console_sub_accounts(
 id TEXT PRIMARY KEY, owner TEXT NOT NULL, name TEXT NOT NULL, base TEXT NOT NULL, email TEXT NOT NULL,
 encrypted_auth TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'idle', message TEXT NOT NULL DEFAULT '',
 job_kind TEXT NOT NULL DEFAULT '', job_id TEXT NOT NULL DEFAULT '', lease_until TIMESTAMPTZ,
 synced_at BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(owner,base,email));
CREATE INDEX IF NOT EXISTS console_sub_accounts_owner ON console_sub_accounts(owner);
CREATE TABLE IF NOT EXISTS console_sub_targets(
 account_id TEXT NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE, group_id BIGINT NOT NULL,
 name TEXT NOT NULL, platform TEXT NOT NULL, channel TEXT NOT NULL DEFAULT '', rate DOUBLE PRECISION NOT NULL DEFAULT 1,
 remote_key_id BIGINT NOT NULL DEFAULT 0, encrypted_key TEXT NOT NULL DEFAULT '', active BOOLEAN NOT NULL DEFAULT true,
 state TEXT NOT NULL DEFAULT 'idle', message TEXT NOT NULL DEFAULT '', result JSONB,
 PRIMARY KEY(account_id,group_id));
ALTER TABLE console_sub_targets ADD COLUMN IF NOT EXISTS billing JSONB;`

type subTarget struct {
	GroupID  int64       `json:"group_id"`
	Name     string      `json:"name"`
	Platform string      `json:"platform"`
	Channel  string      `json:"channel"`
	Rate     float64     `json:"rate"`
	KeyID    int64       `json:"key_id"`
	Active   bool        `json:"active"`
	State    string      `json:"state"`
	Message  string      `json:"message"`
	Result   *subResult  `json:"result"`
	Billing  *subBilling `json:"billing"`
}
type subAccount struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Base     string      `json:"base"`
	Email    string      `json:"email"`
	State    string      `json:"state"`
	Message  string      `json:"message"`
	SyncedAt int64       `json:"synced_at"`
	Targets  []subTarget `json:"targets"`
}
type subChallenge struct {
	Owner, Base, Email, Name, Temp string
	Until                          int64
}

func (s *Service) subAccounts(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	rows, err := s.control.db.QueryContext(r.Context(), `SELECT id,name,base,email,state,message,synced_at FROM console_sub_accounts WHERE owner=$1 ORDER BY created_at,id`, owner)
	if err != nil {
		http.Error(w, "账号列表暂不可用", 503)
		return
	}
	accounts := []subAccount{}
	index := map[string]int{}
	for rows.Next() {
		var a subAccount
		if err = rows.Scan(&a.ID, &a.Name, &a.Base, &a.Email, &a.State, &a.Message, &a.SyncedAt); err != nil {
			break
		}
		a.Targets = []subTarget{}
		index[a.ID] = len(accounts)
		accounts = append(accounts, a)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		http.Error(w, "账号列表读取失败", 503)
		return
	}
	rows, err = s.control.db.QueryContext(r.Context(), `SELECT t.account_id,t.group_id,t.name,t.platform,t.channel,t.rate,t.remote_key_id,t.active,t.state,t.message,t.result,t.billing FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id WHERE a.owner=$1 ORDER BY t.group_id`, owner)
	if err != nil {
		http.Error(w, "检测结果暂不可用", 503)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var t subTarget
		var raw, billingRaw []byte
		if err = rows.Scan(&id, &t.GroupID, &t.Name, &t.Platform, &t.Channel, &t.Rate, &t.KeyID, &t.Active, &t.State, &t.Message, &raw, &billingRaw); err != nil {
			break
		}
		if len(billingRaw) > 0 {
			if err = json.Unmarshal(billingRaw, &t.Billing); err != nil {
				break
			}
		}
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &t.Result); err != nil {
				break
			}
		}
		if i, ok := index[id]; ok {
			accounts[i].Targets = append(accounts[i].Targets, t)
		}
	}
	if err != nil || rows.Err() != nil {
		http.Error(w, "检测结果读取失败", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"data": accounts})
}

func (s *Service) subAddAccount(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	var in struct {
		Name      string `json:"name"`
		Base      string `json:"base"`
		Email     string `json:"email"`
		Password  string `json:"password"`
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		Challenge string `json:"challenge"`
		Code      string `json:"totp_code"`
	}
	if !decodeControl(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var auth subAuth
	var err error
	if in.Challenge != "" {
		plain, e := s.control.decrypt(in.Challenge)
		var challenge subChallenge
		if e != nil || json.Unmarshal([]byte(plain), &challenge) != nil || challenge.Owner != owner || challenge.Until < time.Now().Unix() {
			http.Error(w, "验证会话已失效，请重新登录", 400)
			return
		}
		in.Base, in.Email, in.Name = challenge.Base, challenge.Email, challenge.Name
		if len(in.Code) != 6 {
			http.Error(w, "请输入六位验证码", 400)
			return
		}
		err = subJSON(ctx, subHTTP, in.Base, "POST", "/api/v1/auth/login/2fa", "", map[string]string{"temp_token": challenge.Temp, "totp_code": in.Code}, &auth, "")
	} else {
		in.Base, err = subBase(in.Base)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		in.Email = strings.TrimSpace(in.Email)
		in.Name = strings.TrimSpace(in.Name)
		if len(in.Name) > 120 || len(in.Email) > 320 || in.Email == "" || len(in.Password) > 1024 || len(in.Access) > 8192 || len(in.Refresh) > 8192 {
			http.Error(w, "账号信息无效", 400)
			return
		}
		if in.Name == "" {
			u, _ := url.Parse(in.Base)
			in.Name = u.Hostname()
		}
		if in.Access != "" {
			auth = subAuth{Access: strings.TrimSpace(in.Access), Refresh: strings.TrimSpace(in.Refresh)}
			var user struct {
				Email string `json:"email"`
			}
			err = subJSON(ctx, subHTTP, in.Base, "GET", "/api/v1/auth/me", auth.Access, nil, &user, "")
			if err == nil && !strings.EqualFold(user.Email, in.Email) {
				http.Error(w, "会话令牌与填写的邮箱不一致", 400)
				return
			}
		} else {
			if in.Password == "" {
				http.Error(w, "请输入密码或会话令牌", 400)
				return
			}
			err = subJSON(ctx, subHTTP, in.Base, "POST", "/api/v1/auth/login", "", map[string]string{"email": in.Email, "password": in.Password}, &auth, "")
		}
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if auth.Requires2FA {
		raw, _ := json.Marshal(subChallenge{Owner: owner, Base: in.Base, Email: in.Email, Name: in.Name, Temp: auth.Temp, Until: time.Now().Add(5 * time.Minute).Unix()})
		challenge, e := s.control.encrypt(string(raw))
		if e != nil {
			http.Error(w, "验证会话创建失败", 503)
			return
		}
		writeJSON(w, 200, map[string]any{"requires_2fa": true, "challenge": challenge})
		return
	}
	if auth.Access == "" {
		http.Error(w, "站点未返回登录凭据", 400)
		return
	}
	// Verify the authenticated identity even after the TOTP exchange.
	var profile struct {
		Email string `json:"email"`
	}
	if err = subJSON(ctx, subHTTP, in.Base, "GET", "/api/v1/auth/me", auth.Access, nil, &profile, ""); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !strings.EqualFold(profile.Email, in.Email) {
		http.Error(w, "登录账号与邮箱不一致", 400)
		return
	}
	in.Email = strings.ToLower(profile.Email)
	if auth.ExpiresIn > 0 {
		auth.ExpiresAt = time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second).Unix()
	}
	auth.Temp = ""
	auth.Requires2FA = false
	raw, _ := json.Marshal(auth)
	encrypted, err := s.control.encrypt(string(raw))
	if err != nil {
		http.Error(w, "凭据保存失败", 503)
		return
	}
	var id string
	err = s.control.db.QueryRowContext(ctx, `INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth,state,job_kind) VALUES($1,$2,$3,$4,$5,$6,'queued','sync') ON CONFLICT(owner,base,email) DO UPDATE SET name=excluded.name,encrypted_auth=excluded.encrypted_auth,state='queued',job_kind='sync',job_id='',message='',lease_until=NULL WHERE console_sub_accounts.state NOT IN ('queued','running') RETURNING id`, "sub_"+randomID()[:16], owner, in.Name, in.Base, in.Email, encrypted).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "该账号已有任务进行中，请等待完成", 409)
		return
	}
	if err != nil {
		http.Error(w, "账号保存失败", 503)
		return
	}
	writeJSON(w, 202, map[string]any{"id": id, "queued": true})
}

func (s *Service) subSync(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	result, err := s.control.db.ExecContext(r.Context(), `UPDATE console_sub_accounts SET state='queued',job_kind='sync',job_id='',lease_until=NULL,message='' WHERE id=$1 AND owner=$2 AND state NOT IN ('queued','running')`, r.PathValue("id"), owner)
	if err != nil {
		http.Error(w, "无法同步账号", 503)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		http.Error(w, "账号不存在或已有任务进行中", 409)
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}
func (s *Service) subDelete(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	result, err := s.control.db.ExecContext(r.Context(), `DELETE FROM console_sub_accounts WHERE id=$1 AND owner=$2 AND state NOT IN ('running','queued')`, r.PathValue("id"), owner)
	if err != nil {
		http.Error(w, "删除失败", 503)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		http.Error(w, "请先停止任务，或账号已不存在", 409)
		return
	}
	// Do not modify unrelated upstream keys. The UI explicitly describes local removal.
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Service) subStop(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	tx, err := s.control.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "停止失败", 503)
		return
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(r.Context(), `UPDATE console_sub_accounts SET state='stopped',job_kind='',job_id='',lease_until=NULL,message='检测已停止' WHERE id=$1 AND owner=$2 RETURNING id`, r.PathValue("id"), owner).Scan(&id)
	if err != nil {
		http.Error(w, "账号不存在", 404)
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE console_sub_targets SET state='interrupted',message='检测已停止，已有结果保留' WHERE account_id=$1 AND state IN ('queued','running')`, id)
	if err != nil || tx.Commit() != nil {
		http.Error(w, "停止失败", 503)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type subSelection struct {
	AccountID string `json:"account_id"`
	GroupID   int64  `json:"group_id"`
}

func (s *Service) subCheck(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	var in struct {
		Targets []subSelection `json:"targets"`
	}
	if !decodeControl(w, r, &in) {
		return
	}
	if len(in.Targets) == 0 || len(in.Targets) > 500 {
		http.Error(w, "每次请选择 1–500 个分组", 400)
		return
	}
	tx, err := s.control.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "任务创建失败", 503)
		return
	}
	defer tx.Rollback()
	// Consistent account lock order also prevents overlapping batches from deadlocking.
	sort.Slice(in.Targets, func(i, j int) bool { return in.Targets[i].AccountID < in.Targets[j].AccountID })
	// One transaction: an invalid/busy/foreign target cannot partially queue a batch.
	accounts := map[string]bool{}
	for _, target := range in.Targets {
		if !accounts[target.AccountID] {
			var id string
			err = tx.QueryRowContext(r.Context(), `UPDATE console_sub_accounts SET state='queued',job_kind='check',job_id='',message='',lease_until=NULL WHERE id=$1 AND owner=$2 AND state NOT IN ('queued','running') RETURNING id`, target.AccountID, owner).Scan(&id)
			if err != nil {
				http.Error(w, "所选账号不存在或正在检测，请刷新后重试", 409)
				return
			}
			accounts[target.AccountID] = true
		}
		result, e := tx.ExecContext(r.Context(), `UPDATE console_sub_targets SET state='queued',message='' WHERE account_id=$1 AND group_id=$2 AND active AND encrypted_key<>''`, target.AccountID, target.GroupID)
		if e != nil {
			http.Error(w, "任务创建失败", 503)
			return
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			http.Error(w, "分组不可用或尚未创建 key，请先同步", 400)
			return
		}
	}
	if tx.Commit() != nil {
		http.Error(w, "任务创建失败", 503)
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}

// Durable queue with leases: page reloads do not cancel work. Interrupted paid
// requests are never automatically replayed after a process crash.
func (s *Service) subWorkerLoop(ctx context.Context) {
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				if !s.subWorkOne(ctx) {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Second):
					}
				}
			}
		}()
	}
	wg.Wait()
}
func (s *Service) subWorkOne(parent context.Context) bool {
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	defer cancel()
	// Expiry means the previous worker cannot report safely. Preserve old results.
	_, err := s.control.db.ExecContext(ctx, `WITH expired AS (UPDATE console_sub_accounts SET state='interrupted',message='服务重启或任务中断，请手动重新检测',job_kind='',job_id='',lease_until=NULL WHERE state='running' AND lease_until<now() RETURNING id) UPDATE console_sub_targets SET state='interrupted',message='上次检测中断' WHERE account_id IN (SELECT id FROM expired) AND state IN ('running','queued')`)
	if err != nil {
		return false
	}
	var id, base, kind, encrypted string
	job := randomID()
	err = s.control.db.QueryRowContext(ctx, `UPDATE console_sub_accounts SET state='running',job_id=$1,lease_until=now()+interval '30 seconds' WHERE id=(SELECT id FROM console_sub_accounts WHERE state='queued' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,base,job_kind,encrypted_auth`, job).Scan(&id, &base, &kind, &encrypted)
	if err != nil {
		return false
	}
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				res, e := s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET lease_until=now()+interval '30 seconds' WHERE id=$1 AND job_id=$2 AND state='running'`, id, job)
				if e != nil {
					cancel()
					return
				}
				n, _ := res.RowsAffected()
				if n == 0 {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-heartbeatDone }()
	if kind == "sync" {
		err = s.subSynchronize(ctx, id, base, job, encrypted)
	}
	if err == nil && ctx.Err() == nil {
		err = s.subTestTargets(ctx, id, base, job)
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	state, message := "idle", ""
	if err != nil {
		state, message = "error", err.Error()
	}
	if ctx.Err() != nil {
		state, message = "interrupted", "任务中断，请手动重试；未自动重发检测请求"
	}
	// Fence all late completions against stop/re-login/new worker operations.
	_, _ = s.control.db.ExecContext(finishCtx, `WITH finished AS (UPDATE console_sub_accounts SET state=$3,message=CASE WHEN $3='idle' THEN message ELSE $4 END,job_kind='',job_id='',lease_until=NULL WHERE id=$1 AND job_id=$2 RETURNING id) UPDATE console_sub_targets SET state='interrupted',message='任务未完成，请重试' WHERE account_id IN (SELECT id FROM finished) AND state IN ('queued','running')`, id, job, state, message)
	return true
}

func (s *Service) subSynchronize(ctx context.Context, id, base, job, encrypted string) error {
	plain, err := s.control.decrypt(encrypted)
	if err != nil {
		return errors.New("账号凭据无法解密，请重新登录")
	}
	var auth subAuth
	if json.Unmarshal([]byte(plain), &auth) != nil {
		return errors.New("账号凭据无效，请重新登录")
	}
	call := func(method, path string, body, out any, idem string) error {
		return subJSON(ctx, subHTTP, base, method, path, auth.Access, body, out, idem)
	}
	var groups []subRemoteGroup
	err = call("GET", "/api/v1/groups/available", nil, &groups, "")
	var remoteErr *subRemoteError
	if errors.As(err, &remoteErr) && remoteErr.Status == 401 && auth.Refresh != "" {
		var next subAuth
		if err = subJSON(ctx, subHTTP, base, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": auth.Refresh}, &next, ""); err != nil {
			return err
		}
		if next.Access == "" || next.Refresh == "" {
			return errors.New("刷新凭据失败，请重新登录")
		}
		next.ExpiresAt = time.Now().Add(time.Duration(next.ExpiresIn) * time.Second).Unix()
		auth = next
		raw, _ := json.Marshal(auth)
		enc, e := s.control.encrypt(string(raw))
		if e != nil {
			return errors.New("凭据保存失败")
		}
		result, e := s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET encrypted_auth=$3 WHERE id=$1 AND job_id=$2`, id, job, enc)
		if e != nil {
			return errors.New("凭据保存失败")
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return context.Canceled
		}
		err = call("GET", "/api/v1/groups/available", nil, &groups, "")
	}
	if err != nil {
		return err
	}
	if len(groups) > 500 {
		return errors.New("分组数量超过单账号 500 个的检测上限")
	}
	var userRates map[string]float64
	ratesKnown := call("GET", "/api/v1/groups/rates", nil, &userRates, "") == nil
	// Optional enrichment: many sites intentionally turn this endpoint off.
	var channels []struct {
		Name      string `json:"name"`
		Platforms []struct {
			Groups []struct {
				ID int64 `json:"id"`
			} `json:"groups"`
		} `json:"platforms"`
	}
	_ = call("GET", "/api/v1/channels/available", nil, &channels, "")
	labels := map[int64]string{}
	for _, c := range channels {
		for _, p := range c.Platforms {
			for _, g := range p.Groups {
				labels[g.ID] = c.Name
			}
		}
	}
	// Only keys with our exact deterministic names are adopted. Existing user keys
	// are never changed; pagination and idempotency make interrupted creation recoverable.
	keys := map[string]subRemoteKey{}
	for page := 1; page <= 100; page++ {
		var listing struct {
			Items []subRemoteKey `json:"items"`
			Total int            `json:"total"`
			Pages int            `json:"pages"`
		}
		path := "/api/v1/keys?search=" + url.QueryEscape("uni-console-check-"+id+"-") + "&page_size=100&page=" + strconv.Itoa(page)
		if e := call("GET", path, nil, &listing, ""); e != nil {
			return e
		}
		for _, key := range listing.Items {
			keys[key.Name] = key
		}
		if len(listing.Items) == 0 || len(listing.Items) < 100 || (listing.Pages > 0 && page >= listing.Pages) {
			break
		}
		if page == 100 {
			return errors.New("测试 key 列表过大，未完成同步")
		}
	}
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("分组保存失败")
	}
	defer tx.Rollback()
	var activeID string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM console_sub_accounts WHERE id=$1 AND job_id=$2 FOR UPDATE`, id, job).Scan(&activeID); err != nil {
		return context.Canceled
	}
	if _, err = tx.ExecContext(ctx, `UPDATE console_sub_targets SET active=false WHERE account_id=$1`, id); err != nil {
		return errors.New("分组保存失败")
	}
	for _, g := range groups {
		if g.ID <= 0 || len(g.Name) > 512 {
			return errors.New("站点返回了无效分组")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO console_sub_targets(account_id,group_id,name,platform,channel,rate,state) VALUES($1,$2,$3,$4,$5,$6,'queued') ON CONFLICT(account_id,group_id) DO UPDATE SET name=excluded.name,platform=excluded.platform,channel=excluded.channel,rate=excluded.rate,active=true,state='queued',message=''`, id, g.ID, g.Name, g.Platform, labels[g.ID], g.Rate)
		if err != nil {
			return errors.New("分组保存失败")
		}
	}
	if err = tx.Commit(); err != nil {
		return errors.New("分组保存失败")
	}
	failed := 0
	for _, g := range groups {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := subKeyName(id, g.ID)
		key, found := keys[name]
		var e error
		if !found {
			e = call("POST", "/api/v1/keys", map[string]any{"name": name, "group_id": g.ID, "quota": 1}, &key, "subcheck-"+id+"-"+strconv.FormatInt(g.ID, 10))
		}
		if e == nil && (key.GroupID != g.ID || key.Key == "" || key.ID <= 0) {
			e = errors.New("站点返回的 key 与分组不匹配")
		}
		if e == nil && (key.Status != "active" || (key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()))) {
			e = errors.New("专用测试 key 已停用或过期，请在上游恢复后同步")
		}
		if e != nil {
			failed++
			_, _ = s.control.db.ExecContext(ctx, `UPDATE console_sub_targets SET state='error',message=$3 WHERE account_id=$1 AND group_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$4)`, id, g.ID, e.Error(), job)
			continue
		}
		fallback := subBilling{Source: "unavailable"}
		if ratesKnown && !g.Peak {
			rate := g.Rate
			if personal, ok := userRates[strconv.FormatInt(g.ID, 10)]; ok {
				rate = personal
			}
			if rate >= 0 {
				fallback = subBilling{Rate: &rate, Source: "panel", CheckedAt: time.Now().Unix()}
			}
		}
		billing := subKeyBilling(ctx, subHTTP, base, key.Key, fallback)
		billingRaw, _ := json.Marshal(billing)
		enc, e := s.control.encrypt(key.Key)
		if e != nil {
			return errors.New("测试 key 加密失败")
		}
		_, e = s.control.db.ExecContext(ctx, `UPDATE console_sub_targets SET remote_key_id=$3,encrypted_key=$4,billing=$6 WHERE account_id=$1 AND group_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$5)`, id, g.ID, key.ID, enc, job, string(billingRaw))
		if e != nil {
			return errors.New("测试 key 保存失败")
		}
	}
	message := ""
	if failed > 0 {
		message = fmt.Sprintf("%d 个分组创建 key 失败，详情见表格", failed)
	}
	_, err = s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET synced_at=$3,message=$4 WHERE id=$1 AND job_id=$2`, id, job, time.Now().Unix(), message)
	if err != nil {
		return errors.New("同步结果保存失败")
	}
	return nil
}

func (s *Service) subTestTargets(ctx context.Context, id, base, job string) error {
	rows, err := s.control.db.QueryContext(ctx, `SELECT group_id,encrypted_key FROM console_sub_targets WHERE account_id=$1 AND active AND state='queued' AND encrypted_key<>'' ORDER BY group_id`, id)
	if err != nil {
		return errors.New("测试 key 读取失败")
	}
	type target struct {
		id  int64
		key string
	}
	targets := []target{}
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.id, &t.key); err != nil {
			break
		}
		targets = append(targets, t)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return errors.New("测试 key 读取失败")
	}
	jobs := make(chan target)
	errs := make(chan error, len(targets))
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				if ctx.Err() != nil {
					return
				}
				res, e := s.control.db.ExecContext(ctx, `UPDATE console_sub_targets SET state='running',message='' WHERE account_id=$1 AND group_id=$2 AND state='queued' AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$3)`, id, t.id, job)
				if e != nil {
					errs <- errors.New("检测进度保存失败")
					continue
				}
				n, _ := res.RowsAffected()
				if n != 1 {
					continue
				}
				key, e := s.control.decrypt(t.key)
				if e != nil {
					errs <- errors.New("测试 key 无法解密")
					continue
				}
				probeCtx, cancel := context.WithTimeout(ctx, 125*time.Second)
				result := subRunProbes(probeCtx, subHTTP, base, key)
				cancel()
				// Do not let an upstream echo the credential into the browser.
				result.Availability.Text = strings.ReplaceAll(result.Availability.Text, key, "[redacted]")
				result.Quality.Text = strings.ReplaceAll(result.Quality.Text, key, "[redacted]")
				if ctx.Err() != nil {
					return
				}
				raw, _ := json.Marshal(result)
				_, e = s.control.db.ExecContext(ctx, `UPDATE console_sub_targets SET state='done',result=$3,message='' WHERE account_id=$1 AND group_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$4)`, id, t.id, string(raw), job)
				if e != nil {
					errs <- errors.New("检测结果保存失败")
				}
			}
		}()
	}
send:
	for _, t := range targets {
		select {
		case jobs <- t:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			return e
		}
	}
	return ctx.Err()
}

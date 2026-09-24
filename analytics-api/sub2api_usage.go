package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Values are receipts from the site's account API, never balance deltas or
// estimates from the current catalog. Token unit prices are USD / million.
type subUsage struct {
	Status           string   `json:"status"`
	Message          string   `json:"message,omitempty"`
	LogID            int64    `json:"log_id,omitempty"`
	RequestID        string   `json:"request_id,omitempty"`
	CheckedAt        int64    `json:"checked_at,omitempty"`
	CreatedAt        string   `json:"created_at,omitempty"`
	ActualCost       *float64 `json:"actual_cost"`
	TotalCost        *float64 `json:"total_cost"`
	Rate             *float64 `json:"rate_multiplier"`
	InputTokens      *int64   `json:"input_tokens"`
	OutputTokens     *int64   `json:"output_tokens"`
	CacheReadTokens  *int64   `json:"cache_read_tokens"`
	CacheWriteTokens *int64   `json:"cache_creation_tokens"`
	InputPrice       *float64 `json:"input_price"`
	OutputPrice      *float64 `json:"output_price"`
	CacheReadPrice   *float64 `json:"cache_read_price"`
	CacheWritePrice  *float64 `json:"cache_write_price"`
	PaidInputPrice   *float64 `json:"paid_input_price"`
	PaidOutputPrice  *float64 `json:"paid_output_price"`
	DurationMS       *int64   `json:"duration_ms"`
	FirstTokenMS     *int64   `json:"first_token_ms"`
}

type subUsageLog struct {
	ID                int64    `json:"id"`
	KeyID             int64    `json:"api_key_id"`
	GroupID           *int64   `json:"group_id"`
	Model             string   `json:"model"`
	RequestID         string   `json:"request_id"`
	CreatedAt         string   `json:"created_at"`
	ActualCost        *float64 `json:"actual_cost"`
	TotalCost         *float64 `json:"total_cost"`
	Rate              *float64 `json:"rate_multiplier"`
	InputTokens       *int64   `json:"input_tokens"`
	OutputTokens      *int64   `json:"output_tokens"`
	CacheReadTokens   *int64   `json:"cache_read_tokens"`
	CacheWriteTokens  *int64   `json:"cache_creation_tokens"`
	InputCost         *float64 `json:"input_cost"`
	OutputCost        *float64 `json:"output_cost"`
	CacheReadCost     *float64 `json:"cache_read_cost"`
	CacheWriteCost    *float64 `json:"cache_creation_cost"`
	ImageInputTokens  int64    `json:"image_input_tokens"`
	ImageOutputTokens int64    `json:"image_output_tokens"`
	BillingMode       string   `json:"billing_mode"`
	DurationMS        *int64   `json:"duration_ms"`
	FirstTokenMS      *int64   `json:"first_token_ms"`
}

type subUsageTask struct {
	id, model           string
	group, key, started int64
	ids                 []string
	attempts            int
}

func subSafeRequestID(value, secret string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || (secret != "" && strings.Contains(value, secret)) {
		return ""
	}
	for _, r := range value {
		if r < 33 || r > 126 {
			return ""
		}
	}
	return value
}

func (p *subProbe) addRequestIDs(values ...string) {
	for _, value := range values {
		if len(p.RequestIDs) >= 16 {
			return
		}
		found := false
		for _, existing := range p.RequestIDs {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			p.RequestIDs = append(p.RequestIDs, value)
		}
	}
}
func subValidCount(v *int64) *int64 {
	if v == nil || *v < 0 {
		return nil
	}
	return v
}

func subValidCost(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 {
		return nil
	}
	return v
}
func subUnitPrice(cost *float64, tokens *int64) *float64 {
	if subValidCost(cost) == nil || tokens == nil || *tokens <= 0 {
		return nil
	}
	v := *cost * 1e6 / float64(*tokens)
	return subValidCost(&v)
}
func subPaidUnitPrice(price, rate *float64) *float64 {
	if price == nil || subValidCost(rate) == nil {
		return nil
	}
	v := *price * *rate
	return subValidCost(&v)
}

func subUsageReceipt(log subUsageLog) *subUsage {
	out := &subUsage{Status: "matched", LogID: log.ID, RequestID: log.RequestID, CheckedAt: time.Now().Unix(),
		ActualCost: subValidCost(log.ActualCost), TotalCost: subValidCost(log.TotalCost), Rate: subValidCost(log.Rate),
		InputTokens: subValidCount(log.InputTokens), OutputTokens: subValidCount(log.OutputTokens), CacheReadTokens: subValidCount(log.CacheReadTokens), CacheWriteTokens: subValidCount(log.CacheWriteTokens),
		DurationMS: subValidCount(log.DurationMS), FirstTokenMS: subValidCount(log.FirstTokenMS)}
	if at, err := time.Parse(time.RFC3339Nano, log.CreatedAt); err == nil {
		out.CreatedAt = at.UTC().Format(time.RFC3339Nano)
	}
	if log.BillingMode != "" && log.BillingMode != "token" {
		out.Message = "此请求不是按 token 计费，无法比较输入／输出单价"
		if log.BillingMode == "newapi_unconfirmed" {
			out.Message = "实际扣费已核对，计费规则或用量不足以可靠分解单价"
		}
		return out
	}
	if log.ImageInputTokens == 0 {
		out.InputPrice = subUnitPrice(log.InputCost, log.InputTokens)
	}
	if log.ImageOutputTokens == 0 {
		out.OutputPrice = subUnitPrice(log.OutputCost, log.OutputTokens)
	}
	out.CacheReadPrice = subUnitPrice(log.CacheReadCost, log.CacheReadTokens)
	out.CacheWritePrice = subUnitPrice(log.CacheWriteCost, log.CacheWriteTokens)
	// Some billing rules cap or waive the final amount. Do not pretend the
	// nominal multiplier is an exact per-component allocation in those cases.
	if out.Rate != nil && out.ActualCost != nil && out.TotalCost != nil && math.Abs(*out.ActualCost-*out.TotalCost**out.Rate) <= 1e-8 {
		out.PaidInputPrice = subPaidUnitPrice(out.InputPrice, out.Rate)
		out.PaidOutputPrice = subPaidUnitPrice(out.OutputPrice, out.Rate)
	}
	return out
}

func (s *Service) subQueueUsage(ctx context.Context, account string, group, key int64, result *subResult) error {
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range []*subProbe{&result.Availability, &result.Quality} {
		if p.ID == "" {
			continue
		}
		p.Usage = &subUsage{Status: "pending", Message: "等待站点账单入库"}
		ids, _ := json.Marshal(p.RequestIDs)
		_, err = tx.ExecContext(ctx, `INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, p.ID, account, group, key, result.Model, p.StartedAt, string(ids))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) subAttachUsage(ctx context.Context, owner string, accounts []subAccount) {
	probes := map[string][]*subProbe{}
	for i := range accounts {
		for j := range accounts[i].Targets {
			t := &accounts[i].Targets[j]
			if t.ToolUse != nil {
				for _, model := range t.ToolUse.Models {
					if model.Result == nil {
						continue
					}
					for k := range model.Result.Attempts {
						p := &model.Result.Attempts[k]
						if p.ID != "" {
							probes[p.ID] = append(probes[p.ID], p)
						}
					}
				}
				for k := range t.ToolUse.Attempts {
					p := &t.ToolUse.Attempts[k]
					if p.ID != "" {
						probes[p.ID] = append(probes[p.ID], p)
					}
				}
			}
			if t.Compaction != nil {
				for k := range t.Compaction.Attempts {
					p := &t.Compaction.Attempts[k]
					if p.ID != "" {
						probes[p.ID] = append(probes[p.ID], p)
					}
				}
			}
			results := []*subResult{t.Result}
			for k := range t.Models {
				results = append(results, t.Models[k].Result)
			}
			for _, r := range results {
				if r == nil {
					continue
				}
				for _, p := range []*subProbe{&r.Availability, &r.Quality} {
					if p.ID != "" {
						probes[p.ID] = append(probes[p.ID], p)
					}
				}
			}
		}
	}
	if len(probes) == 0 {
		return
	}
	ids := make([]string, 0, len(probes))
	for id := range probes {
		ids = append(ids, id)
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT u.id,u.status,u.result FROM console_sub_usage u JOIN console_sub_accounts a ON a.id=u.account_id WHERE a.owner=$1 AND u.id=ANY($2)`, owner, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		var raw []byte
		if rows.Scan(&id, &status, &raw) != nil {
			return
		}
		usage := &subUsage{Status: status}
		if len(raw) > 0 && json.Unmarshal(raw, usage) != nil {
			continue
		}
		for _, p := range probes[id] {
			p.Usage = usage
		}
	}
}

// Free account-API reads have a durable queue and separate leases. Slow log
// ingestion cannot occupy either of the two paid probe workers per account.
func (s *Service) subUsageLoop(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()
	for ctx.Err() == nil {
		for ctx.Err() == nil {
			id, base, token, err := s.subClaimUsage(ctx)
			if err != nil {
				break
			}
			wg.Add(1)
			go func() { defer wg.Done(); s.subReadUsage(ctx, id, base, token) }()
		}
		if !waitStartup(ctx, 2*time.Second) {
			return
		}
	}
}

func (s *Service) subClaimUsage(ctx context.Context) (id, base, token string, err error) {
	token = randomID()
	err = s.control.db.QueryRowContext(ctx, `UPDATE console_sub_accounts SET usage_lease_token=$1,usage_lease_until=now()+interval '30 seconds' WHERE id=(SELECT a.id FROM console_sub_accounts a WHERE (a.usage_lease_until IS NULL OR a.usage_lease_until<now()) AND EXISTS(SELECT 1 FROM console_sub_usage u WHERE u.account_id=a.id AND u.status='pending' AND u.next_attempt<=now()) ORDER BY a.created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,base`, token).Scan(&id, &base)
	return
}

func (s *Service) subUsageAuth(ctx context.Context, account, base, rejected string) (string, error) {
	var encrypted string
	read := func() (subAuth, error) {
		var auth subAuth
		err := s.control.db.QueryRowContext(ctx, `SELECT encrypted_auth FROM console_sub_accounts WHERE id=$1`, account).Scan(&encrypted)
		if err != nil {
			return auth, err
		}
		plain, err := s.control.decrypt(encrypted)
		if err == nil {
			err = json.Unmarshal([]byte(plain), &auth)
		}
		if err != nil || auth.Access == "" {
			return auth, errors.New("站点登录已失效，请重新登录")
		}
		return auth, nil
	}
	if rejected == "" {
		auth, err := read()
		return auth.Access, err
	}
	lockCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	unlock, err := s.subLockAuth(lockCtx, account)
	if err != nil {
		return "", errors.New("账号会话正在更新")
	}
	defer unlock()
	ctx = lockCtx
	auth, err := read()
	if err != nil {
		return "", err
	}
	if auth.Access != rejected {
		return auth.Access, nil
	}
	if auth.Refresh == "" {
		return "", errors.New("站点登录已失效，请重新登录")
	}
	var next subAuth
	if err = subJSON(ctx, subHTTP, base, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": auth.Refresh}, &next, ""); err != nil {
		return "", err
	}
	if next.Access == "" || next.Refresh == "" {
		return "", errors.New("刷新站点会话失败")
	}
	next.ExpiresAt = time.Now().Add(time.Duration(next.ExpiresIn) * time.Second).Unix()
	raw, _ := json.Marshal(next)
	enc, err := s.control.encrypt(string(raw))
	if err != nil {
		return "", err
	}
	saved, err := s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET encrypted_auth=$3 WHERE id=$1 AND encrypted_auth=$2`, account, encrypted, enc)
	if err != nil {
		return "", err
	}
	if n, _ := saved.RowsAffected(); n != 1 {
		return "", errors.New("站点会话已更新")
	}
	return next.Access, nil
}

func subMatchUsage(task subUsageTask, logs []subUsageLog) *subUsage {
	ids := make(map[string]bool, len(task.ids))
	for _, id := range task.ids {
		ids[id] = true
	}
	var match *subUsageLog
	for i := range logs {
		l := &logs[i]
		if l.ID <= 0 || l.KeyID != task.key || (l.GroupID != nil && *l.GroupID != task.group) || !ids[l.RequestID] {
			continue
		}
		if match != nil && match.ID != l.ID {
			return &subUsage{Status: "ambiguous", Message: "请求标识对应多条账单，无法确认扣费"}
		}
		match = l
	}
	if match == nil {
		return nil
	}
	return subUsageReceipt(*match)
}

func (s *Service) subReadUsage(parent context.Context, account, base, lease string) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, _ = s.control.db.ExecContext(cleanup, `UPDATE console_sub_accounts SET usage_lease_token='',usage_lease_until=NULL WHERE id=$1 AND usage_lease_token=$2`, account, lease)
	}()
	rows, err := s.control.db.QueryContext(ctx, `SELECT id,group_id,key_id,model,started_at,request_ids,attempts FROM console_sub_usage WHERE account_id=$1 AND status='pending' AND next_attempt<=now() AND key_id=(SELECT key_id FROM console_sub_usage WHERE account_id=$1 AND status='pending' AND next_attempt<=now() ORDER BY next_attempt LIMIT 1) ORDER BY started_at DESC LIMIT 32`, account)
	if err != nil {
		return
	}
	tasks := []subUsageTask{}
	for rows.Next() {
		var t subUsageTask
		var raw []byte
		if err = rows.Scan(&t.id, &t.group, &t.key, &t.model, &t.started, &raw, &t.attempts); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &t.ids); err != nil {
			break
		}
		tasks = append(tasks, t)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil || readErr != nil || len(tasks) == 0 {
		return
	}
	var logs []subUsageLog
	start, end := tasks[0].started, tasks[0].started
	for _, t := range tasks {
		start = min(start, t.started)
		end = max(end, t.started)
	}
	for page := 1; page <= 5 && err == nil; page++ {
		q := url.Values{"api_key_id": {strconv.FormatInt(tasks[0].key, 10)}, "page": {strconv.Itoa(page)}, "page_size": {"100"}, "sort_by": {"created_at"}, "sort_order": {"desc"}, "timezone": {"UTC"}, "start_date": {time.Unix(start, 0).UTC().Add(-24 * time.Hour).Format("2006-01-02")}, "end_date": {time.Unix(end, 0).UTC().Add(24 * time.Hour).Format("2006-01-02")}}
		var listing struct {
			Items []subUsageLog `json:"items"`
			Pages int           `json:"pages"`
		}
		path := "/api/v1/usage?" + q.Encode()
		err = s.subUsageGET(ctx, account, base, path, &listing)
		if err != nil {
			break
		}
		logs = append(logs, listing.Items...)
		if len(listing.Items) < 100 || (listing.Pages > 0 && page >= listing.Pages) {
			break
		}
	}
	// Shutdown leaves the durable queue pending for the next replica.
	if parent.Err() != nil {
		return
	}
	saveCtx, stop := context.WithTimeout(parent, 5*time.Second)
	defer stop()
	for _, t := range tasks {
		usage := subMatchUsage(t, logs)
		attempts := t.attempts + 1
		if usage == nil {
			usage = &subUsage{Status: "pending", Message: "等待站点账单入库", CheckedAt: time.Now().Unix()}
			if err != nil {
				usage.Message = "站点账单暂不可用，稍后自动重试"
			}
			if attempts >= 6 {
				usage.Status, usage.Message = "missing", "未能取得对应账单，无法确认扣费和单价"
			}
			var remote *subRemoteError
			if errors.As(err, &remote) && (remote.Status == 403 || remote.Status == 404) {
				usage.Status, usage.Message = "unsupported", "站点未开放此账号的用量日志查询"
			}
		}
		raw, _ := json.Marshal(usage)
		delay := min(120, 5*(1<<min(attempts-1, 5)))
		_, _ = s.control.db.ExecContext(saveCtx, `UPDATE console_sub_usage SET status=$3,result=$4,attempts=$5,next_attempt=now()+$6*interval '1 second' WHERE id=$1 AND account_id=$2 AND status='pending' AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$2 AND usage_lease_token=$7)`, t.id, account, usage.Status, string(raw), attempts, delay, lease)
	}
}

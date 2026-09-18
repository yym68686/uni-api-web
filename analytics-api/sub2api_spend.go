package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type subSpend struct {
	Status     string   `json:"status"`
	Amount     *float64 `json:"actual_cost_usd"`
	Requests   *int64   `json:"requests"`
	From       int64    `json:"from"`
	To         int64    `json:"to"`
	CheckedAt  int64    `json:"checked_at"`
	KeyID      int64    `json:"key_id"`
	Scope      string   `json:"scope"`
	Message    string   `json:"message,omitempty"`
	Refreshing bool     `json:"refreshing"`
}

// The dedicated business key is authoritative. Never use the quota-limited
// probe key, a wallet balance delta, or the caller's uni-api key for this total.
func (s *Service) subChannelSpend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	owner, _ := s.controlUser(r)
	account, group := r.PathValue("id"), int64Param(r.PathValue("group"))
	var key int64
	var created time.Time
	err := s.control.db.QueryRowContext(ctx, `SELECT t.routing_key_id,a.created_at FROM console_sub_accounts a JOIN console_sub_targets t ON t.account_id=a.id WHERE a.id=$1 AND a.owner=$2 AND t.group_id=$3`, account, owner, group).Scan(&key, &created)
	if err != nil {
		http.Error(w, "渠道不存在", 404)
		return
	}
	to := time.Now().UTC().Truncate(time.Second)
	if raw := r.URL.Query().Get("to"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 || value > to.Unix() {
			http.Error(w, "时间范围无效", 400)
			return
		}
		to = time.Unix(value, 0).UTC()
	}
	zone := time.UTC
	if s.engine != nil {
		zone = s.engine.Location
	}
	from, err := rangeStart(r.URL.Query().Get("range"), to, zone)
	if err != nil {
		http.Error(w, "时间范围无效", 400)
		return
	}
	// Match the minute boundaries of the observation analytics API.
	from = from.Truncate(time.Minute)
	out := subSpend{Status: "pending", From: from.Unix(), To: to.Unix(), KeyID: key, Scope: "sub2api_business_key"}
	if key <= 0 {
		out.Status, out.Message = "unsupported", "此渠道尚无可查询的 sub2api 业务 Key"
		writeJSON(w, 200, out)
		return
	}
	// Route keys are created with this console account's unique ID. They cannot
	// predate the account; this avoids scanning decades of empty days for 'all'.
	wantedFrom := max(from.UnixMilli(), created.UnixMilli())
	wantedTo := to.UnixMilli()
	if wantedFrom >= wantedTo {
		zero, count := float64(0), int64(0)
		out.Status, out.Amount, out.Requests = "complete", &zero, &count
		writeJSON(w, 200, out)
		return
	}
	_, err = s.control.db.ExecContext(ctx, `INSERT INTO console_sub_spend_cache(account_id,key_id,group_id,wanted_from,wanted_to) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(account_id,key_id) DO UPDATE SET wanted_from=least(console_sub_spend_cache.wanted_from,excluded.wanted_from),wanted_to=greatest(console_sub_spend_cache.wanted_to,excluded.wanted_to),
 requested=console_sub_spend_cache.requested OR console_sub_spend_cache.covered_from IS NULL OR console_sub_spend_cache.covered_from>excluded.wanted_from OR console_sub_spend_cache.covered_to<excluded.wanted_to OR console_sub_spend_cache.checked_at<$6`, account, key, group, wantedFrom, wantedTo, time.Now().Add(-time.Minute).UnixMilli())
	if err != nil {
		http.Error(w, "账单查询暂不可用", 503)
		return
	}
	var coveredFrom, coveredTo sql.NullInt64
	var checked int64
	var message string
	var requested bool
	err = s.control.db.QueryRowContext(ctx, `SELECT covered_from,covered_to,checked_at,error,requested FROM console_sub_spend_cache WHERE account_id=$1 AND key_id=$2`, account, key).Scan(&coveredFrom, &coveredTo, &checked, &message, &requested)
	if err != nil {
		http.Error(w, "账单状态暂不可用", 503)
		return
	}
	out.CheckedAt = checked / 1000
	if !coveredFrom.Valid || coveredFrom.Int64 > wantedFrom || !coveredTo.Valid || coveredTo.Int64 < wantedTo {
		out.Message = "正在同步所选时间范围的站点账单"
		if message != "" {
			out.Status, out.Message = "error", message
		}
		writeJSON(w, 200, out)
		return
	}
	var amount float64
	var count int64
	err = s.control.db.QueryRowContext(ctx, `SELECT coalesce(sum(actual_cost),0)::double precision,count(*) FROM console_sub_spend_logs WHERE account_id=$1 AND key_id=$2 AND at_ms>=$3 AND at_ms<$4`, account, key, from.UnixMilli(), wantedTo).Scan(&amount, &count)
	if err != nil {
		http.Error(w, "账单统计暂不可用", 503)
		return
	}
	out.Status, out.Amount, out.Requests, out.Refreshing = "complete", &amount, &count, requested
	if message != "" {
		out.Message = "更新暂未完成，显示上次完整账单统计"
	}
	writeJSON(w, 200, out)
}

type subSpendTask struct {
	account, base, lease   string
	key, group             int64
	wantedFrom, wantedTo   int64
	coveredFrom, coveredTo sql.NullInt64
	from, to               int64
	page, attempts         int
}
type subSpendLog struct {
	ID      int64        `json:"id"`
	KeyID   int64        `json:"api_key_id"`
	GroupID *int64       `json:"group_id"`
	At      string       `json:"created_at"`
	Cost    *json.Number `json:"actual_cost"`
}
type subSpendPage struct {
	Items    []subSpendLog `json:"items"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Pages    int           `json:"pages"`
}

func (s *Service) subSpendLoop(ctx context.Context) {
	var workers sync.WaitGroup
	defer workers.Wait()
	for ctx.Err() == nil {
		for ctx.Err() == nil {
			task, err := s.subClaimSpend(ctx)
			if err != nil {
				break
			}
			workers.Add(1)
			go func() { defer workers.Done(); s.subScanSpend(ctx, task) }()
		}
		if !waitStartup(ctx, 2*time.Second) {
			return
		}
	}
}

func (s *Service) subClaimSpend(ctx context.Context) (task subSpendTask, err error) {
	task.lease = randomID()
	// Share the account read lease with probe receipt lookups: one ledger reader
	// per site account, regardless of replicas, keys or simultaneous UI ranges.
	err = s.control.db.QueryRowContext(ctx, `UPDATE console_sub_accounts SET usage_lease_token=$1,usage_lease_until=now()+interval '30 seconds' WHERE id=(SELECT a.id FROM console_sub_accounts a WHERE (a.usage_lease_until IS NULL OR a.usage_lease_until<now()) AND EXISTS(SELECT 1 FROM console_sub_spend_cache c WHERE c.account_id=a.id AND c.requested AND c.next_attempt<=now()) ORDER BY a.created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,base`, task.lease).Scan(&task.account, &task.base)
	return
}

func (s *Service) subUsageGET(ctx context.Context, account, base, path string, out any) error {
	token, err := s.subUsageAuth(ctx, account, base, "")
	if err != nil {
		return err
	}
	err = subJSON(ctx, subHTTP, base, "GET", path, token, nil, out, "")
	var remote *subRemoteError
	if errors.As(err, &remote) && remote.Status == 401 {
		token, err = s.subUsageAuth(ctx, account, base, token)
		if err == nil {
			err = subJSON(ctx, subHTTP, base, "GET", path, token, nil, out, "")
		}
	}
	return err
}

func (s *Service) subScanSpend(parent context.Context, task subSpendTask) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, _ = s.control.db.ExecContext(cleanup, `UPDATE console_sub_accounts SET usage_lease_token='',usage_lease_until=NULL WHERE id=$1 AND usage_lease_token=$2`, task.account, task.lease)
	}()
	err := s.control.db.QueryRowContext(ctx, `SELECT key_id,group_id,wanted_from,wanted_to,covered_from,covered_to,scan_from,scan_to,page,attempts FROM console_sub_spend_cache WHERE account_id=$1 AND requested AND next_attempt<=now() ORDER BY next_attempt,key_id LIMIT 1`, task.account).Scan(&task.key, &task.group, &task.wantedFrom, &task.wantedTo, &task.coveredFrom, &task.coveredTo, &task.from, &task.to, &task.page, &task.attempts)
	if err != nil {
		return
	}
	if task.to == 0 {
		task.from, task.to, task.page = task.wantedFrom, task.wantedTo, 1
		if task.coveredFrom.Valid && task.coveredFrom.Int64 <= task.wantedFrom && task.coveredTo.Valid {
			// Re-read at least the preceding day to pick up delayed postings and
			// amended costs. ON CONFLICT replaces a receipt; it never adds it twice.
			task.from = max(task.wantedFrom, task.coveredTo.Int64-int64(24*time.Hour/time.Millisecond))
		}
		_, err = s.control.db.ExecContext(ctx, `UPDATE console_sub_spend_cache SET scan_from=$3,scan_to=$4,page=1 WHERE account_id=$1 AND key_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND usage_lease_token=$5)`, task.account, task.key, task.from, task.to, task.lease)
		if err != nil {
			return
		}
	}
	// Small durable batches bound memory, response time and shutdown latency.
	// Larger histories resume on the next turn instead of returning partial sums.
	for range 5 {
		q := url.Values{"api_key_id": {strconv.FormatInt(task.key, 10)}, "start_date": {time.UnixMilli(task.from).UTC().Format("2006-01-02")}, "end_date": {time.UnixMilli(task.to - 1).UTC().Format("2006-01-02")}, "timezone": {"UTC"}, "sort_by": {"created_at"}, "sort_order": {"asc"}, "page_size": {"100"}, "page": {strconv.Itoa(task.page)}}
		var page subSpendPage
		err = s.subUsageGET(ctx, task.account, task.base, "/api/v1/usage?"+q.Encode(), &page)
		if err != nil {
			break
		}
		if page.Items == nil || (page.Page != 0 && page.Page != task.page) {
			err = errors.New("invalid pagination")
			break
		}
		complete := len(page.Items) == 0 || (page.Pages > 0 && task.page >= page.Pages)
		if page.Pages == 0 {
			pageSize := 100
			if page.PageSize > 0 {
				pageSize = page.PageSize
			}
			complete = len(page.Items) < pageSize
		}
		err = s.subStoreSpendPage(ctx, task, page, complete)
		if err != nil || complete {
			break
		}
		task.page++
	}
	if err != nil && parent.Err() == nil {
		message := "站点账单暂不可用，稍后自动重试"
		var remote *subRemoteError
		if errors.As(err, &remote) && (remote.Status == 403 || remote.Status == 404) {
			message = "站点未开放业务 Key 的账单查询"
		}
		if err.Error() == "incomplete receipt" || err.Error() == "invalid pagination" {
			message = "站点账单缺少有效时间、金额或分页信息，无法确认消费"
		}
		saveCtx, stop := context.WithTimeout(parent, 3*time.Second)
		defer stop()
		delay := min(300, 5*(1<<min(task.attempts, 6)))
		_, _ = s.control.db.ExecContext(saveCtx, `UPDATE console_sub_spend_cache SET error=$3,attempts=attempts+1,next_attempt=now()+$4*interval '1 second' WHERE account_id=$1 AND key_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND usage_lease_token=$5)`, task.account, task.key, message, delay, task.lease)
	}
}

func (s *Service) subStoreSpendPage(ctx context.Context, task subSpendTask, page subSpendPage, complete bool) error {
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid bool
	if err = tx.QueryRowContext(ctx, `SELECT usage_lease_token=$2 AND usage_lease_until>now() FROM console_sub_accounts WHERE id=$1 FOR UPDATE`, task.account, task.lease).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return context.Canceled
	}
	for _, log := range page.Items {
		// Never accept an account-wide total when a site ignores the key filter.
		if log.KeyID != task.key {
			continue
		}
		if log.GroupID != nil && *log.GroupID != task.group {
			continue
		}
		at, parseErr := time.Parse(time.RFC3339Nano, log.At)
		if parseErr != nil || log.ID <= 0 || log.Cost == nil {
			return errors.New("incomplete receipt")
		}
		cost, parseErr := log.Cost.Float64()
		if parseErr != nil || math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
			return errors.New("incomplete receipt")
		}
		if at.UnixMilli() < task.from || at.UnixMilli() >= task.to {
			continue
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO console_sub_spend_logs(account_id,key_id,log_id,at_ms,actual_cost) VALUES($1,$2,$3,$4,$5::numeric) ON CONFLICT(account_id,key_id,log_id) DO UPDATE SET at_ms=excluded.at_ms,actual_cost=excluded.actual_cost`, task.account, task.key, log.ID, at.UnixMilli(), log.Cost.String())
		if err != nil {
			return err
		}
	}
	if complete {
		_, err = tx.ExecContext(ctx, `UPDATE console_sub_spend_cache SET covered_from=least(coalesce(covered_from,$3),$3),covered_to=greatest(coalesce(covered_to,$4),$4),scan_from=0,scan_to=0,page=1,checked_at=$5,error='',attempts=0,next_attempt=now(),requested=wanted_from<least(coalesce(covered_from,$3),$3) OR wanted_to>greatest(coalesce(covered_to,$4),$4) WHERE account_id=$1 AND key_id=$2`, task.account, task.key, task.from, task.to, time.Now().UnixMilli())
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE console_sub_spend_cache SET page=$3,error='',attempts=0,next_attempt=now()+interval '2 seconds' WHERE account_id=$1 AND key_id=$2`, task.account, task.key, task.page+1)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

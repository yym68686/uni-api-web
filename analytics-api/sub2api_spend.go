package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
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

// The dedicated business key is authoritative. Never use the dedicated
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
	s.subSpendForKey(w, r, account, key, created)
}

func (s *Service) subAccountKeySpend(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	account, key := r.PathValue("id"), int64Param(r.PathValue("key"))
	bindings, err := s.configuredBindings(r.Context(), owner)
	if err != nil {
		http.Error(w, "渠道关联暂不可用", 503)
		return
	}
	allowed := false
	for _, binding := range bindings {
		if binding.BindingStatus != "matched" {
			continue
		}
		for _, bound := range binding.BoundKeys {
			if bound.AccountID == account && bound.RemoteKeyID == key {
				allowed = true
			}
		}
	}
	if !allowed {
		http.Error(w, "密钥未关联到当前账号的配置渠道", 404)
		return
	}
	var created sql.NullTime
	err = s.control.db.QueryRowContext(r.Context(), `SELECT key_created_at FROM console_sub_key_index WHERE account_id=$1 AND remote_key_id=$2 LIMIT 1`, account, key).Scan(&created)
	if err != nil {
		http.Error(w, "密钥关联不存在", 404)
		return
	}
	// Existing keys can predate their console account. Never clamp their history
	// to the date the operator saved the login in this console.
	since := time.Unix(0, 0)
	if created.Valid {
		since = created.Time
	}
	s.subSpendForKey(w, r, account, key, since)
}

func (s *Service) subSpendForKey(w http.ResponseWriter, r *http.Request, account string, key int64, created time.Time) {
	ctx := r.Context()
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
	// Only clamp to a proven creation date: imported route keys use the console
	// account date; existing configured keys use their own upstream creation date.
	wantedFrom := max(from.UnixMilli(), created.UnixMilli())
	wantedTo := to.UnixMilli()
	if wantedFrom >= wantedTo {
		zero, count := float64(0), int64(0)
		out.Status, out.Amount, out.Requests = "complete", &zero, &count
		writeJSON(w, 200, out)
		return
	}
	err = s.queueAccountSpendWindow(ctx, account, wantedFrom, wantedTo)
	if err != nil {
		http.Error(w, "账单查询暂不可用", 503)
		return
	}
	var coveredFrom, coveredTo sql.NullInt64
	var checked int64
	var message string
	var requested bool
	err = s.control.db.QueryRowContext(ctx, `SELECT covered_from,covered_to,checked_at,error,requested FROM console_sub_account_spend_cache WHERE account_id=$1`, account).Scan(&coveredFrom, &coveredTo, &checked, &message, &requested)
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
	account, base, lease     string
	wantedFrom, wantedTo     int64
	coveredFrom, coveredTo   sql.NullInt64
	from, to                 int64
	page, attempts, pageSize int
}
type subSpendLog struct {
	RequestID string       `json:"request_id"`
	Model     string       `json:"model"`
	Stream    *bool        `json:"stream"`
	Endpoint  string       `json:"inbound_endpoint"`
	ID        int64        `json:"id"`
	KeyID     int64        `json:"api_key_id"`
	GroupID   *int64       `json:"group_id"`
	At        string       `json:"created_at"`
	Cost      *json.Number `json:"actual_cost"`
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
	err = s.control.db.QueryRowContext(ctx, `UPDATE console_sub_accounts SET usage_lease_token=$1,usage_lease_until=now()+interval '30 seconds' WHERE id=(SELECT a.id FROM console_sub_accounts a WHERE (a.usage_lease_until IS NULL OR a.usage_lease_until<now()) AND EXISTS(SELECT 1 FROM console_sub_account_spend_cache c WHERE c.account_id=a.id AND c.requested AND c.next_attempt<=now()) ORDER BY a.created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,base`, task.lease).Scan(&task.account, &task.base)
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
	// Each page has its own bounded deadline. Five slow but successful bodies
	// must not consume one shared 20-second deadline and fail the next page.
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, _ = s.control.db.ExecContext(cleanup, `UPDATE console_sub_accounts SET usage_lease_token='',usage_lease_until=NULL WHERE id=$1 AND usage_lease_token=$2`, task.account, task.lease)
	}()
	err := s.control.db.QueryRowContext(ctx, `SELECT wanted_from,wanted_to,covered_from,covered_to,scan_from,scan_to,page,attempts,page_size FROM console_sub_account_spend_cache WHERE account_id=$1 AND requested AND next_attempt<=now()`, task.account).Scan(&task.wantedFrom, &task.wantedTo, &task.coveredFrom, &task.coveredTo, &task.from, &task.to, &task.page, &task.attempts, &task.pageSize)
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
		_, err = s.control.db.ExecContext(ctx, `UPDATE console_sub_account_spend_cache SET scan_from=$2,scan_to=$3,page=1 WHERE account_id=$1 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND usage_lease_token=$4)`, task.account, task.from, task.to, task.lease)
		if err != nil {
			return
		}
	}
	// Small durable batches bound memory, response time and shutdown latency.
	// Larger histories resume on the next turn instead of returning partial sums.
	for range 5 {
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 21*time.Second {
			// The previous page already persisted its next cursor. Resume in
			// another batch instead of starting a request we cannot finish.
			return
		}
		q := url.Values{"start_date": {time.UnixMilli(task.from).UTC().Format("2006-01-02")}, "end_date": {time.UnixMilli(task.to - 1).UTC().Format("2006-01-02")}, "timezone": {"UTC"}, "sort_by": {"created_at"}, "sort_order": {"asc"}, "page_size": {strconv.Itoa(task.pageSize)}, "page": {strconv.Itoa(task.page)}}
		var page subSpendPage
		began := time.Now()
		err = s.subSpendPageGET(ctx, task, q, &page)
		if err != nil {
			var remote *subRemoteError
			if task.pageSize > 100 && (errors.Is(err, errSubResponseTooLarge) || (errors.As(err, &remote) && remote.Status == 400)) {
				// Restart this scan with smaller pages; never reuse an offset
				// from a different page size. Existing cached receipts dedup.
				_, _ = s.control.db.ExecContext(ctx, `UPDATE console_sub_account_spend_cache SET page=1,page_size=100,error='',next_attempt=now()+interval '2 seconds' WHERE account_id=$1 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND usage_lease_token=$2)`, task.account, task.lease)
				log.Printf("event=sub_account_spend_page_size_fallback account_id=%s page_size=100", task.account)
				return
			}
			break
		}
		if page.Items == nil || (page.Page != 0 && page.Page != task.page) || (page.Pages == 0 && page.PageSize <= 0 && len(page.Items) > 0) {
			err = errors.New("invalid pagination")
			break
		}
		complete := len(page.Items) == 0 || (page.Pages > 0 && task.page >= page.Pages)
		if page.Pages == 0 {
			pageSize := task.pageSize
			if page.PageSize > 0 {
				pageSize = page.PageSize
			}
			complete = len(page.Items) < pageSize
		}
		if page.PageSize > 0 && page.PageSize != task.pageSize {
			if task.page != 1 {
				err = errors.New("invalid pagination")
				break
			}
			task.pageSize = page.PageSize
		}
		err = s.subStoreSpendPage(ctx, task, page, complete)
		if err == nil {
			log.Printf("event=sub_account_spend_page account_id=%s page=%d page_size=%d rows=%d duration_ms=%d complete=%t", task.account, task.page, task.pageSize, len(page.Items), time.Since(began).Milliseconds(), complete)
			if task.page == 1 && page.Pages > 5 && !complete {
				// A long ascending scan must not put today's newest receipts
				// behind every old page. This independent first descending page
				// adds exact receipts only; it never advances scan coverage/cursor.
				// Keep the ascending traversal and its durable resume unchanged.
				s.subReadRecentSpend(ctx, task, q)
			}
		}
		if err != nil || complete {
			break
		}
		task.page++
	}
	if err != nil && parent.Err() == nil {
		message := "站点账单暂不可用，稍后自动重试"
		var remote *subRemoteError
		if errors.As(err, &remote) && (remote.Status == 403 || remote.Status == 404) {
			message = "站点未开放账号账单查询"
		}
		if err.Error() == "incomplete receipt" || err.Error() == "invalid pagination" {
			message = "站点账单缺少有效时间、金额或分页信息，无法确认消费"
		}
		saveCtx, stop := context.WithTimeout(parent, 3*time.Second)
		defer stop()
		delay := min(300, 5*(1<<min(task.attempts, 6)))
		_, _ = s.control.db.ExecContext(saveCtx, `UPDATE console_sub_account_spend_cache SET error=$2,attempts=attempts+1,next_attempt=now()+$3*interval '1 second' WHERE account_id=$1 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND usage_lease_token=$4)`, task.account, message, delay, task.lease)
		log.Printf("event=sub_account_spend_retry account_id=%s page=%d delay_seconds=%d cause=%s reason=%q", task.account, task.page, delay, subSpendErrorCode(err), message)
	}
}

// Renew only our unexpired lease before starting a bounded page. Never reclaim
// a replaced/expired lease or overlap the account's other billing readers.
func (s *Service) subSpendPageGET(parent context.Context, task subSpendTask, query url.Values, page *subSpendPage) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	result, err := s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET usage_lease_until=now()+interval '30 seconds' WHERE id=$1 AND usage_lease_token=$2 AND usage_lease_until>now()`, task.account, task.lease)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return context.Canceled
	}
	return s.subUsageGET(ctx, task.account, task.base, "/api/v1/usage?"+query.Encode(), page)
}

func subSpendErrorCode(err error) string {
	var remote *subRemoteError
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.As(err, &remote):
		return "http_" + strconv.Itoa(remote.Status)
	case errors.Is(err, errSubResponseTooLarge):
		return "response_too_large"
	default:
		return "request_or_receipt_error"
	}
}

func (s *Service) subStoreSpendPage(ctx context.Context, task subSpendTask, page subSpendPage, complete bool) error {
	return s.subStoreSpendReceipts(ctx, task, page, complete, true)
}

func (s *Service) subReadRecentSpend(parent context.Context, task subSpendTask, query url.Values) {
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) < 21*time.Second {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	query.Set("sort_order", "desc")
	query.Set("page", "1")
	query.Set("page_size", strconv.Itoa(task.pageSize))
	var recent subSpendPage
	began := time.Now()
	err := s.subSpendPageGET(ctx, task, query, &recent)
	if err == nil {
		if recent.Items == nil || (recent.Page != 0 && recent.Page != 1) {
			err = errors.New("invalid recent page")
		} else {
			err = s.subStoreSpendReceipts(ctx, task, recent, false, false)
		}
	}
	// Unsupported/failed acceleration never interrupts the full ledger scan.
	log.Printf("event=sub_account_spend_recent account_id=%s rows=%d duration_ms=%d success=%t cause=%s", task.account, len(recent.Items), time.Since(began).Milliseconds(), err == nil, subSpendErrorCode(err))
}

func (s *Service) subStoreSpendReceipts(ctx context.Context, task subSpendTask, page subSpendPage, complete, advance bool) error {
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
	// Preserve each record's key so one account download serves every channel.
	// This is the same immutable row identity used by the earlier per-key cache.
	unique := map[string]map[string]any{}
	for _, record := range page.Items {
		at, parseErr := time.Parse(time.RFC3339Nano, record.At)
		if parseErr != nil || record.ID <= 0 || record.KeyID <= 0 || record.Cost == nil {
			return errors.New("incomplete receipt")
		}
		cost, parseErr := record.Cost.Float64()
		if parseErr != nil || math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
			return errors.New("incomplete receipt")
		}
		if at.UnixMilli() < task.from || at.UnixMilli() >= task.to {
			continue
		}
		unique[strconv.FormatInt(record.KeyID, 10)+":"+strconv.FormatInt(record.ID, 10)] = map[string]any{
			"key_id": record.KeyID, "log_id": record.ID, "at_ms": at.UnixMilli(), "actual_cost": record.Cost.String(),
			"request_id": record.RequestID, "model": record.Model, "stream": record.Stream, "inbound_endpoint": record.Endpoint,
		}
	}
	entries := make([]map[string]any, 0, len(unique))
	for _, entry := range unique {
		entries = append(entries, entry)
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO console_sub_spend_logs(account_id,key_id,log_id,at_ms,actual_cost,request_id,model,stream,inbound_endpoint)
 SELECT $1,r.key_id,r.log_id,r.at_ms,r.actual_cost,r.request_id,r.model,r.stream,r.inbound_endpoint
 FROM jsonb_to_recordset($2::jsonb) AS r(key_id bigint,log_id bigint,at_ms bigint,actual_cost numeric,request_id text,model text,stream boolean,inbound_endpoint text)
 ON CONFLICT(account_id,key_id,log_id) DO UPDATE SET at_ms=excluded.at_ms,actual_cost=excluded.actual_cost,request_id=excluded.request_id,model=excluded.model,stream=excluded.stream,inbound_endpoint=excluded.inbound_endpoint`, task.account, string(raw))
	if err != nil {
		return err
	}
	if !advance {
		return tx.Commit()
	}
	if complete {
		_, err = tx.ExecContext(ctx, `UPDATE console_sub_account_spend_cache SET covered_from=least(coalesce(covered_from,$2),$2),covered_to=greatest(coalesce(covered_to,$3),$3),scan_from=0,scan_to=0,page=1,checked_at=$4,error='',attempts=0,next_attempt=now(),requested=wanted_from<least(coalesce(covered_from,$2),$2) OR wanted_to>greatest(coalesce(covered_to,$3),$3),page_size=$5 WHERE account_id=$1`, task.account, task.from, task.to, time.Now().UnixMilli(), task.pageSize)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE console_sub_account_spend_cache SET page=$2,page_size=$3,error='',attempts=0,next_attempt=now()+interval '2 seconds' WHERE account_id=$1`, task.account, task.page+1, task.pageSize)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) queueAccountSpendWindow(ctx context.Context, account string, from, to int64) error {
	return s.queueAccountSpendWindows(ctx, map[string][2]int64{account: {from, to}})
}

func (s *Service) queueAccountSpendWindows(ctx context.Context, windows map[string][2]int64) error {
	if len(windows) == 0 {
		return nil
	}
	entries := make([]map[string]any, 0, len(windows))
	for account, w := range windows {
		entries = append(entries, map[string]any{"account_id": account, "wanted_from": w[0], "wanted_to": w[1]})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	_, err = s.control.db.ExecContext(ctx, `INSERT INTO console_sub_account_spend_cache(account_id,wanted_from,wanted_to)
 SELECT account_id,wanted_from,wanted_to FROM jsonb_to_recordset($1::jsonb) AS w(account_id text,wanted_from bigint,wanted_to bigint) ORDER BY account_id
 ON CONFLICT(account_id) DO UPDATE SET wanted_from=least(console_sub_account_spend_cache.wanted_from,excluded.wanted_from),wanted_to=greatest(console_sub_account_spend_cache.wanted_to,excluded.wanted_to),
 requested=console_sub_account_spend_cache.requested OR console_sub_account_spend_cache.covered_from IS NULL OR console_sub_account_spend_cache.covered_from>excluded.wanted_from OR console_sub_account_spend_cache.covered_to<excluded.wanted_to OR console_sub_account_spend_cache.checked_at<$2`, string(raw), time.Now().Add(-time.Minute).UnixMilli())
	return err
}

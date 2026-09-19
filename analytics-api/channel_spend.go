package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxBillingFacts = 50000

// Old v3 checkpoints kept this tool endpoint in upstream_base. Normalize at
// query time as well as import so existing receipts become attributable without
// rewriting immutable facts or restarting/replaying gateway requests.
const billingSiteSQL = `regexp_replace(COALESCE(upstream_base,''), '/v1/alpha/search/?$', '')`

type attributedSpend struct {
	Source            string                   `json:"source_id"`
	Provider          string                   `json:"provider"`
	Model             string                   `json:"model"`
	UpstreamModel     string                   `json:"upstream_model"`
	Status            string                   `json:"status"`
	Amount            *float64                 `json:"actual_cost_usd"`
	MatchedAmount     float64                  `json:"matched_cost_usd"`
	Total             int                      `json:"total_attempts"`
	Matched           int                      `json:"matched_attempts"`
	ConfirmedUnbilled int                      `json:"confirmed_unbilled_attempts"`
	Missing           int                      `json:"missing_identifiers"`
	Ambiguous         int                      `json:"ambiguous_attempts"`
	From              int64                    `json:"from"`
	To                int64                    `json:"to"`
	Scope             string                   `json:"scope"`
	LegacyMissing     int                      `json:"missing_correlation_attempts"`
	HeaderMissing     int                      `json:"missing_response_identifiers"`
	Unbound           int                      `json:"unbound_attempts"`
	Pending           int                      `json:"pending_attempts"`
	AbsentReceipts    int                      `json:"absent_receipt_attempts"`
	AbsentStatuses    map[string]int           `json:"absent_receipt_statuses,omitempty"`
	CheckedAt         int64                    `json:"checked_at"`
	Refreshing        bool                     `json:"refreshing"`
	SyncError         bool                     `json:"sync_error"`
	MissingStatuses   map[string]int           `json:"missing_response_statuses,omitempty"`
	Message           string                   `json:"message"`
	UnresolvedSamples []unresolvedSpendAttempt `json:"unresolved_samples,omitempty"`
}
type unresolvedSpendAttempt struct {
	RequestID string `json:"request_id"`
	AttemptID string `json:"attempt_id"`
	Endpoint  string `json:"endpoint"`
	Status    int    `json:"status"`
	At        int64  `json:"at"`
	Reason    string `json:"reason"`
}

func (s *attributedSpend) unresolved(b billingFact, reason string) {
	if len(s.UnresolvedSamples) < 8 {
		s.UnresolvedSamples = append(s.UnresolvedSamples, unresolvedSpendAttempt{b.Request, b.Attempt, b.Endpoint, b.Status, b.At / 1000, reason})
	}
}

type receiptScope struct {
	Account, Site string
	Key, Group    int64
}
type billingFact struct {
	Event, Source, Instance, Request, Attempt, Provider, Model, Upstream, Key, Base, Hash string
	ErrorSHA256                                                                           string
	Endpoint                                                                              string
	At, Started                                                                           int64
	IDs                                                                                   []string
	Completions                                                                           int
	Status                                                                                int
}

func billingRowID(source, provider, model, upstream string) string {
	return mustJSON([]string{source, provider, model, upstream})
}
func receiptIdentity(account string, key int64, id string) string {
	return account + ":" + strconv.FormatInt(key, 10) + ":" + id
}
func receiptOwnerKey(base, hash, id string) string { return base + "\n" + hash + "\n" + id }

func (s *Service) channelSpend(w http.ResponseWriter, r *http.Request) {
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	if !s.analyticsReady() {
		s.initializing(w)
		return
	}
	allowed, err := s.analyticsSources(r)
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	q := r.URL.Query()
	to, err := strconv.ParseInt(q.Get("to"), 10, 64)
	if err != nil || to <= 0 || to > time.Now().Unix()+1 {
		http.Error(w, "时间范围无效", 400)
		return
	}
	from, err := strconv.ParseInt(q.Get("from"), 10, 64)
	if err != nil || from < 0 || from >= to {
		http.Error(w, "时间范围无效", 400)
		return
	}
	f := QueryFilter{SourceIDs: allowed, SourceID: q.Get("source_id"), KeyID: q.Get("key_id"), Model: q.Get("model"), Provider: q.Get("provider"), UpstreamModel: q.Get("upstream_model"), Endpoint: q.Get("endpoint"), Stream: q.Get("stream")}
	if parts := strings.SplitN(f.KeyID, "::", 2); len(parts) == 2 {
		if f.SourceID != "" && f.SourceID != parts[0] {
			http.Error(w, "API key 来源不一致", 400)
			return
		}
		f.SourceID, f.KeyID = parts[0], parts[1]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	rows, err := s.attributedChannelSpend(ctx, owner, f, from, to)
	if err != nil {
		log.Printf("event=channel_spend_query_failed error=%q", err)
		http.Error(w, "逐请求账单关联暂不可用，请稍后刷新", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows, "from": from, "to": to, "scope": "matched_requests"})
}
func billingWhere(f QueryFilter, from, to int64) (string, []any) {
	where := []string{"at_ms>=?", "at_ms<?"}
	args := []any{from * 1000, to * 1000}
	for _, entry := range [][2]string{{"provider", f.Provider}, {"model", f.Model}, {"upstream_model", f.UpstreamModel}, {"endpoint", f.Endpoint}, {"key_id", f.KeyID}} {
		if entry[1] != "" && entry[1] != "all" {
			where = append(where, entry[0]+"=?")
			args = append(args, entry[1])
		}
	}
	if f.Stream == "true" || f.Stream == "false" {
		where = append(where, "stream=?")
		args = append(args, f.Stream == "true")
	}
	where, args = sourceWhere(where, args, f)
	return strings.Join(where, " AND "), args
}

// Costs use the same completion timestamp as attempt metrics. A cancelled hedge
// without a completion keeps its observation timestamp. Reused caller IDs are
// deliberately ambiguous instead of choosing a completion arbitrarily.
const billingAlignedSQL = `WITH aligned_billing AS (
 SELECT b.* EXCLUDE(at_ms),coalesce(a.completed_at,b.at_ms) AS at_ms,coalesce(a.n,0) AS completions
 FROM facts b LEFT JOIN (
 SELECT source_id,instance_id,request_id,attempt_id,provider,key_id,model,upstream_model,endpoint,stream,max(at_ms) AS completed_at,count(*) AS n
 FROM facts WHERE kind='attempt' GROUP BY ALL
 ) a USING(source_id,instance_id,request_id,attempt_id,provider,key_id,model,upstream_model,endpoint,stream)
 WHERE b.kind='billing'
) `

const billingIdentityColumns = "source_id,instance_id,request_id,attempt_id,provider,key_id,model,upstream_model,endpoint,stream"

// Join only candidate identities before grouping completions. Do not restrict
// their time: responses can start outside the window or finish after it, and
// reused caller IDs must retain every completion for ambiguity checks.
func scopedBillingAlignment(f QueryFilter) (string, []any) {
	where, args := billingWhere(f, 0, 1)
	where = strings.TrimPrefix(strings.TrimPrefix(where, "at_ms>=? AND at_ms<?"), " AND ")
	if where == "" {
		where = "true"
	}
	return `WITH candidate_billing AS MATERIALIZED (SELECT * FROM facts WHERE kind='billing' AND ` + where + `),
 completion_keys AS (SELECT DISTINCT ` + billingIdentityColumns + ` FROM candidate_billing),
 completions AS (SELECT ` + "a." + strings.ReplaceAll(billingIdentityColumns, ",", ",a.") + `,max(a.at_ms) AS completed_at,count(*) AS n
 FROM facts a JOIN completion_keys k USING(` + billingIdentityColumns + `) WHERE a.kind='attempt' GROUP BY ALL),
 aligned_billing AS (SELECT b.* EXCLUDE(at_ms),coalesce(a.completed_at,b.at_ms) AS at_ms,coalesce(a.n,0) AS completions
 FROM candidate_billing b LEFT JOIN completions a USING(` + billingIdentityColumns + `)) `, args[2:]
}

func (s *Service) attributedChannelSpend(ctx context.Context, owner string, f QueryFilter, from, to int64) ([]attributedSpend, error) {
	where, args := billingWhere(f, from, to)
	alignment, scopeArgs := scopedBillingAlignment(f)
	alignedArgs := append(append([]any{}, scopeArgs...), args...)
	rows, err := s.engine.DB.QueryContext(ctx, alignment+`SELECT event_id,source_id,COALESCE(instance_id,''),COALESCE(request_id,''),COALESCE(attempt_id,''),provider,model,upstream_model,key_id,at_ms,COALESCE(started_ms,at_ms),`+billingSiteSQL+`,COALESCE(upstream_key_hash,''),CAST(COALESCE(to_json(billing_request_ids),'[]') AS VARCHAR),completions,COALESCE(status,0),COALESCE(upstream_error_sha256,''),COALESCE(endpoint,'') FROM aligned_billing WHERE kind='billing' AND `+where+` ORDER BY at_ms,event_id LIMIT 50001`, alignedArgs...)
	if err != nil {
		return nil, err
	}
	facts := []billingFact{}
	for rows.Next() {
		var b billingFact
		var ids []byte
		if err = rows.Scan(&b.Event, &b.Source, &b.Instance, &b.Request, &b.Attempt, &b.Provider, &b.Model, &b.Upstream, &b.Key, &b.At, &b.Started, &b.Base, &b.Hash, &ids, &b.Completions, &b.Status, &b.ErrorSHA256, &b.Endpoint); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(ids, &b.IDs); err != nil {
			rows.Close()
			return nil, err
		}
		facts = append(facts, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(facts) > maxBillingFacts {
		return nil, errors.New("billing query limit exceeded")
	}
	if err = s.supplementBillingErrorEvidence(ctx, facts); err != nil {
		return nil, err
	}
	totals := map[string]*attributedSpend{}
	sums := map[string]*big.Rat{}
	ensure := func(source, provider, model, upstream string) *attributedSpend {
		id := billingRowID(source, provider, model, upstream)
		if totals[id] == nil {
			totals[id] = &attributedSpend{Source: source, Provider: provider, Model: model, UpstreamModel: upstream, Status: "complete", From: from, To: to, Scope: "matched_requests"}
			sums[id] = new(big.Rat)
		}
		return totals[id]
	}
	// Completion facts without any companion correlation fact are old data (or a
	// missing export), never proof of zero upstream cost. Search companions outside
	// this window because generic streaming headers can precede stream completion.
	legacySQL := `WITH selected AS (SELECT * FROM facts WHERE kind='attempt' AND ` + where + `) SELECT a.source_id,a.provider,a.model,a.upstream_model,count(*) FROM selected a WHERE NOT EXISTS(SELECT 1 FROM facts b WHERE b.kind='billing' AND b.source_id=a.source_id AND b.instance_id=a.instance_id AND b.request_id=a.request_id AND b.attempt_id=a.attempt_id AND b.provider=a.provider AND b.key_id=a.key_id AND b.model=a.model AND b.upstream_model=a.upstream_model AND b.endpoint=a.endpoint AND b.stream=a.stream) GROUP BY a.source_id,a.provider,a.model,a.upstream_model`
	rows, err = s.engine.DB.QueryContext(ctx, legacySQL, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var source, provider, model, upstream string
		var count int
		if err = rows.Scan(&source, &provider, &model, &upstream, &count); err != nil {
			rows.Close()
			return nil, err
		}
		item := ensure(source, provider, model, upstream)
		item.Total += count
		item.Missing += count
		item.LegacyMissing += count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	bindings, err := s.receiptBindings(ctx, owner)
	if err != nil {
		return nil, err
	}
	windows := map[string][2]int64{}
	selectedBindings := map[string]receiptScope{}
	ids := map[string]bool{}
	accounts := map[string]bool{}
	for _, b := range facts {
		item := ensure(b.Source, b.Provider, b.Model, b.Upstream)
		item.Total++
		if b.Completions > 1 {
			item.Ambiguous++
			continue
		}
		if len(b.IDs) == 0 && !confirmedBalanceRejection(b) {
			item.Missing++
			item.HeaderMissing++
			if item.MissingStatuses == nil {
				item.MissingStatuses = map[string]int{}
			}
			item.MissingStatuses[strconv.Itoa(b.Status)]++
			item.unresolved(b, "missing_response_identifier")
			continue
		}
		matches := bindings[subBindingSite(b.Base)+"\n"+b.Hash]
		if len(matches) != 1 || b.Hash == "" || b.Base == "" {
			item.Missing++
			item.Unbound++
			continue
		}
		scope := matches[0]
		selectedBindings[b.Event] = scope
		key := scope.Account
		accounts[scope.Account] = true
		window, exists := windows[key]
		lo := min(b.Started, b.At) - 60000
		if !exists {
			window = [2]int64{max(0, lo), min(time.Now().UnixMilli(), max(to*1000, b.At+60000))}
		} else {
			window[0] = min(window[0], max(0, lo))
			window[1] = max(window[1], min(time.Now().UnixMilli(), max(to*1000, b.At+60000)))
		}
		windows[key] = window
		for _, id := range b.IDs {
			ids[id] = true
		}
	}
	for account, window := range windows {
		if err = s.queueAccountSpendWindow(ctx, account, window[0], window[1]); err != nil {
			return nil, err
		}
	}
	// Look for competing claims across ALL sources and caller keys, not just the
	// currently selected row. A duplicated ID must never charge two different keys.
	ownership := map[string]int{}
	if len(ids) > 0 {
		claimsSQL := alignment + `, target AS (SELECT DISTINCT ` + billingSiteSQL + ` AS upstream_base,upstream_key_hash,unnest(billing_request_ids) AS rid FROM aligned_billing WHERE kind='billing' AND ` + where + `), claims AS (SELECT event_id,` + billingSiteSQL + ` AS upstream_base,upstream_key_hash,unnest(billing_request_ids) AS rid FROM facts WHERE kind='billing') SELECT t.upstream_base,t.upstream_key_hash,t.rid,count(DISTINCT c.event_id) FROM target t JOIN claims c ON c.upstream_base=t.upstream_base AND c.upstream_key_hash=t.upstream_key_hash AND c.rid=t.rid GROUP BY t.upstream_base,t.upstream_key_hash,t.rid`
		rows, err = s.engine.DB.QueryContext(ctx, claimsSQL, alignedArgs...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var base, hash, id string
			var n int
			if err = rows.Scan(&base, &hash, &id, &n); err != nil {
				rows.Close()
				return nil, err
			}
			ownership[receiptOwnerKey(base, hash, id)] = n
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	type bill struct {
		ID   int64
		Cost string
	}
	// Read coverage and cached rows from the same snapshot. Otherwise a scan
	// completing between the two reads can falsely label a newly saved bill absent.
	ledger, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer ledger.Rollback()
	logs := map[string][]bill{}
	if len(ids) > 0 {
		requestIDs := make([]string, 0, len(ids))
		for id := range ids {
			requestIDs = append(requestIDs, id)
		}
		accountIDs := make([]string, 0, len(accounts))
		for id := range accounts {
			accountIDs = append(accountIDs, id)
		}
		rows, err = ledger.QueryContext(ctx, `SELECT account_id,key_id,log_id,request_id,actual_cost::text FROM console_sub_spend_logs WHERE account_id=ANY($1) AND request_id=ANY($2)`, accountIDs, requestIDs)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var account, rid, cost string
			var key, id int64
			if err = rows.Scan(&account, &key, &id, &rid, &cost); err != nil {
				rows.Close()
				return nil, err
			}
			identity := receiptIdentity(account, key, rid)
			logs[identity] = append(logs[identity], bill{id, cost})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	type ledgerCoverage struct {
		From, To sql.NullInt64
		Checked  int64
		Failed   bool
	}
	coverage := map[string]ledgerCoverage{}
	for account := range accounts {
		var state ledgerCoverage
		if err = ledger.QueryRowContext(ctx, `SELECT covered_from,covered_to,checked_at,error<>'' FROM console_sub_account_spend_cache WHERE account_id=$1`, account).Scan(&state.From, &state.To, &state.Checked, &state.Failed); err != nil {
			return nil, err
		}
		coverage[account] = state
	}
	if err = ledger.Commit(); err != nil {
		return nil, err
	}
	rowErrors := map[string]bool{}
	claimed := map[string]string{}
	for _, b := range facts {
		scope, ok := selectedBindings[b.Event]
		if !ok {
			continue
		}
		item := ensure(b.Source, b.Provider, b.Model, b.Upstream)
		state := coverage[scope.Account]
		if state.Checked > 0 && (item.CheckedAt == 0 || state.Checked/1000 < item.CheckedAt) {
			item.CheckedAt = state.Checked / 1000
		}
		found := map[int64]bill{}
		ambiguous := false
		for _, id := range b.IDs {
			if ownership[receiptOwnerKey(b.Base, b.Hash, id)] > 1 {
				ambiguous = true
			}
			for _, log := range logs[receiptIdentity(scope.Account, scope.Key, id)] {
				found[log.ID] = log
			}
		}
		if ambiguous || len(found) > 1 {
			item.Ambiguous++
			continue
		}
		if len(found) == 0 {
			// Only the two verified sub2api pre-forward rejection responses are
			// zero-cost evidence. A status code, failed stream, missing bill or
			// current account balance alone is never evidence. A real bill wins.
			if confirmedBalanceRejection(b) {
				item.ConfirmedUnbilled++
				continue
			}
			if state.Failed {
				rowErrors[billingRowID(b.Source, b.Provider, b.Model, b.Upstream)] = true
			} else if state.From.Valid && state.To.Valid && state.From.Int64 <= min(b.Started, b.At) && state.To.Int64 > b.At && state.Checked >= b.At {
				// This only proves that the last complete scan found no receipt.
				// It does not prove zero cost or that a delayed bill cannot appear.
				item.AbsentReceipts++
				if item.AbsentStatuses == nil {
					item.AbsentStatuses = map[string]int{}
				}
				item.AbsentStatuses[strconv.Itoa(b.Status)]++
				item.unresolved(b, "receipt_absent_after_sync")
			}
			continue
		}
		for _, log := range found {
			receipt := receiptIdentity(scope.Account, scope.Key, strconv.FormatInt(log.ID, 10))
			if previous, exists := claimed[receipt]; exists && previous != b.Event {
				item.Ambiguous++
				continue
			}
			claimed[receipt] = b.Event
			value, ok := new(big.Rat).SetString(log.Cost)
			if !ok || value.Sign() < 0 {
				return nil, errors.New("invalid receipt cost")
			}
			sums[billingRowID(b.Source, b.Provider, b.Model, b.Upstream)].Add(sums[billingRowID(b.Source, b.Provider, b.Model, b.Upstream)], value)
			item.Matched++
		}
	}
	out := make([]attributedSpend, 0, len(totals))
	for id, item := range totals {
		amount, _ := sums[id].Float64()
		item.MatchedAmount = amount
		item.Pending = max(0, item.Total-item.Matched-item.ConfirmedUnbilled-item.Missing-item.Ambiguous-item.AbsentReceipts)
		item.Refreshing = item.Pending > 0
		item.SyncError = rowErrors[id]
		switch {
		case item.Ambiguous > 0:
			item.Status = "ambiguous"
			item.Message = "账单标识对应多个请求或多条账单，不能准确归属"
		case item.Missing > 0:
			item.Status = "unmatched"
			item.Message = "部分请求缺少上游响应标识或账号关联，不能用共享 Key 总额替代"
		case rowErrors[id]:
			item.Status = "error"
			item.Message = "站点账单查询失败，后台会自动重试；未匹配金额不按零处理"
		case item.Pending == 0 && item.AbsentReceipts > 0:
			item.Status = "unmatched"
			item.Message = "账单日志已同步，但仍未找到部分请求的账单；金额未知，不按零消费处理"
		case item.Matched+item.ConfirmedUnbilled < item.Total:
			item.Status = "pending"
			item.Message = "正在逐请求同步账单；尚未匹配的请求不按零消费处理"
		default:
			item.Amount = &amount
			item.Message = "按当前来源、调用 Key、渠道、模型、端点与流式筛选关联；每条账单只计一次"
		}
		out = append(out, *item)
	}
	return out, nil
}
func (s *Service) receiptBindings(ctx context.Context, owner string) (map[string][]receiptScope, error) {
	out := map[string][]receiptScope{}
	add := func(base, hash string, scope receiptScope) {
		site := subBindingSite(base)
		if site == "" || hash == "" || scope.Key <= 0 {
			return
		}
		scope.Site = site
		k := site + "\n" + hash
		for _, old := range out[k] {
			if old.Account == scope.Account && old.Key == scope.Key {
				return
			}
		}
		out[k] = append(out[k], scope)
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT a.id,a.base,i.key_hash,i.remote_key_id,i.group_id FROM console_sub_key_index i JOIN console_sub_accounts a ON a.id=i.account_id WHERE a.owner=$1`, owner)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var account, base, hash string
		var key, group int64
		if err = rows.Scan(&account, &base, &hash, &key, &group); err != nil {
			rows.Close()
			return nil, err
		}
		add(base, hash, receiptScope{Account: account, Key: key, Group: group})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Imported business keys are available immediately, without waiting for the
	// account-wide key index to refresh. Decrypted values never leave this method.
	rows, err = s.control.db.QueryContext(ctx, `SELECT a.id,a.base,t.routing_key_id,t.group_id,t.encrypted_routing_key FROM console_sub_accounts a JOIN console_sub_targets t ON t.account_id=a.id WHERE a.owner=$1 AND t.routing_key_id>0 AND t.encrypted_routing_key<>''`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var account, base, encrypted string
		var key, group int64
		if err = rows.Scan(&account, &base, &key, &group, &encrypted); err != nil {
			return nil, err
		}
		secret, e := s.control.decrypt(encrypted)
		if e != nil {
			continue
		}
		add(base, tokenHash(secret), receiptScope{Account: account, Key: key, Group: group})
	}
	return out, rows.Err()
}

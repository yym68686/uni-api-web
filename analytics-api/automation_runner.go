package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

type automationChannel struct {
	Provider string `json:"provider"`
	Upstream string `json:"upstream_model"`
	Model    string `json:"model"`
	Eligible bool   `json:"eligible"`
}

func (s *Service) automationCatalog(ctx context.Context, src controlSource, t AutomationTask) ([]automationChannel, error) {
	read := func(endpoint string) ([]automationChannel, error) {
		raw, _, e := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {t.KeyID}, "model": {t.Model}, "endpoint": {endpoint}, "stream": {t.Policy.Stream}})
		if e != nil {
			return nil, errors.New("无法读取当前 API key 的渠道目录")
		}
		var channels []automationChannel
		if decodeMap(raw["data"], &channels) != nil || len(channels) == 0 || raw["api_key_id"] != t.KeyID {
			return nil, errors.New("所选 API key 或模型没有渠道")
		}
		seen := map[string]bool{}
		for _, c := range channels {
			if c.Provider == "" || c.Model != t.Model || seen[c.Provider] {
				return nil, errors.New("渠道目录存在重复或不匹配的模型")
			}
			seen[c.Provider] = true
		}
		return channels, nil
	}
	selected, e := read(t.Policy.Endpoint)
	if e != nil {
		return nil, e
	}
	if t.Kind == "quality" {
		return selected, nil
	}
	// A key/model rule affects every endpoint. Preserve excluded providers as
	// barriers in the full order; never accidentally promote past unseen routes.
	all, e := read("all")
	if e != nil {
		return nil, e
	}
	eligible := map[string]bool{}
	for _, c := range selected {
		eligible[c.Provider] = c.Eligible
	}
	for i := range all {
		all[i].Eligible = all[i].Eligible && eligible[all[i].Provider]
	}
	return all, nil
}
func channelOrder(cs []automationChannel) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Provider)
	}
	return out
}
func exactRule(l retainedLive, t AutomationTask) retainedRule {
	for _, r := range l.Rules {
		if r.KeyID == t.KeyID && r.Model == t.Model {
			return r
		}
	}
	return retainedRule{KeyID: t.KeyID, Model: t.Model, Order: []string{}, Disabled: []string{}}
}
func (s *Service) automationFixedPriority(ctx context.Context, src controlSource, t AutomationTask) error {
	if src.configKeyError != nil {
		return errors.New("配置读取密钥不可用")
	}
	if src.ConfigKey != "" {
		src.Key = src.ConfigKey
	}
	raw, _, e := subGateway(ctx, src, "GET", "/v1/api_config", nil)
	if e != nil {
		return errors.New("无法确认 API key 调度算法，请配置可读取配置的管理员密钥")
	}
	var config struct {
		Keys []struct {
			Key         string         `json:"api"`
			Preferences map[string]any `json:"preferences"`
		} `json:"api_keys"`
	}
	if decodeMap(raw["api_config"], &config) != nil {
		return errors.New("来源配置格式无效")
	}
	for _, k := range config.Keys {
		if "key-"+tokenHash(k.Key) == t.KeyID {
			algorithm, _ := k.Preferences["SCHEDULING_ALGORITHM"].(string)
			if algorithm == "" {
				algorithm, _ = k.Preferences["api_key_schedule_algorithm"].(string)
			}
			algorithm = strings.ToLower(strings.TrimSpace(algorithm))
			if algorithm != "" && algorithm != "fixed_priority" {
				return errors.New("自动调序仅支持 fixed_priority 调度算法")
			}
			return nil
		}
	}
	return errors.New("配置中找不到所选 API key")
}
func (s *Service) insertAutomationAudit(ctx context.Context, t AutomationTask, status, action string, before, after []string, changes []map[string]any, metrics map[string]any, reason string) (int64, error) {
	if before == nil {
		before = []string{}
	}
	if after == nil {
		after = []string{}
	}
	if changes == nil {
		changes = []map[string]any{}
	}
	if metrics == nil {
		metrics = map[string]any{}
	}
	var id int64
	e := s.control.db.QueryRowContext(ctx, `INSERT INTO console_automation_audit(task_id,task_name,status,action,source_id,model,before_order,after_order,changes,metrics,reason,policy) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9::jsonb,$10::jsonb,$11,$12::jsonb) RETURNING id`, t.ID, t.Name, status, action, t.SourceID, t.Model, mustJSON(before), mustJSON(after), mustJSON(changes), mustJSON(metrics), reason, mustJSON(t.Policy)).Scan(&id)
	return id, e
}
func (s *Service) automationStreak(ctx context.Context, t AutomationTask, proposal string, watermark int64, participantSamples ...map[string]string) (int, bool, error) {
	rows, e := s.control.db.QueryContext(ctx, `SELECT status,metrics,run_at FROM console_automation_audit WHERE task_id=$1 AND status<>'running' ORDER BY id DESC LIMIT 100`, t.ID)
	if e != nil {
		return 0, false, e
	}
	defer rows.Close()
	streak := 0
	fresh := true
	for rows.Next() {
		var status string
		var raw []byte
		var at time.Time
		if e = rows.Scan(&status, &raw, &at); e != nil {
			return 0, false, e
		}
		var m struct {
			Proposal     string            `json:"proposal"`
			Watermark    int64             `json:"watermark"`
			TaskRevision int64             `json:"task_revision"`
			Samples      map[string]string `json:"samples"`
		}
		if json.Unmarshal(raw, &m) != nil {
			break
		}
		if status != "waiting" && status != "suggested" && status != "cooldown" && status != "no_new_data" {
			break
		}
		if m.Proposal != proposal || m.TaskRevision != t.Revision || time.Since(at) > time.Duration(max(t.Policy.MaxAgeSeconds, t.IntervalSeconds*2))*time.Second {
			break
		}
		if streak == 0 {
			if watermark <= m.Watermark {
				fresh = false
			}
			if len(participantSamples) > 0 {
				for provider, current := range participantSamples[0] {
					if m.Samples[provider] == current {
						fresh = false
					}
				}
			}
		}
		if status != "no_new_data" {
			streak++
		}
	}
	return streak, fresh, rows.Err()
}
func (s *Service) runAutomation(parent context.Context, t AutomationTask) {
	ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
	defer cancel()
	done := make(chan struct{})
	var heartbeat sync.WaitGroup
	heartbeat.Add(1)
	go func() {
		defer heartbeat.Done()
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				res, e := s.control.db.ExecContext(ctx, `UPDATE console_automation_tasks SET lease_until=now()+interval '5 minutes' WHERE id=$1 AND revision=$2 AND enabled AND NOT archived AND lease_token=$3`, t.ID, t.Revision, t.LeaseToken)
				if e != nil {
					cancel()
					return
				}
				n, _ := res.RowsAffected()
				if n != 1 {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		close(done)
		heartbeat.Wait()
		cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_, e := s.control.db.ExecContext(cleanup, `UPDATE console_automation_tasks SET lease_token='',lease_until=NULL,next_run=now()+make_interval(secs=>interval_seconds) WHERE id=$1 AND lease_token=$2`, t.ID, t.LeaseToken)
		if e != nil {
			log.Printf("automation task=%s lease_release_failed", t.ID)
		}
	}()
	var runAuditID int64
	audit := func(status, reason string, before, after []string, changes []map[string]any, metrics map[string]any) {
		cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		var err error
		if runAuditID == 0 {
			_, err = s.insertAutomationAudit(cleanup, t, status, "observe", before, after, changes, metrics, reason)
		} else {
			if before == nil {
				before = []string{}
			}
			if after == nil {
				after = []string{}
			}
			if changes == nil {
				changes = []map[string]any{}
			}
			if metrics == nil {
				metrics = map[string]any{}
			}
			_, err = s.control.db.ExecContext(cleanup, `UPDATE console_automation_audit SET status=$2,reason=$3,before_order=$4::jsonb,after_order=$5::jsonb,changes=$6::jsonb,metrics=$7::jsonb WHERE id=$1`, runAuditID, status, reason, mustJSON(before), mustJSON(after), mustJSON(changes), mustJSON(metrics))
		}
		if err != nil {
			log.Printf("automation task=%s audit_write_failed status=%s", t.ID, status)
		}
	}
	if _, e := automationPolicy(t.Policy); e != nil {
		audit("error", e.Error(), nil, nil, nil, nil)
		return
	}
	var runErr error
	runAuditID, runErr = s.insertAutomationAudit(ctx, t, "running", "observe", nil, nil, nil, map[string]any{"task_revision": t.Revision}, "任务正在执行")
	if runErr != nil {
		log.Printf("automation task=%s run_journal_failed", t.ID)
		return
	}
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_, _ = s.control.db.ExecContext(cleanup, `UPDATE console_automation_audit SET status='completed',reason='本次执行已结束；配置提交记录单独列出' WHERE id=$1 AND status='running'`, runAuditID)
	}()
	src, e := s.control.source(ctx, t.SourceID)
	if e != nil {
		audit("error", "来源不可用", nil, nil, nil, nil)
		return
	}
	catalog, e := s.automationCatalog(ctx, src, t)
	if e != nil {
		audit("error", e.Error(), nil, nil, nil, nil)
		return
	}
	if t.Kind == "quality" || t.Policy.QualityCheck && t.Model == checkModel {
		guard := func() bool {
			var valid bool
			err := s.control.db.QueryRowContext(ctx, `SELECT enabled AND NOT archived AND revision=$2 AND lease_token=$3 AND lease_until>now() FROM console_automation_tasks WHERE id=$1`, t.ID, t.Revision, t.LeaseToken).Scan(&valid)
			return err == nil && valid
		}
		results := s.runAutomationQualityChecks(ctx, src, catalog, t.Policy.QualityConcurrency, guard)
		if t.Kind == "quality" {
			status := "checked"
			if ctx.Err() != nil {
				status = "error"
			}
			audit(status, fmt.Sprintf("已处理 %d/%d 个渠道；检测结果和失败原因见详情", len(results), len(catalog)), nil, nil, nil, map[string]any{"checks": results})
			return
		}
	}
	if !s.analyticsReady() {
		audit("skipped", "历史数据未就绪，本次未调序", nil, nil, nil, nil)
		return
	}
	status := s.importStatus([]string{t.SourceID})
	lastScan, _ := status["last_scan_ms"].(int64)
	if lastScan == 0 || time.Now().UnixMilli()-lastScan > int64(t.Policy.MaxAgeSeconds)*1000 || status["caught_up"] != true {
		audit("skipped", "历史采集尚未追平或扫描已过期，本次不调序", nil, nil, nil, nil)
		return
	}
	if e = s.automationFixedPriority(ctx, src, t); e != nil {
		audit("skipped", e.Error(), nil, nil, nil, nil)
		return
	}
	p := t.Policy
	if t.Model != checkModel {
		p.Metrics = slices.DeleteFunc(slices.Clone(p.Metrics), func(m string) bool { return m == "quality" })
	}
	raw, _, e := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if e != nil {
		audit("error", "临时控制读取失败", nil, nil, nil, nil)
		return
	}
	live, e := decodeRetained(raw)
	if e != nil {
		audit("error", e.Error(), nil, nil, nil, nil)
		return
	}
	// Read the catalog between two control reads: a concurrent UI change makes
	// this observation invalid instead of supplying an invented fallback order.
	catalog, e = s.automationCatalog(ctx, src, t)
	if e != nil {
		audit("error", e.Error(), nil, nil, nil, nil)
		return
	}
	verify, _, e := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if e != nil || verify["revision"] != live.Revision {
		audit("conflict", "读取目录期间配置发生变化，等待下次执行", nil, nil, nil, nil)
		return
	}
	metrics, e := s.automationMetrics(ctx, t, catalog)
	if e != nil {
		audit("error", e.Error(), nil, nil, nil, nil)
		return
	}
	before := channelOrder(catalog)
	after, changes, reasons := automationPlan(before, metrics, p)
	var watermark int64
	for _, m := range metrics {
		watermark = max(watermark, m.Latest, m.QualityLatest*1000)
	}
	detail := map[string]any{"channels": metrics, "comparisons": reasons, "task_revision": t.Revision, "watermark": watermark, "control_revision": live.Revision, "previous_rule": exactRule(live, t), "key_id": t.KeyID}
	if len(changes) == 0 {
		audit("unchanged", "没有满足支配条件的上移；详情包含样本和区间判定原因", before, after, changes, detail)
		return
	}
	proposal := tokenHash(mustJSON([]any{before, after, live.Revision, t.Revision}))
	detail["proposal"] = proposal
	samples := map[string]string{}
	for _, change := range changes {
		for _, field := range []string{"provider", "over"} {
			provider := change[field].(string)
			m := metrics[provider]
			samples[provider] = tokenHash(mustJSON([]any{m.Success, m.SuccessN, m.SuccessLatest, m.Latency, m.LatencyN, m.LatencyLatest, m.Cache, m.CacheN, m.CacheLatest, m.Quality, m.QualityN, m.QualityLatest}))
		}
	}
	detail["samples"] = samples
	streak, fresh, e := s.automationStreak(ctx, t, proposal, watermark, samples)
	if e != nil {
		audit("error", "连续确认记录读取失败", before, after, changes, detail)
		return
	}
	if !fresh { // Do not create another confirmation from the same facts.
		audit("no_new_data", "指标没有新样本，本次不增加连续确认次数", before, after, changes, detail)
		return
	}
	streak++
	detail["confirmations"] = streak
	if streak < p.RequireConsecutive {
		audit("waiting", fmt.Sprintf("同一变动已连续确认 %d/%d 次；每次需要新数据", streak, p.RequireConsecutive), before, after, changes, detail)
		return
	}
	var last time.Time
	e = s.control.db.QueryRowContext(ctx, `SELECT COALESCE(max(run_at),to_timestamp(0)) FROM console_automation_audit WHERE task_id=$1 AND status IN ('applied','rolled_back','prepared','uncertain')`, t.ID).Scan(&last)
	if e != nil {
		audit("error", "冷却状态读取失败", before, after, changes, detail)
		return
	}
	if time.Since(last) < time.Duration(p.CooldownSeconds)*time.Second {
		audit("cooldown", "仍在冷却时间内，本次未调序", before, after, changes, detail)
		return
	}
	if p.Action == "suggest" {
		audit("suggested", "已满足全部条件；执行方式为仅生成建议", before, after, changes, detail)
		return
	}
	if e = s.applyAutomation(ctx, t, src, live, catalog, after, changes, detail); e != nil {
		audit("error", e.Error(), before, after, changes, detail)
	}
}
func (s *Service) applyAutomation(ctx context.Context, t AutomationTask, src controlSource, observed retainedLive, catalog []automationChannel, after []string, changes []map[string]any, detail map[string]any) error {
	currentSource, err := s.control.source(ctx, src.ID)
	if err != nil || controlTarget(currentSource) != controlTarget(src) {
		return errors.New("来源连接已修改，本次不应用")
	}
	if err = s.automationFixedPriority(ctx, src, t); err != nil {
		return err
	}
	unlock, e := s.control.lockControls(ctx, src.ID)
	if e != nil {
		return e
	}
	defer unlock()
	raw, e := s.reconcileControls(ctx, src)
	if e != nil {
		return e
	}
	if raw["revision"] != observed.Revision {
		return errors.New("临时控制版本已变化，本次建议已失效")
	}
	current, e := s.automationCatalog(ctx, src, t)
	if e != nil {
		return e
	}
	if mustJSON(current) != mustJSON(catalog) {
		return errors.New("渠道可用性或目录已变化，本次建议已失效")
	}
	// Hold the task row until the network mutation has settled so pause/edit and
	// a reclaimed lease cannot race the final configuration write.
	tx, e := s.control.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var valid bool
	e = tx.QueryRowContext(ctx, `SELECT enabled AND NOT archived AND revision=$2 AND lease_token=$3 AND lease_until>now() FROM console_automation_tasks WHERE id=$1 FOR NO KEY UPDATE`, t.ID, t.Revision, t.LeaseToken).Scan(&valid)
	if e != nil || !valid {
		return errors.New("任务已暂停、更新或租约失效，本次不应用")
	}
	var unresolved bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_automation_audit WHERE task_id=$1 AND status IN ('prepared','uncertain'))`, t.ID).Scan(&unresolved)
	if e != nil || unresolved {
		return errors.New("存在未确认的历史提交，请先核对来源规则")
	}
	var last time.Time
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(max(run_at),to_timestamp(0)) FROM console_automation_audit WHERE task_id=$1 AND status IN ('applied','rolled_back')`, t.ID).Scan(&last); e != nil {
		return errors.New("冷却状态读取失败")
	}
	if time.Since(last) < time.Duration(t.Policy.CooldownSeconds)*time.Second {
		return errors.New("仍在冷却时间内，请稍后应用")
	}
	previous := exactRule(observed, t)
	disabled := append([]string{}, previous.Disabled...)
	id, e := s.insertAutomationAudit(ctx, t, "prepared", "apply", channelOrder(catalog), after, changes, detail, "准备提交：审计已持久化")
	if e != nil {
		return errors.New("无法保存提交审计，未修改渠道")
	}
	next, _, sendErr := subGateway(ctx, src, "POST", "/v1/channel-controls", map[string]any{"revision": observed.Revision, "action": "set", "api_key_id": t.KeyID, "model": t.Model, "order": after, "disabled": disabled})
	status, reason := "applied", "已应用临时顺序，停用规则保持原样"
	if sendErr != nil {
		next, _, e = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
		if e != nil {
			status, reason = "uncertain", "提交响应不确定，无法读取来源核对；禁止自动重试"
		}
	}
	if status != "uncertain" {
		actual, decodeErr := decodeRetained(next)
		r := exactRule(actual, t)
		if decodeErr != nil || actual.Instance != observed.Instance || !slices.Equal(r.Order, after) || !slices.Equal(r.Disabled, disabled) {
			status, reason = "uncertain", "来源返回状态与预期不一致；保留审计并停止自动重试"
		} else {
			detail["applied_revision"] = actual.Revision
			if e = s.saveLiveControls(ctx, src, next); e != nil {
				status, reason = "uncertain", "顺序已生效，但恢复配置保存失败；请核对后再继续"
			}
		}
	}
	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_, e = s.control.db.ExecContext(cleanup, `UPDATE console_automation_audit SET status=$2,reason=$3,metrics=$4::jsonb WHERE id=$1`, id, status, reason, mustJSON(detail))
	if e != nil {
		return errors.New("提交后审计写入失败；原准备记录已保留，禁止自动重试")
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	if status == "uncertain" {
		return errors.New(reason)
	}
	return nil
}
func (s *Service) runAutomationQualityChecks(ctx context.Context, src controlSource, channels []automationChannel, concurrency int, guards ...func() bool) map[string]string {
	out := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, concurrency)
	for _, channel := range channels {
		if !channel.Eligible {
			mu.Lock()
			out[channel.Provider] = "渠道不可用或已停用，跳过"
			mu.Unlock()
			continue
		}
		provider := channel.Provider
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return out
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			result := ""
			defer func() { mu.Lock(); out[provider] = result; mu.Unlock() }()
			for _, guard := range guards {
				if !guard() {
					result = "任务已暂停或更新，跳过"
					return
				}
			}
			checkCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
			defer cancel()
			id := "automation-" + randomID()
			var claimed string
			e := s.control.db.QueryRowContext(checkCtx, `INSERT INTO console_channel_check_runs(source_id,provider,run_id,expires_at) VALUES($1,$2,$3,now()+interval '70 seconds') ON CONFLICT(source_id,provider) DO UPDATE SET run_id=excluded.run_id,expires_at=excluded.expires_at WHERE console_channel_check_runs.expires_at<now() RETURNING run_id`, src.ID, provider, id).Scan(&claimed)
			if e != nil {
				result = "已有检测正在运行或检测锁不可用"
				return
			}
			defer func() {
				cleanup, c := context.WithTimeout(context.Background(), 3*time.Second)
				defer c()
				_, _ = s.control.db.ExecContext(cleanup, `DELETE FROM console_channel_check_runs WHERE source_id=$1 AND provider=$2 AND run_id=$3`, src.ID, provider, id)
			}()
			if e = s.control.beginQuality(checkCtx, id, qualityScope{Source: src.ID, Provider: provider}, 70*time.Second); e != nil {
				result = "检测历史保存失败，未发起请求"
				return
			}
			checked := runChannelCheck(checkCtx, src, provider)
			if e = s.control.finishQuality(id, checked.Verdict, checked.Verdict == "pass" || checked.Verdict == "fail", checked.CheckedAt, checked); e != nil {
				result = "检测完成但历史保存失败"
				return
			}
			_, e = s.control.db.ExecContext(checkCtx, `INSERT INTO console_channel_checks(source_id,provider,result) VALUES($1,$2,$3) ON CONFLICT(source_id,provider) DO UPDATE SET result=excluded.result`, src.ID, provider, mustJSON(checked))
			if e != nil {
				result = "历史已保存，但最新结果更新失败"
				return
			}
			result = checked.Verdict
			if checked.Message != "" {
				result += "：" + checked.Message
			}
		}()
	}
	wg.Wait()
	return out
}
func (s *controlStore) claimAutomations(ctx context.Context) ([]AutomationTask, error) {
	// One claim per free worker. Leases last longer than the bounded run and are
	// renewed; scheduling waits until completion before starting the next period.
	token := randomID()
	row := s.db.QueryRowContext(ctx, `UPDATE console_automation_tasks SET lease_token=$1,lease_until=now()+interval '5 minutes',last_run=now() WHERE id=(SELECT id FROM console_automation_tasks WHERE enabled AND NOT archived AND next_run<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY next_run FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+automationColumns, token)
	t, e := decodeAutomationTask(row)
	if errors.Is(e, sql.ErrNoRows) {
		return []AutomationTask{}, nil
	}
	if e != nil {
		return nil, e
	}
	_, e = s.db.ExecContext(ctx, `UPDATE console_automation_audit SET status='interrupted',reason='前次执行未正常结束，租约到期后已重新领取' WHERE task_id=$1 AND status='running'`, t.ID)
	if e != nil {
		return nil, e
	}
	return []AutomationTask{t}, nil
}
func (s *Service) automationLoop(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	slots := make(chan struct{}, 2)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			select {
			case slots <- struct{}{}:
			default:
				continue
			}
			tasks, e := s.control.claimAutomations(ctx)
			if e != nil {
				log.Print("automation claim_failed")
				<-slots
				continue
			}
			if len(tasks) == 0 {
				<-slots
				continue
			}
			wg.Add(1)
			go func(t AutomationTask) { defer wg.Done(); defer func() { <-slots }(); s.runAutomation(ctx, t) }(tasks[0])
		}
	}
}
func (s *Service) queueAutomation(w http.ResponseWriter, r *http.Request) {
	owner, e := s.controlUser(r)
	if e != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	res, e := s.control.db.ExecContext(r.Context(), `UPDATE console_automation_tasks SET next_run=now() WHERE id=$1 AND owner=$2 AND enabled AND NOT archived AND (lease_until IS NULL OR lease_until<now())`, r.PathValue("id"), owner)
	if e != nil {
		http.Error(w, "任务排队失败", 503)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		http.Error(w, "任务未启用或已经运行中", 409)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Rollback uses the exact recorded post-apply revision and restores only this
// key/model rule. It never calls reset, which would also delete temporary routes.
func (s *Service) rollbackAutomation(w http.ResponseWriter, r *http.Request) {
	owner, e := s.controlUser(r)
	if e != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	var record automationAudit
	var before, after, raw []byte
	var key string
	e = s.control.db.QueryRowContext(r.Context(), `SELECT a.task_id,a.task_name,a.source_id,a.model,a.status,a.before_order,a.after_order,a.metrics,t.key_id FROM console_automation_audit a JOIN console_automation_tasks t ON t.id=a.task_id WHERE a.id::text=$1 AND t.owner=$2`, r.PathValue("audit"), owner).Scan(&record.TaskID, &record.TaskName, &record.SourceID, &record.Model, &record.Status, &before, &after, &raw, &key)
	if e != nil || record.Status != "applied" {
		http.Error(w, "仅可恢复已确认应用的记录", 409)
		return
	}
	var detail struct {
		AppliedRevision string       `json:"applied_revision"`
		Previous        retainedRule `json:"previous_rule"`
	}
	if json.Unmarshal(raw, &detail) != nil || detail.AppliedRevision == "" {
		http.Error(w, "记录缺少可验证的恢复版本", 409)
		return
	}
	key = detail.Previous.KeyID
	var newOrder []string
	_ = json.Unmarshal(after, &newOrder)
	src, e := s.control.source(r.Context(), record.SourceID)
	if e != nil {
		http.Error(w, "来源不可用", 503)
		return
	}
	unlock, e := s.control.lockControls(r.Context(), src.ID)
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	defer unlock()
	state, e := s.reconcileControls(r.Context(), src)
	if e != nil || state["revision"] != detail.AppliedRevision {
		http.Error(w, "应用后配置已有其他修改，不能自动恢复", 409)
		return
	}
	task := AutomationTask{ID: record.TaskID, Name: record.TaskName, SourceID: record.SourceID, KeyID: key, Model: record.Model}
	tx, e := s.control.db.BeginTx(r.Context(), nil)
	if e != nil {
		http.Error(w, "配置存储不可用", 503)
		return
	}
	defer tx.Rollback()
	var id string
	e = tx.QueryRowContext(r.Context(), `SELECT id FROM console_automation_tasks WHERE id=$1 FOR NO KEY UPDATE`, task.ID).Scan(&id)
	if e != nil {
		http.Error(w, "任务不可用", 503)
		return
	}
	auditID, e := s.insertAutomationAudit(r.Context(), task, "prepared", "rollback", newOrder, detail.Previous.Order, nil, map[string]any{"original_audit": r.PathValue("audit")}, "准备恢复此前的临时规则")
	if e != nil {
		http.Error(w, "无法持久化恢复审计，未修改渠道", 503)
		return
	}
	disabled := append([]string{}, detail.Previous.Disabled...)
	order := append([]string{}, detail.Previous.Order...)
	next, _, sendErr := subGateway(r.Context(), src, "POST", "/v1/channel-controls", map[string]any{"revision": detail.AppliedRevision, "action": "set", "api_key_id": key, "model": task.Model, "order": order, "disabled": disabled})
	status, reason := "rolled_back", "已恢复此前顺序，任务已暂停"
	if sendErr != nil {
		next, _, e = subGateway(r.Context(), src, "GET", "/v1/channel-controls", nil)
	}
	live, de := decodeRetained(next)
	rule := exactRule(live, task)
	if e != nil || de != nil || !slices.Equal(rule.Order, order) || !slices.Equal(rule.Disabled, disabled) {
		status, reason = "uncertain", "恢复提交无法确认，请核对来源规则"
	} else if e = s.saveLiveControls(r.Context(), src, next); e != nil {
		status, reason = "uncertain", "恢复已生效，但恢复配置保存失败"
	}
	_, e = tx.ExecContext(r.Context(), `UPDATE console_automation_tasks SET enabled=false,revision=revision+1 WHERE id=$1`, task.ID)
	if e == nil {
		_, e = tx.ExecContext(r.Context(), `UPDATE console_automation_audit SET status=$2,reason=$3 WHERE id=$1`, auditID, status, reason)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		http.Error(w, "恢复审计写入失败，请刷新核对", 503)
		return
	}
	if status == "uncertain" {
		http.Error(w, reason, 409)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Service) applyAutomationSuggestion(w http.ResponseWriter, r *http.Request) {
	owner, e := s.controlUser(r)
	if e != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	var taskID, status string
	var raw, beforeRaw, afterRaw, changesRaw []byte
	e = s.control.db.QueryRowContext(r.Context(), `SELECT a.task_id,a.status,a.metrics,a.before_order,a.after_order,a.changes FROM console_automation_audit a JOIN console_automation_tasks t ON t.id=a.task_id WHERE a.id::text=$1 AND t.owner=$2 AND NOT t.archived`, r.PathValue("audit"), owner).Scan(&taskID, &status, &raw, &beforeRaw, &afterRaw, &changesRaw)
	if e != nil || status != "suggested" {
		http.Error(w, "建议不存在或尚未满足连续确认条件", 409)
		return
	}
	t, e := s.control.automationTask(r.Context(), owner, taskID)
	if e != nil {
		http.Error(w, "任务不存在", 404)
		return
	}
	var detail map[string]any
	_ = json.Unmarshal(raw, &detail)
	var before, after []string
	_ = json.Unmarshal(beforeRaw, &before)
	_ = json.Unmarshal(afterRaw, &after)
	if detail["task_revision"] != float64(t.Revision) {
		http.Error(w, "任务配置已变化，请等待新建议", 409)
		return
	}
	if !s.analyticsReady() || s.importStatus([]string{t.SourceID})["caught_up"] != true {
		http.Error(w, "历史数据尚未就绪，请稍后重试", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	token := randomID()
	res, e := s.control.db.ExecContext(ctx, `UPDATE console_automation_tasks SET lease_token=$3,lease_until=now()+interval '1 minute' WHERE id=$1 AND revision=$2 AND enabled AND NOT archived AND (lease_until IS NULL OR lease_until<now())`, t.ID, t.Revision, token)
	if e != nil {
		http.Error(w, "无法领取执行任务", 503)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		http.Error(w, "任务已暂停、更新或正在执行", 409)
		return
	}
	t.LeaseToken = token
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_, _ = s.control.db.ExecContext(cleanup, `UPDATE console_automation_tasks SET lease_token='',lease_until=NULL,next_run=now()+make_interval(secs=>interval_seconds) WHERE id=$1 AND lease_token=$2`, t.ID, token)
	}()
	src, e := s.control.source(ctx, t.SourceID)
	if e != nil {
		http.Error(w, "来源不可用", 503)
		return
	}
	if e = s.automationFixedPriority(ctx, src, t); e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	state, _, e := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if e != nil || state["revision"] != detail["control_revision"] {
		http.Error(w, "渠道规则已变化，建议已失效", 409)
		return
	}
	live, e := decodeRetained(state)
	if e != nil {
		http.Error(w, "临时控制无效", 503)
		return
	}
	catalog, e := s.automationCatalog(ctx, src, t)
	if e != nil || !slices.Equal(channelOrder(catalog), before) {
		http.Error(w, "当前渠道顺序已变化", 409)
		return
	}
	metrics, e := s.automationMetrics(ctx, t, catalog)
	if e != nil {
		http.Error(w, "数据读取失败", 503)
		return
	}
	p := t.Policy
	if t.Model != checkModel {
		p.Metrics = slices.DeleteFunc(slices.Clone(p.Metrics), func(m string) bool { return m == "quality" })
	}
	fresh, changes, _ := automationPlan(before, metrics, p)
	if !slices.Equal(fresh, after) || len(changes) == 0 {
		http.Error(w, "最新数据不再支持这次调整，请等待新建议", 409)
		return
	}
	detail["channels"] = metrics
	detail["approved_audit"] = r.PathValue("audit")
	if e = s.applyAutomation(ctx, t, src, live, catalog, after, changes, detail); e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Review an uncertain result without resending a mutation. Whatever is observed,
// pause the task so the operator can inspect before enabling it again.
func (s *Service) reviewAutomationSubmission(w http.ResponseWriter, r *http.Request) {
	owner, e := s.controlUser(r)
	if e != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	var task AutomationTask
	var status string
	var raw, afterRaw []byte
	e = s.control.db.QueryRowContext(r.Context(), `SELECT a.task_id,a.task_name,a.source_id,a.model,a.status,a.metrics,a.after_order FROM console_automation_audit a JOIN console_automation_tasks t ON t.id=a.task_id WHERE a.id::text=$1 AND t.owner=$2`, r.PathValue("audit"), owner).Scan(&task.ID, &task.Name, &task.SourceID, &task.Model, &status, &raw, &afterRaw)
	if e != nil || (status != "prepared" && status != "uncertain") {
		http.Error(w, "没有待核对的提交", 409)
		return
	}
	var detail map[string]any
	var prior retainedRule
	_ = json.Unmarshal(raw, &detail)
	_ = decodeMap(detail["previous_rule"], &prior)
	task.KeyID = prior.KeyID
	src, e := s.control.source(r.Context(), task.SourceID)
	if e != nil {
		http.Error(w, "来源不可用", 503)
		return
	}
	unlock, e := s.control.lockControls(r.Context(), src.ID)
	if e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	defer unlock()
	liveMap, _, e := subGateway(r.Context(), src, "GET", "/v1/channel-controls", nil)
	if e != nil {
		http.Error(w, "来源暂不可用，请稍后再核对", 503)
		return
	}
	live, e := decodeRetained(liveMap)
	if e != nil {
		http.Error(w, e.Error(), 503)
		return
	}
	rule := exactRule(live, task)
	var after []string
	_ = json.Unmarshal(afterRaw, &after)
	detail["reviewed_revision"] = live.Revision
	detail["reviewed_rule"] = rule
	final, reason := "reviewed", "已读取当前配置并暂停任务；未重新提交修改"
	if task.KeyID != "" && slices.Equal(rule.Order, after) && slices.Equal(rule.Disabled, prior.Disabled) {
		if e = s.saveLiveControls(r.Context(), src, liveMap); e != nil {
			http.Error(w, "当前规则符合预期，但保存恢复配置仍失败", 503)
			return
		}
		final = "applied"
		detail["applied_revision"] = live.Revision
		reason = "已核实当前规则符合预期，任务已暂停"
	}
	tx, e := s.control.db.BeginTx(r.Context(), nil)
	if e != nil {
		http.Error(w, "审计存储不可用", 503)
		return
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(r.Context(), `UPDATE console_automation_tasks SET enabled=false,revision=revision+1 WHERE id=$1`, task.ID)
	if e == nil {
		_, e = tx.ExecContext(r.Context(), `UPDATE console_automation_audit SET status=$2,reason=$3,metrics=$4::jsonb WHERE id::text=$1 AND status IN ('prepared','uncertain')`, r.PathValue("audit"), final, reason, mustJSON(detail))
	}
	if e == nil {
		_, e = tx.ExecContext(r.Context(), `INSERT INTO console_automation_audit(task_id,task_name,status,action,source_id,model,reason,metrics) VALUES($1,$2,'reviewed','review',$3,$4,$5,$6::jsonb)`, task.ID, task.Name, task.SourceID, task.Model, reason, mustJSON(map[string]any{"original_audit": r.PathValue("audit"), "observed_rule": rule, "observed_revision": live.Revision}))
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		http.Error(w, "核对记录保存失败", 503)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

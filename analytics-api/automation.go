package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Automation is deliberately stored as a policy document. Adding a new
// threshold does not require a database migration, while the top-level task
// fields remain indexed for scheduling and ownership checks.
const automationSchema = `
CREATE TABLE IF NOT EXISTS console_automation_tasks(
 id TEXT PRIMARY KEY, owner TEXT NOT NULL, name TEXT NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT false, source_id TEXT NOT NULL,
 key_id TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
 interval_seconds INTEGER NOT NULL, range_name TEXT NOT NULL,
 policy JSONB NOT NULL DEFAULT '{}', next_run TIMESTAMPTZ NOT NULL DEFAULT now(),
 last_run TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(source_id) REFERENCES console_sources(id) ON DELETE CASCADE);
CREATE INDEX IF NOT EXISTS console_automation_due ON console_automation_tasks(enabled,next_run);
CREATE TABLE IF NOT EXISTS console_automation_audit(
 id BIGSERIAL PRIMARY KEY, task_id TEXT NOT NULL REFERENCES console_automation_tasks(id) ON DELETE CASCADE,
 run_at TIMESTAMPTZ NOT NULL DEFAULT now(), status TEXT NOT NULL, action TEXT NOT NULL,
 source_id TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
 before_order JSONB NOT NULL DEFAULT '[]', after_order JSONB NOT NULL DEFAULT '[]',
 changes JSONB NOT NULL DEFAULT '[]', metrics JSONB NOT NULL DEFAULT '{}',
 reason TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS console_automation_audit_task ON console_automation_audit(task_id,id DESC);`

type AutomationPolicy struct {
	Metrics            []string `json:"metrics"`
	MinSuccessSamples  int      `json:"min_success_samples"`
	MinCacheSamples    int      `json:"min_cache_samples"`
	MinLatencySamples  int      `json:"min_latency_samples"`
	MinQualitySamples  int      `json:"min_quality_samples"`
	ConfidenceLevel    float64  `json:"confidence_level"`
	LatencyQuantile    float64  `json:"latency_quantile"`
	LatencyMinPercent  float64  `json:"latency_min_percent"`
	LatencyMinMS       float64  `json:"latency_min_ms"`
	SuccessMinPP       float64  `json:"success_min_pp"`
	CacheMinPP         float64  `json:"cache_min_pp"`
	QualityMinPP       float64  `json:"quality_min_pp"`
	RequireConsecutive int      `json:"require_consecutive"`
	CooldownSeconds    int      `json:"cooldown_seconds"`
	MaxMoves           int      `json:"max_moves"`
	Action             string   `json:"action"` // suggest or apply
	RequireAllHigher   bool     `json:"require_all_higher"`
	QualityCheck       bool     `json:"quality_check"`
}

func defaultAutomationPolicy() AutomationPolicy {
	return AutomationPolicy{Metrics: []string{"latency", "success", "cache", "quality"}, MinSuccessSamples: 100, MinCacheSamples: 50, MinLatencySamples: 50, MinQualitySamples: 20, ConfidenceLevel: .95, LatencyQuantile: .5, LatencyMinPercent: 10, LatencyMinMS: 100, SuccessMinPP: 2, CacheMinPP: 5, QualityMinPP: 5, RequireConsecutive: 3, CooldownSeconds: 1800, MaxMoves: 1, Action: "suggest", RequireAllHigher: true, QualityCheck: true}
}

type AutomationTask struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Enabled         bool             `json:"enabled"`
	SourceID        string           `json:"source_id"`
	KeyID           string           `json:"key_id"`
	Model           string           `json:"model"`
	IntervalSeconds int              `json:"interval_seconds"`
	Range           string           `json:"range"`
	Policy          AutomationPolicy `json:"policy"`
	NextRun         *time.Time       `json:"next_run,omitempty"`
	LastRun         *time.Time       `json:"last_run,omitempty"`
	CreatedAt       *time.Time       `json:"created_at,omitempty"`
	UpdatedAt       *time.Time       `json:"updated_at,omitempty"`
}

type automationAudit struct {
	ID       int64            `json:"id"`
	TaskID   string           `json:"task_id"`
	RunAt    time.Time        `json:"run_at"`
	Status   string           `json:"status"`
	Action   string           `json:"action"`
	SourceID string           `json:"source_id"`
	Model    string           `json:"model"`
	Before   []string         `json:"before_order"`
	After    []string         `json:"after_order"`
	Changes  []map[string]any `json:"changes"`
	Metrics  map[string]any   `json:"metrics"`
	Reason   string           `json:"reason"`
}

func automationPolicy(in AutomationPolicy) (AutomationPolicy, error) {
	p := in
	if len(p.Metrics) == 0 {
		p = defaultAutomationPolicy()
	}
	valid := map[string]bool{"latency": true, "success": true, "cache": true, "quality": true}
	seen := map[string]bool{}
	for _, metric := range p.Metrics {
		if !valid[metric] || seen[metric] {
			return p, errors.New("自动化指标无效")
		}
		seen[metric] = true
	}
	if p.MinSuccessSamples < 0 || p.MinCacheSamples < 0 || p.MinLatencySamples < 0 || p.MinQualitySamples < 0 || p.RequireConsecutive < 1 || p.CooldownSeconds < 0 || p.MaxMoves < 1 || p.LatencyMinPercent < 0 || p.LatencyMinMS < 0 || p.SuccessMinPP < 0 || p.CacheMinPP < 0 || p.QualityMinPP < 0 || p.ConfidenceLevel <= 0 || p.ConfidenceLevel >= 1 || p.LatencyQuantile <= 0 || p.LatencyQuantile >= 1 {
		return p, errors.New("自动化参数超出范围")
	}
	if p.Action != "suggest" && p.Action != "apply" {
		return p, errors.New("自动化执行方式无效")
	}
	if p.LatencyQuantile != .5 && p.LatencyQuantile != .95 {
		return p, errors.New("首字分位数只能选择 p50 或 p95")
	}
	return p, nil
}

func decodeAutomationTask(row interface{ Scan(...any) error }) (AutomationTask, error) {
	var t AutomationTask
	var raw []byte
	if err := row.Scan(&t.ID, &t.Name, &t.Enabled, &t.SourceID, &t.KeyID, &t.Model, &t.IntervalSeconds, &t.Range, &raw, &t.NextRun, &t.LastRun, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return t, err
	}
	t.Policy = defaultAutomationPolicy()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &t.Policy); err != nil {
			return t, err
		}
	}
	t.Policy, _ = automationPolicy(t.Policy)
	return t, nil
}

func (s *controlStore) automationTasks(ctx context.Context, owner string) ([]AutomationTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,enabled,source_id,key_id,model,interval_seconds,range_name,policy,next_run,last_run,created_at,updated_at FROM console_automation_tasks WHERE owner=$1 ORDER BY created_at,id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutomationTask{}
	for rows.Next() {
		t, e := decodeAutomationTask(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Service) automations(w http.ResponseWriter, r *http.Request) {
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method == http.MethodGet {
		if r.PathValue("id") != "" {
			automationAuditHandler(s, owner, w, r)
			return
		}
		data, e := s.control.automationTasks(r.Context(), owner)
		if e != nil {
			http.Error(w, "自动化任务读取失败", 503)
			return
		}
		writeJSON(w, 200, map[string]any{"data": data})
		return
	}
	var in struct{ AutomationTask }
	if !decodeControlLimit(w, r, &in, 512<<10) {
		return
	}
	in.Policy, err = automationPolicy(in.Policy)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 120 || in.SourceID == "" || in.IntervalSeconds < 10 || in.IntervalSeconds > 86400 || len(in.Range) > 32 {
		http.Error(w, "自动化任务参数无效", 400)
		return
	}
	if in.Policy.Action == "apply" && in.KeyID == "" {
		http.Error(w, "自动应用必须指定 API key", 400)
		return
	}
	if _, err = s.control.source(r.Context(), in.SourceID); err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		id = randomID()
	}
	_, err = s.control.db.ExecContext(r.Context(), `INSERT INTO console_automation_tasks(id,owner,name,enabled,source_id,key_id,model,interval_seconds,range_name,policy,next_run) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,now()) ON CONFLICT(id) DO UPDATE SET name=excluded.name,enabled=excluded.enabled,source_id=excluded.source_id,key_id=excluded.key_id,model=excluded.model,interval_seconds=excluded.interval_seconds,range_name=excluded.range_name,policy=excluded.policy,next_run=CASE WHEN console_automation_tasks.enabled<>excluded.enabled OR console_automation_tasks.interval_seconds<>excluded.interval_seconds THEN now() ELSE console_automation_tasks.next_run END,updated_at=now() WHERE console_automation_tasks.owner=$2`, id, owner, strings.TrimSpace(in.Name), in.Enabled, in.SourceID, in.KeyID, in.Model, in.IntervalSeconds, in.Range, mustJSON(in.Policy))
	if err != nil {
		http.Error(w, "自动化任务保存失败", 503)
		return
	}
	data, e := s.control.automationTask(r.Context(), owner, id)
	if e != nil {
		http.Error(w, "自动化任务读取失败", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"task": data})
}

func (s *controlStore) automationTask(ctx context.Context, owner, id string) (AutomationTask, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,name,enabled,source_id,key_id,model,interval_seconds,range_name,policy,next_run,last_run,created_at,updated_at FROM console_automation_tasks WHERE owner=$1 AND id=$2`, owner, id)
	return decodeAutomationTask(row)
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func (s *Service) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	if _, err = s.control.db.ExecContext(r.Context(), `DELETE FROM console_automation_tasks WHERE owner=$1 AND id=$2`, owner, r.PathValue("id")); err != nil {
		http.Error(w, "自动化任务删除失败", 503)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func automationAuditHandler(s *Service, owner string, w http.ResponseWriter, r *http.Request) {
	var exists bool
	if err := s.control.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM console_automation_tasks WHERE owner=$1 AND id=$2)`, owner, r.PathValue("id")).Scan(&exists); err != nil || !exists {
		http.Error(w, "自动化任务不存在", 404)
		return
	}
	rows, err := s.control.db.QueryContext(r.Context(), `SELECT id,task_id,run_at,status,action,source_id,model,before_order,after_order,changes,metrics,reason FROM console_automation_audit WHERE task_id=$1 ORDER BY id DESC LIMIT 100`, r.PathValue("id"))
	if err != nil {
		http.Error(w, "自动化审计读取失败", 503)
		return
	}
	defer rows.Close()
	out := []automationAudit{}
	for rows.Next() {
		var a automationAudit
		var before, after, changes, metrics []byte
		if err = rows.Scan(&a.ID, &a.TaskID, &a.RunAt, &a.Status, &a.Action, &a.SourceID, &a.Model, &before, &after, &changes, &metrics, &a.Reason); err != nil {
			http.Error(w, "自动化审计读取失败", 503)
			return
		}
		_ = json.Unmarshal(before, &a.Before)
		_ = json.Unmarshal(after, &a.After)
		_ = json.Unmarshal(changes, &a.Changes)
		_ = json.Unmarshal(metrics, &a.Metrics)
		out = append(out, a)
	}
	writeJSON(w, 200, map[string]any{"data": out})
}

type automationMetric struct {
	Provider                             string
	Latency, Success, Cache, Quality     float64
	SuccessN, CacheN, LatencyN, QualityN int
	QualityOK                            bool
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}
func nestedNumber(m map[string]any, key string) (float64, bool) {
	v, ok := m[key].(map[string]any)
	if !ok {
		return 0, false
	}
	return number(v["p50_ms"])
}
func wilson(v float64, n int, z float64) (float64, float64) {
	if n <= 0 {
		return 0, 1
	}
	p := v / float64(n)
	d := 1 + z*z/float64(n)
	c := (p + z*z/(2*float64(n))) / d
	h := z * math.Sqrt((p*(1-p)+z*z/(4*float64(n)))/float64(n)) / d
	return c - h, c + h
}
func confidenceZ(level float64) float64 {
	switch {
	case level >= .99:
		return 2.576
	case level >= .975:
		return 2.241
	case level >= .95:
		return 1.96
	case level >= .9:
		return 1.645
	default:
		return 1.282
	}
}

func (s *Service) runAutomation(ctx context.Context, task AutomationTask) {
	src, err := s.control.source(ctx, task.SourceID)
	if err != nil {
		s.writeAutomationAudit(ctx, task, "error", "none", nil, nil, nil, nil, err.Error())
		return
	}
	result, err := s.engine.Query(ctx, QueryFilter{SourceID: task.SourceID, KeyID: task.KeyID, Model: task.Model, Range: task.Range})
	if err != nil {
		s.writeAutomationAudit(ctx, task, "error", "none", nil, nil, nil, nil, err.Error())
		return
	}
	metrics := map[string]automationMetric{}
	p := task.Policy
	if p.QualityCheck && task.Model == checkModel {
		s.runAutomationQualityChecks(ctx, src, result.Data)
	}
	if task.Model != "gpt-6-astra" {
		filtered := make([]string, 0, len(p.Metrics))
		for _, metric := range p.Metrics {
			if metric != "quality" {
				filtered = append(filtered, metric)
			}
		}
		p.Metrics = filtered
	}
	z := confidenceZ(p.ConfidenceLevel)
	for _, row := range result.Data {
		st := row.Stats
		m := automationMetric{Provider: row.Provider}
		if v, ok := number(st["success_rate"]); ok {
			m.Success = v
		}
		if v, ok := number(st["success_rate_denominator"]); ok {
			m.SuccessN = int(v)
		}
		if v, ok := number(st["cache_rate"]); ok {
			m.Cache = v
		}
		if v, ok := number(st["cache_samples"]); ok {
			m.CacheN = int(v)
		}
		latencyKey := "p50_ms"
		if p.LatencyQuantile >= .95 {
			latencyKey = "p95_ms"
		}
		if created, ok := st["response_created"].(map[string]any); ok {
			if v, ok := number(created[latencyKey]); ok {
				m.Latency = v
			}
		}
		if v, ok := nestedNumber(st, "response_created"); ok && m.Latency == 0 {
			m.Latency = v
		}
		if created, ok := st["response_created"].(map[string]any); ok {
			if v, ok := number(created["sample_count"]); ok {
				m.LatencyN = int(v)
			}
		}
		if task.Model == "gpt-6-astra" {
			var passed, total int
			_ = s.control.db.QueryRowContext(ctx, `SELECT COALESCE(passed,0),COALESCE(successful,0) FROM console_quality_totals WHERE source_id=$1 AND provider=$2`, task.SourceID, row.Provider).Scan(&passed, &total)
			m.QualityN = total
			if total > 0 {
				m.Quality = float64(passed) / float64(total)
				m.QualityOK = true
			}
		}
		metrics[row.Provider] = m
	}
	keys := []string{}
	raw, _, e := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if e != nil {
		s.writeAutomationAudit(ctx, task, "error", "none", nil, nil, nil, nil, e.Error())
		return
	}
	live, e := decodeRetained(raw)
	if e != nil {
		s.writeAutomationAudit(ctx, task, "error", "none", nil, nil, nil, nil, e.Error())
		return
	}
	for _, rule := range live.Rules {
		if (task.KeyID == "" || rule.KeyID == task.KeyID) && rule.Model == task.Model {
			keys = append(keys, rule.Order...)
			break
		}
	}
	if len(keys) == 0 {
		for provider := range metrics {
			keys = append(keys, provider)
		}
		sort.Strings(keys)
	}
	changes := []map[string]any{}
	after := append([]string(nil), keys...)
	maxMoves := p.MaxMoves
	if maxMoves < 1 {
		maxMoves = 1
	}
	for moved := 0; moved < maxMoves; moved++ {
		found := false
		for i := len(after) - 1; i > 0; i-- {
			low, high := metrics[after[i]], metrics[after[i-1]]
			if dominates(low, high, p, z) && (!p.RequireAllHigher || dominatesAllHigher(after, i, metrics, p, z)) {
				after[i], after[i-1] = after[i-1], after[i]
				changes = append(changes, map[string]any{"provider": low.Provider, "over": high.Provider})
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	status, action, reason := "unchanged", "suggest", "没有渠道满足全部支配条件"
	if len(changes) > 0 {
		streak, _ := s.automationStreak(ctx, task.ID)
		streak++
		if streak < p.RequireConsecutive {
			status = "waiting"
			reason = fmt.Sprintf("支配关系已连续确认 %d/%d 次", streak, p.RequireConsecutive)
		} else if p.Action == "apply" && s.automationInCooldown(ctx, task.ID, p.CooldownSeconds) {
			status = "cooldown"
			reason = "仍在冷却时间内，保留建议但不重复应用"
		} else {
			status = "suggested"
		}
		if status == "suggested" && p.Action == "apply" {
			action = "apply"
			body := map[string]any{"revision": live.Revision, "action": "set", "api_key_id": task.KeyID, "model": task.Model, "order": after, "disabled": []string{}}
			if _, _, e = subGateway(ctx, src, "POST", "/v1/channel-controls", body); e != nil {
				status = "error"
				reason = e.Error()
			} else {
				status = "applied"
				reason = "已通过 revision 校验应用临时顺序"
			}
		} else {
			reason = "发现满足支配条件的渠道，等待确认"
		}
	}
	metricJSON := map[string]any{}
	for k, v := range metrics {
		metricJSON[k] = v
	}
	s.writeAutomationAudit(ctx, task, status, action, keys, after, changes, metricJSON, reason)
}

func (s *Service) runAutomationQualityChecks(ctx context.Context, src controlSource, rows []AnalyticChannel) {
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, row := range rows {
		provider := row.Provider
		if provider == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			checkCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
			defer cancel()
			runID := "automation-" + randomID()
			scope := qualityScope{Source: src.ID, Provider: provider}
			var claimed string
			err := s.control.db.QueryRowContext(checkCtx, `INSERT INTO console_channel_check_runs(source_id,provider,run_id,expires_at) VALUES($1,$2,$3,now()+interval '70 seconds') ON CONFLICT(source_id,provider) DO UPDATE SET run_id=excluded.run_id,expires_at=excluded.expires_at WHERE console_channel_check_runs.expires_at < now() RETURNING run_id`, src.ID, provider, runID).Scan(&claimed)
			if err != nil {
				return
			}
			defer s.control.db.ExecContext(context.Background(), `DELETE FROM console_channel_check_runs WHERE source_id=$1 AND provider=$2 AND run_id=$3`, src.ID, provider, runID)
			if s.control.beginQuality(checkCtx, runID, scope, 70*time.Second) != nil {
				return
			}
			result := runChannelCheck(checkCtx, src, provider)
			verdict := result.Verdict
			if checkCtx.Err() != nil && verdict == "error" {
				verdict = "cancelled"
			}
			if s.control.finishQuality(runID, verdict, verdict == "pass" || verdict == "fail", result.CheckedAt, result) != nil {
				return
			}
			raw, _ := json.Marshal(result)
			_, _ = s.control.db.ExecContext(checkCtx, `INSERT INTO console_channel_checks(source_id,provider,result) VALUES($1,$2,$3) ON CONFLICT(source_id,provider) DO UPDATE SET result=excluded.result`, src.ID, provider, string(raw))
		}()
	}
	wg.Wait()
}

func dominatesAllHigher(order []string, index int, metrics map[string]automationMetric, p AutomationPolicy, z float64) bool {
	for j := 0; j < index; j++ {
		if !dominates(metrics[order[index]], metrics[order[j]], p, z) {
			return false
		}
	}
	return true
}

func (s *Service) automationStreak(ctx context.Context, taskID string) (int, error) {
	rows, err := s.control.db.QueryContext(ctx, `SELECT status,changes FROM console_automation_audit WHERE task_id=$1 ORDER BY id DESC LIMIT 100`, taskID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	streak := 0
	for rows.Next() {
		var status string
		var changes []byte
		if err = rows.Scan(&status, &changes); err != nil {
			return streak, err
		}
		if (status != "suggested" && status != "applied" && status != "waiting") || string(changes) == "[]" || string(changes) == "null" {
			break
		}
		streak++
	}
	return streak, rows.Err()
}

func (s *Service) automationInCooldown(ctx context.Context, taskID string, seconds int) bool {
	if seconds <= 0 {
		return false
	}
	var last time.Time
	if err := s.control.db.QueryRowContext(ctx, `SELECT COALESCE(max(run_at),to_timestamp(0)) FROM console_automation_audit WHERE task_id=$1 AND status='applied'`, taskID).Scan(&last); err != nil {
		return false
	}
	return time.Since(last) < time.Duration(seconds)*time.Second
}
func dominates(a, b automationMetric, p AutomationPolicy, z float64) bool {
	if a.Provider == "" || b.Provider == "" {
		return false
	}
	for _, metric := range p.Metrics {
		switch metric {
		case "latency":
			if a.Latency <= 0 || b.Latency <= 0 || a.LatencyN < p.MinLatencySamples || b.LatencyN < p.MinLatencySamples || a.Latency > b.Latency*(1-p.LatencyMinPercent/100) && a.Latency > b.Latency-p.LatencyMinMS {
				return false
			}
		case "success":
			if a.SuccessN < p.MinSuccessSamples || b.SuccessN < p.MinSuccessSamples {
				return false
			}
			alo, ahi := wilson(a.Success*float64(a.SuccessN), a.SuccessN, z)
			_, bhi := wilson(b.Success*float64(b.SuccessN), b.SuccessN, z)
			if alo < bSuccessBound(b.Success, b.SuccessN, z)+p.SuccessMinPP/100 || ahi <= bhi {
				return false
			}
		case "cache":
			if a.CacheN < p.MinCacheSamples || b.CacheN < p.MinCacheSamples {
				return false
			}
			alo, _ := wilson(a.Cache*float64(a.CacheN), a.CacheN, z)
			_, bhi := wilson(b.Cache*float64(b.CacheN), b.CacheN, z)
			if alo < bhi+p.CacheMinPP/100 {
				return false
			}
		case "quality":
			if !a.QualityOK || a.QualityN < p.MinQualitySamples || !b.QualityOK || b.QualityN < p.MinQualitySamples {
				return false
			}
			alo, _ := wilson(a.Quality*float64(a.QualityN), a.QualityN, z)
			_, bhi := wilson(b.Quality*float64(b.QualityN), b.QualityN, z)
			if alo < bhi+p.QualityMinPP/100 {
				return false
			}
		}
	}
	return true
}
func bSuccessBound(v float64, n int, z float64) float64 { _, h := wilson(v*float64(n), n, z); return h }
func (s *Service) writeAutomationAudit(ctx context.Context, t AutomationTask, status, action string, before, after []string, changes []map[string]any, metrics map[string]any, reason string) {
	_, _ = s.control.db.ExecContext(ctx, `INSERT INTO console_automation_audit(task_id,status,action,source_id,model,before_order,after_order,changes,metrics,reason) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8::jsonb,$9::jsonb,$10)`, t.ID, status, action, t.SourceID, t.Model, mustJSON(before), mustJSON(after), mustJSON(changes), mustJSON(metrics), reason)
}

func (s *Service) automationLoop(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.analyticsReady() {
				continue
			}
			tasks, _ := s.control.claimAutomations(ctx)
			for _, task := range tasks {
				s.runAutomation(ctx, task)
			}
		}
	}
}
func (s *controlStore) claimAutomations(ctx context.Context) ([]AutomationTask, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT id,name,enabled,source_id,key_id,model,interval_seconds,range_name,policy,next_run,last_run,created_at,updated_at FROM console_automation_tasks WHERE enabled AND next_run<=now() ORDER BY next_run FOR UPDATE SKIP LOCKED LIMIT 8`)
	if e != nil {
		return nil, e
	}
	tasks := []AutomationTask{}
	for rows.Next() {
		t, e := decodeAutomationTask(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		tasks = append(tasks, t)
	}
	rows.Close()
	for _, t := range tasks {
		if _, e = tx.ExecContext(ctx, `UPDATE console_automation_tasks SET next_run=now()+make_interval(secs=>$2),last_run=now(),updated_at=now() WHERE id=$1`, t.ID, t.IntervalSeconds); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return tasks, nil
}

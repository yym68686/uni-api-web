package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"
)

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
 id BIGSERIAL PRIMARY KEY, task_id TEXT NOT NULL REFERENCES console_automation_tasks(id) ON DELETE RESTRICT,
 run_at TIMESTAMPTZ NOT NULL DEFAULT now(), status TEXT NOT NULL, action TEXT NOT NULL,
 source_id TEXT NOT NULL, model TEXT NOT NULL DEFAULT '',
 before_order JSONB NOT NULL DEFAULT '[]', after_order JSONB NOT NULL DEFAULT '[]',
 changes JSONB NOT NULL DEFAULT '[]', metrics JSONB NOT NULL DEFAULT '{}',
 reason TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS console_automation_audit_task ON console_automation_audit(task_id,id DESC);
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'order';
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS archived BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS lease_until TIMESTAMPTZ;
ALTER TABLE console_automation_tasks ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE console_automation_audit ADD COLUMN IF NOT EXISTS task_name TEXT NOT NULL DEFAULT '';
ALTER TABLE console_automation_audit ADD COLUMN IF NOT EXISTS policy JSONB NOT NULL DEFAULT '{}';
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname='console_automation_audit_task_id_fkey' AND confdeltype='c') THEN
    ALTER TABLE console_automation_audit DROP CONSTRAINT console_automation_audit_task_id_fkey;
    ALTER TABLE console_automation_audit ADD CONSTRAINT console_automation_audit_task_id_fkey FOREIGN KEY(task_id) REFERENCES console_automation_tasks(id) ON DELETE RESTRICT;
  END IF;
EXCEPTION WHEN duplicate_object THEN NULL; END $$;`

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
	Action             string   `json:"action"`
	RequireAllHigher   bool     `json:"require_all_higher"`
	QualityCheck       bool     `json:"quality_check"`
	Endpoint           string   `json:"endpoint"`
	Stream             string   `json:"stream"`
	MaxAgeSeconds      int      `json:"max_age_seconds"`
	QualityConcurrency int      `json:"quality_concurrency"`
	// Equality margins apply only to interval uncertainty. Point estimates must
	// still be Pareto non-inferior. Zero means strict interval separation.
	LatencyTolerancePercent float64 `json:"latency_tolerance_percent"`
	SuccessTolerancePP      float64 `json:"success_tolerance_pp"`
	CacheTolerancePP        float64 `json:"cache_tolerance_pp"`
	QualityTolerancePP      float64 `json:"quality_tolerance_pp"`
}

func defaultAutomationPolicy() AutomationPolicy {
	return AutomationPolicy{Metrics: []string{"latency", "success", "cache", "quality"}, MinSuccessSamples: 100, MinCacheSamples: 50, MinLatencySamples: 50, MinQualitySamples: 20, ConfidenceLevel: .95, LatencyQuantile: .5, LatencyMinPercent: 10, LatencyMinMS: 100, SuccessMinPP: 2, CacheMinPP: 5, QualityMinPP: 5, RequireConsecutive: 3, CooldownSeconds: 1800, MaxMoves: 1, Action: "suggest", RequireAllHigher: false, QualityCheck: false, Endpoint: "/v1/responses", Stream: "true", MaxAgeSeconds: 300, QualityConcurrency: 4}
}

type AutomationTask struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Kind            string           `json:"kind"`
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
	Revision        int64            `json:"revision"`
	Archived        bool             `json:"archived,omitempty"`
	LeaseToken      string           `json:"-"`
	Owner           string           `json:"-"`
}
type automationAudit struct {
	ID       int64            `json:"id"`
	TaskID   string           `json:"task_id"`
	TaskName string           `json:"task_name"`
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
	Policy   AutomationPolicy `json:"policy"`
}

func automationPolicy(p AutomationPolicy) (AutomationPolicy, error) {
	if len(p.Metrics) == 0 {
		return p, errors.New("至少选择一个比较指标")
	}
	valid := map[string]bool{"latency": true, "success": true, "cache": true, "quality": true}
	seen := map[string]bool{}
	for _, m := range p.Metrics {
		if !valid[m] || seen[m] {
			return p, errors.New("自动化指标无效")
		}
		seen[m] = true
	}
	for _, n := range []int{p.MinSuccessSamples, p.MinCacheSamples, p.MinLatencySamples, p.MinQualitySamples} {
		if n < 1 || n > 1000000 {
			return p, errors.New("最小样本数必须为 1–1000000")
		}
	}
	for _, n := range []float64{p.LatencyMinPercent, p.SuccessMinPP, p.CacheMinPP, p.QualityMinPP, p.LatencyTolerancePercent, p.SuccessTolerancePP, p.CacheTolerancePP, p.QualityTolerancePP} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
			return p, errors.New("百分比参数必须为 0–100")
		}
	}
	if p.ConfidenceLevel < .8 || p.ConfidenceLevel > .999 || p.LatencyQuantile < .01 || p.LatencyQuantile > .99 || p.LatencyMinMS < 0 || p.LatencyMinMS > 3600000 || p.RequireConsecutive < 1 || p.RequireConsecutive > 100 || p.CooldownSeconds < 0 || p.CooldownSeconds > 2592000 || p.MaxMoves < 1 || p.MaxMoves > 100 || p.MaxAgeSeconds < 10 || p.MaxAgeSeconds > 2592000 || p.QualityConcurrency < 1 || p.QualityConcurrency > 16 {
		return p, errors.New("自动化参数超出范围")
	}
	if p.Action != "suggest" && p.Action != "apply" {
		return p, errors.New("自动化执行方式无效")
	}
	if p.Endpoint != "/v1/responses" && p.Endpoint != "/v1/messages" && p.Endpoint != "/v1/chat/completions" && p.Endpoint != "/v1beta/models" {
		return p, errors.New("请选择一个明确的请求端点")
	}
	if p.Stream != "true" && p.Stream != "false" {
		return p, errors.New("请选择流式或非流式请求")
	}
	return p, nil
}

const automationColumns = `id,name,kind,enabled,source_id,key_id,model,interval_seconds,range_name,policy,next_run,last_run,created_at,updated_at,revision,archived,lease_token,owner`

func decodeAutomationTask(row interface{ Scan(...any) error }) (AutomationTask, error) {
	var t AutomationTask
	var raw []byte
	if e := row.Scan(&t.ID, &t.Name, &t.Kind, &t.Enabled, &t.SourceID, &t.KeyID, &t.Model, &t.IntervalSeconds, &t.Range, &raw, &t.NextRun, &t.LastRun, &t.CreatedAt, &t.UpdatedAt, &t.Revision, &t.Archived, &t.LeaseToken, &t.Owner); e != nil {
		return t, e
	}
	t.Policy = defaultAutomationPolicy()
	if e := json.Unmarshal(raw, &t.Policy); e != nil {
		return t, e
	}
	return t, nil
}
func (s *controlStore) automationTasks(ctx context.Context, owner string) ([]AutomationTask, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT `+automationColumns+` FROM console_automation_tasks WHERE owner=$1 AND NOT archived ORDER BY created_at,id`, owner)
	if e != nil {
		return nil, e
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
func (s *controlStore) automationTask(ctx context.Context, owner, id string) (AutomationTask, error) {
	return decodeAutomationTask(s.db.QueryRowContext(ctx, `SELECT `+automationColumns+` FROM console_automation_tasks WHERE owner=$1 AND id=$2 AND NOT archived`, owner, id))
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func (s *Service) automations(w http.ResponseWriter, r *http.Request) {
	owner, e := s.controlUser(r)
	if e != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method == "GET" {
		if r.PathValue("id") != "" {
			s.automationAuditHandler(w, r)
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
	in := AutomationTask{Kind: "order", Policy: defaultAutomationPolicy()}
	if !decodeControlLimit(w, r, &in, 64<<10) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Model = strings.TrimSpace(in.Model)
	if in.Kind == "quality" {
		in.Model = checkModel
		in.Policy.Action = "suggest"
		in.Policy.Metrics = []string{"quality"}
		in.Policy.QualityCheck = true
		in.Policy.Endpoint = "/v1/responses"
		in.Policy.Stream = "true"
	}
	in.Policy, e = automationPolicy(in.Policy)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_, rangeErr := rangeStart(in.Range, time.Now(), time.UTC)
	if in.Name == "" || len(in.Name) > 120 || len(in.Model) > 512 || in.Model == "" || in.Model == "all" || in.KeyID == "" || in.IntervalSeconds < 10 || in.IntervalSeconds > 86400 || rangeErr != nil || in.Range == "all" || (in.Kind != "order" && in.Kind != "quality") {
		http.Error(w, "请填写名称、来源、API key、模型、有效时间范围和 10–86400 秒间隔", 400)
		return
	}
	if in.Kind == "order" && in.Model != checkModel && len(in.Policy.Metrics) == 1 && in.Policy.Metrics[0] == "quality" {
		http.Error(w, "不降智指标只适用于 gpt-6-astra", 400)
		return
	}
	src, e := s.control.source(r.Context(), in.SourceID)
	if e != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	key, e := subRawKey(src.ID, in.KeyID)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	in.KeyID = key
	// Validate the selected scope through the live catalog before saving a policy.
	if in.Enabled {
		if _, e = s.automationCatalog(r.Context(), src, in); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
	}
	id := r.PathValue("id")
	create := id == ""
	if create {
		id = randomID()
	}
	tx, e := s.control.db.BeginTx(r.Context(), nil)
	if e != nil {
		http.Error(w, "自动化任务保存失败", 503)
		return
	}
	defer tx.Rollback()
	if create {
		_, e = tx.ExecContext(r.Context(), `INSERT INTO console_automation_tasks(id,owner,name,kind,enabled,source_id,key_id,model,interval_seconds,range_name,policy,next_run) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,now())`, id, owner, in.Name, in.Kind, in.Enabled, in.SourceID, in.KeyID, in.Model, in.IntervalSeconds, in.Range, mustJSON(in.Policy))
	} else {
		var res sql.Result
		res, e = tx.ExecContext(r.Context(), `UPDATE console_automation_tasks SET name=$3,kind=$4,enabled=$5,source_id=$6,key_id=$7,model=$8,interval_seconds=$9,range_name=$10,policy=$11::jsonb,next_run=now(),revision=revision+1,updated_at=now() WHERE owner=$1 AND id=$2 AND revision=$12 AND NOT archived`, owner, id, in.Name, in.Kind, in.Enabled, in.SourceID, in.KeyID, in.Model, in.IntervalSeconds, in.Range, mustJSON(in.Policy), in.Revision)
		if e == nil {
			n, _ := res.RowsAffected()
			if n != 1 {
				http.Error(w, "任务已变化，请刷新后重新编辑", 409)
				return
			}
		}
	}
	if e == nil {
		_, e = tx.ExecContext(r.Context(), `INSERT INTO console_automation_audit(task_id,task_name,status,action,source_id,model,policy,reason) VALUES($1,$2,'configured','config',$3,$4,$5::jsonb,$6)`, id, in.Name, in.SourceID, in.Model, mustJSON(in.Policy), map[bool]string{true: "任务已保存并启用", false: "任务已保存，处于暂停状态"}[in.Enabled])
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		http.Error(w, "自动化任务保存失败", 503)
		return
	}
	data, e := s.control.automationTask(r.Context(), owner, id)
	if e != nil {
		http.Error(w, "任务已保存，读取失败，请刷新", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"task": data})
}
func (s *Service) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	owner, e := s.controlUser(r)
	if e != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	tx, e := s.control.db.BeginTx(r.Context(), nil)
	if e != nil {
		http.Error(w, "任务归档失败", 503)
		return
	}
	defer tx.Rollback()
	t, e := decodeAutomationTask(tx.QueryRowContext(r.Context(), `SELECT `+automationColumns+` FROM console_automation_tasks WHERE owner=$1 AND id=$2 AND NOT archived FOR UPDATE`, owner, r.PathValue("id")))
	if e != nil {
		http.Error(w, "任务不存在", 404)
		return
	}
	_, e = tx.ExecContext(r.Context(), `UPDATE console_automation_tasks SET archived=true,enabled=false,revision=revision+1,updated_at=now() WHERE id=$1`, t.ID)
	if e == nil {
		_, e = tx.ExecContext(r.Context(), `INSERT INTO console_automation_audit(task_id,task_name,status,action,source_id,model,reason) VALUES($1,$2,'archived','config',$3,$4,'任务已删除，保留历史审计')`, t.ID, t.Name, t.SourceID, t.Model)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		http.Error(w, "任务归档失败", 503)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Service) automationAuditHandler(w http.ResponseWriter, r *http.Request) {
	owner, e := s.controlUser(r)
	if e != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	id := r.PathValue("id")
	var before int64
	if v := r.URL.Query().Get("before"); v != "" {
		if json.Unmarshal([]byte(v), &before) != nil || before < 1 {
			http.Error(w, "无效审计游标", 400)
			return
		}
	}
	rows, e := s.control.db.QueryContext(r.Context(), `SELECT a.id,a.task_id,COALESCE(NULLIF(a.task_name,''),t.name),a.run_at,a.status,a.action,a.source_id,a.model,a.before_order,a.after_order,a.changes,a.metrics,a.reason,a.policy FROM console_automation_audit a JOIN console_automation_tasks t ON t.id=a.task_id WHERE t.owner=$1 AND ($2='' OR a.task_id=$2) AND ($3::bigint=0 OR a.id<$3) ORDER BY a.id DESC LIMIT 100`, owner, id, before)
	if e != nil {
		http.Error(w, "审计读取失败", 503)
		return
	}
	defer rows.Close()
	out := []automationAudit{}
	for rows.Next() {
		var a automationAudit
		var b, af, c, m, p []byte
		if e = rows.Scan(&a.ID, &a.TaskID, &a.TaskName, &a.RunAt, &a.Status, &a.Action, &a.SourceID, &a.Model, &b, &af, &c, &m, &a.Reason, &p); e != nil {
			http.Error(w, "审计读取失败", 503)
			return
		}
		_ = json.Unmarshal(b, &a.Before)
		_ = json.Unmarshal(af, &a.After)
		_ = json.Unmarshal(c, &a.Changes)
		_ = json.Unmarshal(m, &a.Metrics)
		_ = json.Unmarshal(p, &a.Policy)
		out = append(out, a)
	}
	if rows.Err() != nil {
		http.Error(w, "审计读取失败", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"data": out})
}

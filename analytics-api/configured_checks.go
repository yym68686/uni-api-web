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
	"strings"
	"sync"
	"time"
)

// Independent of site accounts: a configured provider already owns its upstream
// credentials. Only the gateway uses them; diagnostic requests never edit routes.
const configuredCheckSchema = `
CREATE TABLE IF NOT EXISTS console_configured_checks(
 source_id TEXT NOT NULL REFERENCES console_sources(id) ON DELETE CASCADE,
 provider TEXT NOT NULL,kind TEXT NOT NULL,model TEXT NOT NULL DEFAULT '',
 source_target TEXT NOT NULL,fingerprint TEXT NOT NULL DEFAULT '',quality_only BOOLEAN NOT NULL DEFAULT false,
 state TEXT NOT NULL DEFAULT 'queued',message TEXT NOT NULL DEFAULT '',result JSONB,
 run_id TEXT NOT NULL DEFAULT '',deadline TIMESTAMPTZ,updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(source_id,provider,kind,model));
CREATE INDEX IF NOT EXISTS console_configured_checks_pending ON console_configured_checks(updated_at) WHERE state='queued';`

type configuredCheck struct {
	Source      string          `json:"source_id"`
	Provider    string          `json:"provider"`
	Kind        string          `json:"kind"`
	Model       string          `json:"model"`
	Fingerprint string          `json:"fingerprint"`
	State       string          `json:"state"`
	Message     string          `json:"message"`
	Result      json.RawMessage `json:"result"`
	History     qualitySummary  `json:"history"`
}

func configuredProbeFingerprint(src controlSource, p configuredProvider) string {
	return tokenHash(controlTarget(src) + "\n" + p.Provider + "\n" + p.Base + "\n" + mustJSON(configuredKeyHashes(p)))
}

func (s *Service) configuredChecks(w http.ResponseWriter, r *http.Request) {
	rows, err := s.control.db.QueryContext(r.Context(), `SELECT c.source_id,c.provider,c.kind,c.model,c.fingerprint,
 CASE WHEN c.state='running' AND c.deadline<now() THEN 'interrupted' ELSE c.state END,c.message,c.result,
 COALESCE(h.total,0),COALESCE(h.successful,0),COALESCE(h.passed,0)
 FROM console_configured_checks c JOIN console_sources s ON s.id=c.source_id
 LEFT JOIN console_quality_totals h ON h.source_id=c.source_id AND h.provider=c.provider WHERE s.enabled`)
	if err != nil {
		log.Printf("configured_checks read: %v", err)
		http.Error(w, "检测记录读取失败", 503)
		return
	}
	defer rows.Close()
	data := []configuredCheck{}
	for rows.Next() {
		var c configuredCheck
		var raw []byte
		if err = rows.Scan(&c.Source, &c.Provider, &c.Kind, &c.Model, &c.Fingerprint, &c.State, &c.Message, &raw, &c.History.Total, &c.History.Successful, &c.History.Passed); err != nil {
			break
		}
		c.Result = raw
		data = append(data, c)
	}
	if err != nil || rows.Err() != nil {
		log.Printf("configured_checks scan: %v rows: %v", err, rows.Err())
		http.Error(w, "检测记录读取失败", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"data": data})
}

func (s *Service) queueConfiguredChecks(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind    string `json:"kind"`
		Targets []struct {
			Source   string   `json:"source_id"`
			Provider string   `json:"provider"`
			Models   []string `json:"models"`
		} `json:"targets"`
	}
	if !decodeControlLimit(w, r, &in, checkBatchBodyLimit) {
		return
	}
	if (in.Kind != "check" && in.Kind != "availability" && in.Kind != "quality" && in.Kind != "compaction" && in.Kind != "tool-use") || len(in.Targets) == 0 || len(in.Targets) > 500 {
		http.Error(w, "无效检测任务", 400)
		return
	}
	// Resolve credentials before opening a transaction (the source reader uses
	// the same pool). All selections are committed together or rejected together.
	sources := map[string]controlSource{}
	for _, t := range in.Targets {
		if t.Provider == "" || len(t.Provider) > 256 || strings.ContainsAny(t.Provider, "\r\n/") || len(t.Models) > 100 {
			http.Error(w, "无效渠道或模型", 400)
			return
		}
		for _, m := range t.Models {
			if strings.TrimSpace(m) == "" || len(m) > 256 {
				http.Error(w, "无效模型", 400)
				return
			}
		}
		if _, ok := sources[t.Source]; !ok {
			src, err := s.control.source(r.Context(), t.Source)
			if err != nil {
				http.Error(w, "来源不存在", 404)
				return
			}
			sources[t.Source] = src
		}
		if (in.Kind == "check" || in.Kind == "availability") && len(t.Models) == 0 {
			http.Error(w, "请选择检测模型", 400)
			return
		}
	}
	tx, err := s.control.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "检测队列暂不可用", 503)
		return
	}
	defer tx.Rollback()
	queued := int64(0)
	for _, t := range in.Targets {
		kind, models := in.Kind, t.Models
		if kind == "quality" {
			kind, models = "model", []string{checkModel}
		} else if kind == "check" {
			kind = "model"
		} else if kind == "compaction" || (kind == "tool-use" && len(models) == 0) {
			models = []string{""}
		}
		if kind == "tool-use" && len(t.Models) == 0 {
			// The dispatcher waits for availability checks, then creates one bounded
			// job per successful model. Ignore duplicate clicks while children run.
			var active bool
			if e := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM console_configured_checks WHERE source_id=$1 AND provider=$2 AND kind='tool-use' AND model<>'' AND state IN ('queued','running') AND (deadline IS NULL OR deadline>now()))`, t.Source, t.Provider).Scan(&active); e != nil {
				http.Error(w, "检测状态读取失败", 503)
				return
			}
			if active {
				continue
			}
		}
		for _, m := range models {
			res, e := tx.ExecContext(r.Context(), `INSERT INTO console_configured_checks(source_id,provider,kind,model,source_target,quality_only)
 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(source_id,provider,kind,model) DO UPDATE SET
 source_target=excluded.source_target,quality_only=excluded.quality_only,state='queued',message='',deadline=NULL,updated_at=now()
 WHERE console_configured_checks.state NOT IN ('queued','running') OR console_configured_checks.deadline<now()`, t.Source, t.Provider, kind, m, controlTarget(sources[t.Source]), in.Kind == "quality")
			if e != nil {
				http.Error(w, "检测入队失败", 503)
				return
			}
			n, _ := res.RowsAffected()
			queued += n
		}
	}
	if tx.Commit() != nil {
		http.Error(w, "检测入队失败", 503)
		return
	}
	writeJSON(w, 202, map[string]any{"queued": queued})
}

// The wrapper is private to one job, not a mutation of the shared HTTP client.
// Older gateways must pass the capability check before any billable request.
type configuredProbeTransport struct {
	base              http.RoundTripper
	provider          string
	allowUnconfigured bool
}

func (t configuredProbeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Uni-API-Provider", t.provider)
	if t.allowUnconfigured {
		r.Header.Set("X-Uni-API-Probe-Unconfigured-Model", "true")
	}
	return t.base.RoundTrip(r)
}

func (s *Service) configuredCheckLoop(ctx context.Context) {
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				if s.configuredCheckOne(ctx) {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
		}()
	}
	workers.Wait()
}

func (s *Service) configuredCheckOne(parent context.Context) bool {
	// A crashed request is never replayed automatically: it may already have
	// incurred a charge. New clicks can explicitly retry interrupted jobs.
	_, _ = s.control.db.ExecContext(parent, `UPDATE console_configured_checks SET state='interrupted',message='检测中断，请重新检测' WHERE state='running' AND deadline<now()`)
	var c configuredCheck
	var target, run string
	var qualityOnly bool
	run = randomID()
	err := s.control.db.QueryRowContext(parent, `WITH job AS (
 SELECT source_id,provider,kind,model FROM console_configured_checks c WHERE state='queued'
	AND (kind IN ('model','availability') OR NOT EXISTS(SELECT 1 FROM console_configured_checks m WHERE m.source_id=c.source_id AND m.provider=c.provider AND m.kind IN ('model','availability') AND m.state IN ('queued','running')))
 ORDER BY updated_at,source_id,provider,model FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE console_configured_checks c SET state='running',run_id=$1,deadline=now()+interval '210 seconds'
 FROM job j WHERE c.source_id=j.source_id AND c.provider=j.provider AND c.kind=j.kind AND c.model=j.model
 RETURNING c.source_id,c.provider,c.kind,c.model,c.source_target,c.quality_only`, run).Scan(&c.Source, &c.Provider, &c.Kind, &c.Model, &target, &qualityOnly)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && parent.Err() == nil {
			log.Printf("configured_checks claim: %v", err)
		}
		return false
	}
	ctx, cancel := context.WithTimeout(parent, 180*time.Second)
	defer cancel()
	var result any
	err = s.runConfiguredCheck(ctx, &c, target, run, qualityOnly, &result)
	state, message := "done", ""
	if err != nil {
		state, message = "error", err.Error()
	}
	if parent.Err() != nil {
		state, message = "interrupted", "检测中断，请重新检测"
	}
	finish, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	var raw any
	if result != nil {
		raw = mustJSON(result)
	}
	_, saveErr := s.control.db.ExecContext(finish, `UPDATE console_configured_checks SET state=$6,message=$7,result=$8,fingerprint=$9,updated_at=now(),deadline=NULL
 WHERE source_id=$1 AND provider=$2 AND kind=$3 AND model=$4 AND run_id=$5 AND state='running'`, c.Source, c.Provider, c.Kind, c.Model, run, state, message, raw, c.Fingerprint)
	if saveErr != nil {
		log.Printf("configured_checks save: %v", saveErr)
	}
	log.Printf("configured_check source=%q provider=%q kind=%s model=%q state=%s saved=%t", c.Source, c.Provider, c.Kind, c.Model, state, saveErr == nil)
	return true
}

func (s *Service) runConfiguredCheck(ctx context.Context, c *configuredCheck, target, run string, qualityOnly bool, result *any) error {
	src, err := s.control.source(ctx, c.Source)
	if err != nil || controlTarget(src) != target {
		return errors.New("来源已变更，请刷新后重新检测")
	}
	preflight, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	runtime, _, err := subGateway(preflight, src, "GET", "/v1/observability/runtime", nil)
	var caps struct {
		Targeted     bool `json:"targeted_responses"`
		Unconfigured bool `json:"targeted_unconfigured_models"`
	}
	if err != nil || decodeMap(runtime["capabilities"], &caps) != nil || !caps.Targeted {
		return errors.New("来源不支持渠道定向检测，请更新 uni-api 或检查来源权限")
	}
	providers, err := effectiveProviders(preflight, src, true)
	if err != nil {
		return errors.New("无法确认当前渠道配置，请检查来源配置读取权限")
	}
	found := false
	secrets := []string{src.Key, src.ConfigKey}
	for _, p := range providers {
		if p.Provider == c.Provider {
			c.Fingerprint = configuredProbeFingerprint(src, p)
			secrets = append(secrets, providerKeys(p.API)...)
			found = true
			break
		}
	}
	if !found {
		return errors.New("渠道已不存在，请刷新列表")
	}
	redact := func(v any) {
		raw, _ := json.Marshal(v)
		for _, secret := range secrets {
			if secret != "" {
				encoded, _ := json.Marshal(secret)
				raw = []byte(strings.ReplaceAll(string(raw), string(encoded[1:len(encoded)-1]), "[redacted]"))
			}
		}
		_ = json.Unmarshal(raw, v)
	}
	catalog, _, err := fetchSource(preflight, src, "/v1/model-channels", url.Values{"endpoint": {"all"}, "stream": {"all"}})
	var rows []struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err != nil || decodeMap(catalog["data"], &rows) != nil {
		return errors.New("渠道模型读取失败")
	}
	models := []subModelResult{}
	for _, r := range rows {
		if r.Provider == c.Provider {
			models = append(models, subModelResult{Model: r.Model, State: "done", Result: &subResult{Availability: subProbe{Status: "success"}}})
		}
	}
	candidates := subCompactionModels(models)
	client := &http.Client{Timeout: 60 * time.Second, Transport: configuredProbeTransport{base: http.DefaultTransport, provider: c.Provider, allowUnconfigured: caps.Unconfigured}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if c.Kind == "model" || c.Kind == "availability" {
		valid := false
		for _, m := range candidates {
			if m == c.Model {
				valid = true
				break
			}
		}
		if !valid && !caps.Unconfigured {
			return errors.New("来源版本不支持探测未配置模型，请更新 uni-api；该模型尚未发起上游检测")
		}
		withQuality := c.Kind == "model" && c.Model == checkModel
		if withQuality {
			if err = s.control.beginQuality(ctx, run, qualityScope{Source: c.Source, Provider: c.Provider}, 210*time.Second); err != nil {
				return errors.New("检测历史保存失败，未发起请求")
			}
		}
		out := subResult{Model: c.Model, Verdict: "not_applicable", Quality: subProbe{Status: "not_applicable", Message: "该模型不执行降智检测"}}
		if qualityOnly {
			out = subRunQualityProbe(ctx, client, src.Base, src.Key)
		} else {
			out.Availability = subProbeStream(ctx, client, src.Base, src.Key, "say test", c.Model)
			if withQuality {
				out.Verdict = "error"
				out.Quality = subProbe{Status: "skipped", Message: "可用性检测未通过"}
				if out.Availability.Status == "success" && ctx.Err() == nil {
					out.Quality = subProbeStream(ctx, client, src.Base, src.Key, checkPrompt, checkModel)
					if out.Quality.Status == "success" {
						out.Verdict = checkVerdict(out.Quality.Text)
					}
				}
			}
		}
		out.CheckedAt = time.Now().Unix()
		redact(&out)
		*result = out
		if withQuality {
			check := ChannelCheck{SourceID: c.Source, Provider: c.Provider, Model: checkModel, Verdict: out.Verdict, Text: out.Quality.Text, Message: out.Quality.Message, CheckedAt: out.CheckedAt, DurationMS: out.Quality.Duration, QualityProbe: &out.Quality}
			if err = s.control.finishQuality(run, out.Verdict, out.Quality.Status == "success", out.CheckedAt, check); err != nil {
				return errors.New("检测历史保存失败")
			}
			_, err = s.control.db.ExecContext(ctx, `INSERT INTO console_channel_checks(source_id,provider,result) VALUES($1,$2,$3) ON CONFLICT(source_id,provider) DO UPDATE SET result=excluded.result WHERE (console_channel_checks.result->>'checked_at')::BIGINT <= $4`, c.Source, c.Provider, mustJSON(check), out.CheckedAt)
			if err != nil {
				return errors.New("渠道降智结果保存失败")
			}
		}
		return nil
	}
	probeFn := subProbeCompaction
	out := subCapabilityResult{Status: "error", Attempts: []subProbe{}, Message: "没有可用模型完成检测，请查看模型检测结果"}
	// Prefer previously successful models, but a first capability check must also
	// work before model checks have ever run. Try configured models serially.
	uncertain := false
	availableModels := map[string]bool{}
	savedRows, e := s.control.db.QueryContext(ctx, `SELECT model FROM (SELECT DISTINCT ON (model) model,state,result FROM console_configured_checks WHERE source_id=$1 AND provider=$2 AND kind IN ('model','availability') AND fingerprint=$3 ORDER BY model,updated_at DESC) latest WHERE state='done' AND result->'availability'->>'status'='success'`, c.Source, c.Provider, c.Fingerprint)
	if e != nil {
		return errors.New("模型检测记录读取失败")
	}
	for savedRows.Next() {
		var model string
		if e = savedRows.Scan(&model); e != nil {
			break
		}
		availableModels[model] = true
	}
	if e == nil {
		e = savedRows.Err()
	}
	savedRows.Close()
	if e != nil {
		return errors.New("模型检测记录读取失败")
	}
	if c.Kind == "tool-use" {
		if c.Model == "" {
			return s.expandConfiguredToolChecks(ctx, c, target, run, availableModels, result)
		}
		configured := false
		for _, model := range candidates {
			configured = configured || model == c.Model
		}
		if !availableModels[c.Model] && !configured {
			return errors.New("该模型当前没有可用性检测成功记录，请先检测模型")
		}
		probe := subProbeToolUse(ctx, client, src.Base, src.Key, c.Model)
		out := subCapabilityResult{Model: c.Model, Status: probe.Status, Message: probe.Message, CheckedAt: time.Now().Unix(), Attempts: []subProbe{probe}}
		redact(&out)
		*result = out
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool { return availableModels[candidates[i]] && !availableModels[candidates[j]] })
	for _, model := range candidates {
		if ctx.Err() != nil {
			break
		}
		// A compaction-only provider may intentionally reject ordinary text
		// requests. The actual compaction probe is its availability check.
		probe := probeFn(ctx, client, src.Base, src.Key, model)
		out.Attempts = append(out.Attempts, probe)
		if probe.Status == "error" {
			uncertain = true
		}
		out.Status, out.Model, out.Message = probe.Status, model, probe.Message
		if probe.Status == "supported" {
			break
		}
	}
	if out.Status != "supported" && c.Kind == "compaction" && (uncertain || ctx.Err() != nil) {
		out.Status = "error"
		out.Message = "尚无成功压缩结果，部分模型检测失败或超时，可重新检测"
	}
	out.CheckedAt = time.Now().Unix()
	redact(&out)
	*result = out
	return nil
}

func (s *Service) expandConfiguredToolChecks(ctx context.Context, c *configuredCheck, target, run string, available map[string]bool, result *any) error {
	models := []subToolUseModel{}
	for model := range available {
		models = append(models, subToolUseModel{Model: model, State: "queued"})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Model < models[j].Model })
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Fence against cancellation/expiry and serialize a duplicate dispatch.
	var claimed string
	err = tx.QueryRowContext(ctx, `SELECT run_id FROM console_configured_checks WHERE source_id=$1 AND provider=$2 AND kind='tool-use' AND model='' AND state='running' AND run_id=$3 FOR UPDATE`, c.Source, c.Provider, run).Scan(&claimed)
	if err != nil {
		return errors.New("检测任务已变化，请重新检测")
	}
	for _, m := range models {
		_, err = tx.ExecContext(ctx, `INSERT INTO console_configured_checks(source_id,provider,kind,model,source_target,fingerprint) VALUES($1,$2,'tool-use',$3,$4,$5)
 ON CONFLICT(source_id,provider,kind,model) DO UPDATE SET state='queued',message='',source_target=excluded.source_target,
 result=CASE WHEN console_configured_checks.fingerprint=excluded.fingerprint THEN console_configured_checks.result ELSE NULL END,
 fingerprint=excluded.fingerprint,deadline=NULL,updated_at=now() WHERE console_configured_checks.state NOT IN ('queued','running') OR console_configured_checks.deadline<now()`, c.Source, c.Provider, m.Model, target, c.Fingerprint)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	out := summarizeToolUse(models)
	*result = out
	return nil
}

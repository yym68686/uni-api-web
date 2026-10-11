package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const modelFingerprintSchema = `
CREATE TABLE IF NOT EXISTS console_model_fingerprints(
 scope TEXT NOT NULL, model TEXT NOT NULL,
 source_id TEXT REFERENCES console_sources(id) ON DELETE CASCADE, provider TEXT NOT NULL DEFAULT '',
 account_id TEXT REFERENCES console_sub_accounts(id) ON DELETE CASCADE, group_id BIGINT NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'queued', result JSONB, message TEXT NOT NULL DEFAULT '',
 revision TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '', deadline TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(scope,model));
CREATE INDEX IF NOT EXISTS console_model_fingerprints_pending ON console_model_fingerprints(updated_at) WHERE state='queued';
CREATE TABLE IF NOT EXISTS console_fingerprint_worker(id BOOLEAN PRIMARY KEY CHECK(id),run_id TEXT NOT NULL DEFAULT '',deadline TIMESTAMPTZ);
INSERT INTO console_fingerprint_worker(id) VALUES(true) ON CONFLICT DO NOTHING;`

type fingerprintTarget struct {
	Source   string `json:"source_id,omitempty"`
	Provider string `json:"provider,omitempty"`
	Account  string `json:"account_id,omitempty"`
	Group    int64  `json:"group_id,omitempty"`
	Model    string `json:"model"`
}
type fingerprintRecord struct {
	fingerprintTarget
	State    string          `json:"state"`
	Message  string          `json:"message"`
	Revision string          `json:"revision"`
	Result   json.RawMessage `json:"result"`
}

func fingerprintModel(model string) bool { return model == "gpt-6-astra" || model == "gpt-6.1-sol" }
func (t fingerprintTarget) scope() string {
	return mustJSON([]any{t.Source, t.Provider, t.Account, t.Group})
}
func (t fingerprintTarget) valid() bool {
	return fingerprintModel(t.Model) && ((t.Source != "" && t.Provider != "" && len(t.Provider) <= 256 && !strings.ContainsAny(t.Provider, "\r\n/") && t.Account == "" && t.Group == 0) || (t.Source == "" && t.Provider == "" && t.Account != "" && t.Group > 0))
}

type fingerprintDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func enqueueFingerprint(ctx context.Context, db fingerprintDB, t fingerprintTarget) error {
	if !t.valid() {
		return errors.New("仅支持 gpt-6-astra 与 gpt-6.1-sol 的指纹检测")
	}
	_, err := db.ExecContext(ctx, `INSERT INTO console_model_fingerprints(scope,model,source_id,provider,account_id,group_id)
 VALUES($1,$2,NULLIF($3,''),$4,NULLIF($5,''),$6) ON CONFLICT(scope,model) DO UPDATE SET
 state='queued',message='',result=NULL,revision='',deadline=NULL,updated_at=now()
 WHERE console_model_fingerprints.state NOT IN ('queued','running') OR console_model_fingerprints.deadline<now()`, t.scope(), t.Model, t.Source, t.Provider, t.Account, t.Group)
	return err
}
func (s *Service) modelFingerprints(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	rows, err := s.control.db.QueryContext(r.Context(), `SELECT COALESCE(f.source_id,''),f.provider,COALESCE(f.account_id,''),f.group_id,f.model,
 CASE WHEN f.state='running' AND f.deadline<now() THEN 'interrupted' ELSE f.state END,f.message,f.revision,f.result,
 COALESCE(a.base,''),COALESCE(t.encrypted_key,'')
 FROM console_model_fingerprints f LEFT JOIN console_sub_accounts a ON a.id=f.account_id
 LEFT JOIN console_sub_targets t ON t.account_id=f.account_id AND t.group_id=f.group_id
 LEFT JOIN console_sources s ON s.id=f.source_id WHERE (a.owner=$1 OR s.enabled)`, owner)
	if err != nil {
		http.Error(w, "模型指纹记录读取失败", 503)
		return
	}
	defer rows.Close()
	data := []fingerprintRecord{}
	for rows.Next() {
		var f fingerprintRecord
		var base, key string
		// Queued, running and unsuccessful checks legitimately have SQL NULL
		// results. database/sql supports NULL for []byte, but not *json.RawMessage.
		var result []byte
		if err = rows.Scan(&f.Source, &f.Provider, &f.Account, &f.Group, &f.Model, &f.State, &f.Message, &f.Revision, &result, &base, &key); err != nil {
			break
		}
		f.Result = json.RawMessage(result)
		// A rotated site credential must not inherit an old identity result.
		if f.Account != "" && f.State == "done" && f.Revision != tokenHash(base+"\n"+key) {
			continue
		}
		data = append(data, f)
	}
	if err != nil || rows.Err() != nil {
		http.Error(w, "模型指纹记录读取失败", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"data": data})
}
func (s *Service) queueModelFingerprints(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Targets []fingerprintTarget `json:"targets"`
	}
	if !decodeConfiguration(w, r, &in) {
		return
	}
	if len(in.Targets) == 0 || len(in.Targets) > 512 {
		http.Error(w, "请选择 1–512 个检测项", 400)
		return
	}
	owner, _ := s.controlUser(r)
	tx, err := s.control.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "检测入队失败", 503)
		return
	}
	defer tx.Rollback()
	for _, t := range in.Targets {
		if !t.valid() {
			http.Error(w, "仅支持 gpt-6-astra 与 gpt-6.1-sol 的指纹检测", 400)
			return
		}
		var allowed bool
		if t.Account != "" {
			err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id WHERE a.owner=$1 AND a.id=$2 AND t.group_id=$3 AND t.active AND t.encrypted_key<>'')`, owner, t.Account, t.Group).Scan(&allowed)
		} else {
			err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM console_sources WHERE id=$1 AND enabled)`, t.Source).Scan(&allowed)
		}
		if err != nil {
			http.Error(w, "检测状态读取失败", 503)
			return
		}
		if !allowed {
			http.Error(w, "渠道不可用或无权访问", 404)
			return
		}
		if r.Method == "DELETE" {
			_, err = tx.ExecContext(r.Context(), `UPDATE console_model_fingerprints SET state='interrupted',message='检测已取消',deadline=NULL WHERE scope=$1 AND model=$2 AND state IN ('queued','running')`, t.scope(), t.Model)
		} else {
			err = enqueueFingerprint(r.Context(), tx, t)
		}
		if err != nil {
			http.Error(w, "检测入队失败", 503)
			return
		}
	}
	if tx.Commit() != nil {
		http.Error(w, "检测入队失败", 503)
		return
	}
	writeJSON(w, 202, map[string]bool{"accepted": true})
}

// Separate from all quality history and availability tables. One bounded worker
// across replicas prevents bulk checks from creating a fleet of scoring heaps.
func (s *Service) fingerprintLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if s.fingerprintOne(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
func (s *Service) claimFingerprint(ctx context.Context) (fingerprintTarget, string, error) {
	var t fingerprintTarget
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return t, "", err
	}
	defer tx.Rollback()
	var free bool
	err = tx.QueryRowContext(ctx, `SELECT deadline IS NULL OR deadline<now() FROM console_fingerprint_worker WHERE id=true FOR UPDATE SKIP LOCKED`).Scan(&free)
	if err != nil {
		return t, "", err
	}
	if !free {
		return t, "", sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `UPDATE console_model_fingerprints SET state='interrupted',message='检测中断，请重新检测',deadline=NULL WHERE state='running' AND deadline<now()`); err != nil {
		return t, "", err
	}
	run := randomID()
	err = tx.QueryRowContext(ctx, `WITH next AS (SELECT scope,model FROM console_model_fingerprints WHERE state='queued' ORDER BY updated_at,scope,model FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE console_model_fingerprints f SET state='running',run_id=$1,deadline=now()+interval '30 seconds'
 FROM next n WHERE f.scope=n.scope AND f.model=n.model RETURNING COALESCE(f.source_id,''),f.provider,COALESCE(f.account_id,''),f.group_id,f.model`, run).Scan(&t.Source, &t.Provider, &t.Account, &t.Group, &t.Model)
	if errors.Is(err, sql.ErrNoRows) {
		// Persist expiry classification even when there is no next queued job.
		if e := tx.Commit(); e != nil {
			return t, "", e
		}
		return t, "", err
	}
	if err != nil {
		return t, "", err
	}
	_, err = tx.ExecContext(ctx, `UPDATE console_fingerprint_worker SET run_id=$1,deadline=now()+interval '30 seconds' WHERE id=true`, run)
	if err != nil {
		return t, "", err
	}
	err = tx.Commit()
	return t, run, err
}
func (s *Service) fingerprintOne(parent context.Context) bool {
	t, run, err := s.claimFingerprint(parent)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, 780*time.Second)
	heartbeat := make(chan struct{})
	go func() {
		defer close(heartbeat)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pulse, done := context.WithTimeout(ctx, 3*time.Second)
				var alive bool
				e := s.control.db.QueryRowContext(pulse, `WITH live AS (UPDATE console_model_fingerprints SET deadline=now()+interval '30 seconds' WHERE scope=$1 AND model=$2 AND run_id=$3 AND state='running' RETURNING run_id)
 UPDATE console_fingerprint_worker SET deadline=now()+interval '30 seconds' WHERE id=true AND run_id=$3 AND EXISTS(SELECT 1 FROM live) RETURNING true`, t.scope(), t.Model, run).Scan(&alive)
				done()
				if e != nil || !alive {
					cancel()
					return
				}
			}
		}
	}()
	result, revision, err := s.runFingerprint(ctx, t)
	state, message := "done", ""
	if err != nil {
		state, message = "error", err.Error()
	}
	if ctx.Err() != nil {
		state, message = "interrupted", "检测中断或超时，未自动重发请求"
	}
	cancel()
	<-heartbeat
	finish, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	var raw any
	if len(result) > 0 {
		raw = string(result)
	}
	_, e := s.control.db.ExecContext(finish, `UPDATE console_model_fingerprints SET state=$4,message=$5,result=$6,revision=$7,deadline=NULL,updated_at=now() WHERE scope=$1 AND model=$2 AND run_id=$3 AND state='running'`, t.scope(), t.Model, run, state, message, raw, revision)
	_, _ = s.control.db.ExecContext(finish, `UPDATE console_fingerprint_worker SET deadline=NULL WHERE id=true AND run_id=$1`, run)
	log.Printf("model_fingerprint model=%q state=%s saved=%t", t.Model, state, e == nil)
	return true
}
func (s *Service) runFingerprint(ctx context.Context, t fingerprintTarget) (json.RawMessage, string, error) {
	if !t.valid() {
		return nil, "", errors.New("不支持的指纹检测模型")
	}
	config := map[string]any{"model": t.Model}
	revision := ""
	client := *subHTTP
	client.Timeout = 250 * time.Second
	if t.Account != "" {
		var base, encrypted, protocol string
		err := s.control.db.QueryRowContext(ctx, `SELECT a.base,t.encrypted_key,COALESCE(m.result->'availability'->>'protocol','responses') FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id LEFT JOIN console_sub_models m ON m.account_id=t.account_id AND m.group_id=t.group_id AND m.model=$3 WHERE t.account_id=$1 AND t.group_id=$2 AND t.active AND t.encrypted_key<>''`, t.Account, t.Group, t.Model).Scan(&base, &encrypted, &protocol)
		if err != nil {
			return nil, "", errors.New("站点渠道已不可用")
		}
		key, err := s.control.decrypt(encrypted)
		if err != nil {
			return nil, "", errors.New("测试 key 无法解密")
		}
		revision = tokenHash(base + "\n" + encrypted)
		config["base_url"], config["key"] = base, key
		if protocol == "messages" {
			config["format"] = "anthropic"
		}
	} else {
		src, err := s.control.source(ctx, t.Source)
		if err != nil {
			return nil, "", errors.New("来源已不可用")
		}
		preflight, done := context.WithTimeout(ctx, 15*time.Second)
		defer done()
		runtime, _, err := subGateway(preflight, src, "GET", "/v1/observability/runtime", nil)
		var caps struct {
			Targeted     bool `json:"targeted_responses"`
			Unconfigured bool `json:"targeted_unconfigured_models"`
		}
		if err != nil || decodeMap(runtime["capabilities"], &caps) != nil || !caps.Targeted {
			return nil, "", errors.New("来源不支持渠道定向检测")
		}
		providers, err := effectiveProviders(preflight, src, true)
		if err != nil {
			return nil, "", errors.New("无法确认渠道配置")
		}
		for _, p := range providers {
			if p.Provider == t.Provider {
				revision = configuredProbeFingerprint(src, p)
				break
			}
		}
		if revision == "" {
			return nil, "", errors.New("渠道已不存在")
		}
		config["base_url"], config["key"], config["provider"], config["allow_unconfigured"] = src.Base, src.Key, t.Provider, caps.Unconfigured
		client = http.Client{Timeout: 250 * time.Second, Transport: configuredProbeTransport{base: http.DefaultTransport, provider: t.Provider, allowUnconfigured: caps.Unconfigured}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	result, err := runFingerprintViaTransport(ctx, &client, config)
	return result, revision, err
}

type boundedFingerprintOutput struct{ bytes.Buffer }

func (b *boundedFingerprintOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errors.New("fingerprint_output_limit")
	}
	return b.Buffer.Write(p)
}
func runFingerprintProcess(ctx context.Context, config map[string]any) (json.RawMessage, error) {
	runner := filepath.Join("fingerprint", "runner.mjs")
	if dir := os.Getenv("MODEL_FINGERPRINT_DIR"); dir != "" {
		runner = filepath.Join(dir, "runner.mjs")
	}
	cmd := exec.CommandContext(ctx, "node", "--max-old-space-size=256", runner)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "NODE_NO_WARNINGS=1"}
	cmd.Stdin = strings.NewReader(mustJSON(config))
	var out boundedFingerprintOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, errors.New("指纹检测执行失败，请检查检测运行环境或稍后重试")
	}
	var result struct {
		Model, Status string
		Probability   *float64 `json:"probability"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Model != config["model"] || (result.Status != "match" && result.Status != "mismatch" && result.Status != "error" && result.Status != "insufficient") || (result.Probability != nil && (*result.Probability < 0 || *result.Probability > 1)) {
		return nil, errors.New("指纹结果无效")
	}
	return append(json.RawMessage(nil), out.Bytes()...), nil
}

// Keep upstream I/O in Go: site probes retain the existing DNS/IP restrictions,
// source probes retain targeting, and neither upstream credential reaches Node.
// The loopback bridge accepts at most three requests for one fixed model/path.
func runFingerprintViaTransport(ctx context.Context, client *http.Client, config map[string]any) (json.RawMessage, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("指纹检测传输初始化失败")
	}
	token := randomID()
	model, _ := config["model"].(string)
	base, _ := config["base_url"].(string)
	key, _ := config["key"].(string)
	path := "/v1/responses"
	if config["format"] == "anthropic" {
		path = "/v1/messages"
	}
	var requests atomic.Int32
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != path || r.Header.Get("Authorization") != "Bearer "+token && r.Header.Get("x-api-key") != token {
			http.Error(w, "invalid probe", 403)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 16385))
		if err != nil || len(raw) > 16384 {
			http.Error(w, "invalid body", 400)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(raw, &body) != nil || body.Model != model || requests.Add(1) > 3 {
			http.Error(w, "invalid probe", 400)
			return
		}
		call, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(r.Context(), cancel)
		defer stop()
		req, err := http.NewRequestWithContext(call, "POST", strings.TrimRight(base, "/")+path, bytes.NewReader(raw))
		if err != nil {
			http.Error(w, "invalid endpoint", 502)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("X-Request-ID", r.Header.Get("X-Request-ID"))
		if path == "/v1/messages" {
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		response, err := client.Do(req)
		if err != nil {
			http.Error(w, "upstream unavailable", 502)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(response.StatusCode)
		reader := io.LimitReader(response.Body, (2<<20)+1)
		buffer := make([]byte, 8192)
		for {
			n, e := reader.Read(buffer)
			if n > 0 {
				if _, err = w.Write(buffer[:n]); err != nil {
					return
				}
				_ = http.NewResponseController(w).Flush()
			}
			if e != nil {
				return
			}
		}
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	local := map[string]any{"model": model, "base_url": "http://" + listener.Addr().String(), "key": token, "format": config["format"]}
	return runFingerprintProcess(ctx, local)
}

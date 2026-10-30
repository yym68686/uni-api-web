package main

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

type fingerprintTarget struct {
	Source   string `json:"source_id,omitempty"`
	Provider string `json:"provider,omitempty"`
	Account  string `json:"account_id,omitempty"`
	Group    int64  `json:"group_id,omitempty"`
	Model    string `json:"model"`
}
type fingerprintRecord struct {
	fingerprintTarget
	State           string          `json:"state"`
	Message         string          `json:"message"`
	Revision        string          `json:"revision"`
	UpdatedAtMS     int64           `json:"updated_at_ms"`
	Result          json.RawMessage `json:"result"`
	RetainedSamples int             `json:"retained_samples,omitempty"`
}

// Shared by the UI, API and scorer so each model uses the same reference class.
//
//go:embed fingerprint/models.json
var fingerprintModelsJSON []byte

type fingerprintReference struct {
	ClassID string `json:"class_id"`
	Effort  string `json:"effort"`
}

var fingerprintModels = func() map[string]fingerprintReference {
	var models map[string]fingerprintReference
	if err := json.Unmarshal(fingerprintModelsJSON, &models); err != nil {
		panic(err)
	}
	return models
}()

func fingerprintModel(model string) bool { _, ok := fingerprintModels[model]; return ok }
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
		return errors.New("检测目标无效或模型未接入指纹参考库")
	}
	_, err := db.ExecContext(ctx, `INSERT INTO console_model_fingerprints(scope,model,source_id,provider,account_id,group_id)
 VALUES($1,$2,NULLIF($3,''),$4,NULLIF($5,''),$6) ON CONFLICT(scope,model) DO UPDATE SET
 state='queued',message='',
 result=CASE WHEN console_model_fingerprints.sample_state IS NOT NULL AND console_model_fingerprints.result->>'used_outputs' IN ('1','2') THEN console_model_fingerprints.result END,
 revision=CASE WHEN console_model_fingerprints.sample_state IS NOT NULL AND console_model_fingerprints.result->>'used_outputs' IN ('1','2') THEN console_model_fingerprints.revision ELSE '' END,
 sample_state=CASE WHEN console_model_fingerprints.result->>'used_outputs' IN ('1','2') THEN console_model_fingerprints.sample_state END,
 deadline=NULL,site_key='',updated_at=now()
 WHERE console_model_fingerprints.state NOT IN ('queued','running') OR console_model_fingerprints.deadline<now()`, t.scope(), t.Model, t.Source, t.Provider, t.Account, t.Group)
	return err
}
func (s *Service) modelFingerprints(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	data, err := readManagementFingerprintsFrom(r.Context(), s.control.db, owner)
	if err != nil {
		http.Error(w, "模型指纹记录读取失败", 503)
		return
	}
	supported := make([]string, 0, len(fingerprintModels))
	for model := range fingerprintModels {
		supported = append(supported, model)
	}
	sort.Strings(supported)
	writeJSON(w, 200, map[string]any{"data": data, "supported_models": supported})
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
			http.Error(w, "检测目标无效或模型未接入指纹参考库", 400)
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

type fingerprintPlan struct {
	client         http.Client
	config         map[string]any
	revision, site string
	sampleKey      string
}

// Resolve the actual upstream before reserving its site and sending paid probes.
func (s *Service) prepareFingerprint(ctx context.Context, t fingerprintTarget) (fingerprintPlan, error) {
	plan := fingerprintPlan{}
	if !t.valid() {
		return plan, errors.New("不支持的指纹检测模型")
	}
	config := map[string]any{"model": t.Model}
	revision := ""
	client := *subHTTP
	client.Timeout = 250 * time.Second
	if t.Account != "" {
		var base, encrypted, protocol string
		err := s.control.db.QueryRowContext(ctx, `SELECT a.base,t.encrypted_key,COALESCE(NULLIF(m.result->'availability'->>'protocol',''),$4) FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id LEFT JOIN console_sub_models m ON m.account_id=t.account_id AND m.group_id=t.group_id AND m.model=$3 WHERE t.account_id=$1 AND t.group_id=$2 AND t.active AND t.encrypted_key<>''`, t.Account, t.Group, t.Model, subModelProtocol(t.Model)).Scan(&base, &encrypted, &protocol)
		if err != nil {
			return plan, errors.New("站点渠道已不可用")
		}
		key, err := s.control.decrypt(encrypted)
		if err != nil {
			return plan, errors.New("测试 key 无法解密")
		}
		revision = tokenHash(base + "\n" + encrypted)
		// Encryption uses a fresh nonce on every normal synchronization. Bind
		// resumable evidence to the actual credential, not its changing ciphertext.
		plan.sampleKey = tokenHash(base + "\n" + key)
		plan.site = fingerprintSite(base)
		config["base_url"], config["key"] = base, key
		if protocol == "messages" {
			config["format"] = "anthropic"
		} else if protocol == "gemini" {
			config["format"] = "gemini"
		} else if protocol == "chat" {
			config["format"] = "openai"
		} else if protocol != "responses" {
			return plan, errors.New("渠道检测协议不受支持")
		}
	} else {
		src, err := s.control.source(ctx, t.Source)
		if err != nil {
			return plan, errors.New("来源已不可用")
		}
		preflight, done := context.WithTimeout(ctx, 15*time.Second)
		defer done()
		runtime, _, err := subGateway(preflight, src, "GET", "/v1/observability/runtime", nil)
		var caps struct {
			Targeted     bool `json:"targeted_responses"`
			Unconfigured bool `json:"targeted_unconfigured_models"`
		}
		if err != nil || decodeMap(runtime["capabilities"], &caps) != nil || !caps.Targeted {
			return plan, errors.New("来源不支持渠道定向检测")
		}
		providers, err := effectiveProviders(preflight, src, true)
		if err != nil {
			return plan, errors.New("无法确认渠道配置")
		}
		for _, p := range providers {
			if p.Provider == t.Provider {
				revision = configuredProbeFingerprint(src, p)
				plan.site = fingerprintSite(p.Base)
				break
			}
		}
		if revision == "" {
			return plan, errors.New("渠道已不存在")
		}
		plan.sampleKey = revision
		config["base_url"], config["key"], config["provider"], config["allow_unconfigured"] = src.Base, src.Key, t.Provider, caps.Unconfigured
		client = http.Client{Timeout: 250 * time.Second, Transport: configuredProbeTransport{base: http.DefaultTransport, provider: t.Provider, allowUnconfigured: caps.Unconfigured}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if plan.site == "" {
		return plan, errors.New("无法确认渠道上游站点")
	}
	plan.client, plan.config, plan.revision = client, config, revision
	return plan, nil
}

func runFingerprintProcess(ctx context.Context, config map[string]any) (json.RawMessage, error) {
	raw, err := sharedFingerprintWorker.detect(ctx, config)
	if err != nil {
		return nil, err
	}
	var result struct {
		Model, Status string
		Probability   *float64 `json:"probability"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Model != config["model"] || (result.Status != "match" && result.Status != "mismatch" && result.Status != "error" && result.Status != "insufficient") || (result.Probability != nil && (*result.Probability < 0 || *result.Probability > 1)) {
		return nil, errors.New("指纹结果无效")
	}
	return raw, nil
}

// Keep upstream I/O in Go: site probes retain the existing DNS/IP restrictions,
// source probes retain targeting, and neither upstream credential reaches Node.
// The loopback bridge accepts at most three requests for one fixed model/path.
func runFingerprintViaTransport(ctx context.Context, client *http.Client, config map[string]any) (json.RawMessage, error) {
	return runFingerprintViaTransportProgress(ctx, client, config, nil, nil)
}

func runFingerprintViaTransportProgress(ctx context.Context, client *http.Client, config map[string]any, resume json.RawMessage, checkpoint func(json.RawMessage, json.RawMessage) error) (json.RawMessage, error) {
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
	} else if config["format"] == "gemini" {
		path = "/v1beta/models/" + url.PathEscape(model) + ":streamGenerateContent"
	} else if config["format"] == "openai" {
		path = "/v1/chat/completions"
	}
	query := ""
	if config["format"] == "gemini" {
		query = "alt=sse"
	}
	var requests atomic.Int32
	var checkpoints atomic.Int32
	requestLimit := int32(3)
	if count, valid := validateFingerprintSamples(resume, config); valid && count < 3 {
		requestLimit -= int32(count)
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/checkpoint" && r.URL.RawQuery == "" && r.Header.Get("Authorization") == "Bearer "+token && checkpoint != nil {
			if checkpoints.Add(1) > requestLimit {
				http.Error(w, "invalid checkpoint", 400)
				return
			}
			raw, err := io.ReadAll(io.LimitReader(r.Body, fingerprintSampleBytes+1))
			public, samples, decodeErr := decodeFingerprintCheckpoint(raw, config)
			if err != nil || decodeErr != nil || checkpoint(public, samples) != nil {
				http.Error(w, "checkpoint unavailable", 503)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != "POST" || r.URL.EscapedPath() != path || r.URL.RawQuery != query || r.Header.Get("Authorization") != "Bearer "+token && r.Header.Get("x-api-key") != token && r.Header.Get("x-goog-api-key") != token {
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
		decodeErr := json.Unmarshal(raw, &body)
		validBody := decodeErr == nil && body.Model == model
		// Native Gemini selects the fixed model in the URL, never in the body.
		if config["format"] == "gemini" {
			validBody = decodeErr == nil && body.Model == "" && len(raw) > 0 && raw[0] == '{'
		}
		if !validBody || requests.Add(1) > requestLimit {
			http.Error(w, "invalid probe", 400)
			return
		}
		call, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(r.Context(), cancel)
		defer stop()
		destination := strings.TrimRight(base, "/") + path
		if query != "" {
			destination += "?" + query
		}
		req, err := http.NewRequestWithContext(call, "POST", destination, bytes.NewReader(raw))
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
		} else if config["format"] == "gemini" {
			req.Header.Set("x-goog-api-key", key)
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
	if len(resume) > 0 {
		local["resume"] = resume
	}
	local["checkpoint"] = checkpoint != nil
	raw, err := runFingerprintProcess(ctx, local)
	if err != nil {
		return nil, err
	}
	public, _, err := decodeFingerprintCheckpoint(raw, config)
	return public, err
}

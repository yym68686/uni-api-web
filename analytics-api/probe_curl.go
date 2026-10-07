package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Requests and credentials stay encrypted in the existing result JSON. The
// summary feed drops this token; only an explicit, authenticated export opens it.
type probeCurlScope struct {
	Account string `json:"account,omitempty"`
	Source  string `json:"source,omitempty"`
}
type probeCurlSnapshot struct {
	Purpose string         `json:"purpose"`
	Scope   probeCurlScope `json:"scope"`
	Method  string         `json:"method"`
	URL     string         `json:"url"`
	Headers http.Header    `json:"headers"`
	Body    string         `json:"body"`
}
type probeCurlContext struct {
	store *controlStore
	scope probeCurlScope
}
type probeCurlContextKey struct{}

func withProbeCurl(ctx context.Context, store *controlStore, scope probeCurlScope) context.Context {
	return context.WithValue(ctx, probeCurlContextKey{}, probeCurlContext{store, scope})
}

// Both the detector and legacy-request export use these builders. Never invent
// a Responses request for a Messages/Gemini probe or substitute its model alias.
func newProbeRequest(ctx context.Context, base, key, model, mode, prompt, id string) (*http.Request, error) {
	path := "/v1/responses"
	body := map[string]any{"model": model, "input": []map[string]string{{"role": "user", "content": prompt}}, "stream": true}
	switch mode {
	case "messages":
		path = "/v1/messages"
		body = map[string]any{"model": model, "max_tokens": 256, "stream": true, "messages": []map[string]string{{"role": "user", "content": prompt}}}
	case "gemini":
		path = "/v1beta/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
		body = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": prompt}}}}}
	case "compaction":
		body = compactionBody(model)
	case "tool-use":
		if err := json.Unmarshal(toolUseBody(model), &body); err != nil {
			return nil, err
		}
	case "quality-json":
		delete(body, "stream")
	case "responses":
	default:
		return nil, errors.New("unknown probe protocol")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if mode == "quality-json" {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("X-Request-ID", id)
	switch mode {
	case "messages":
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "gemini":
		req.Header.Set("x-goog-api-key", key)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}
func snapshotProbeRequest(req *http.Request, scope probeCurlScope) (probeCurlSnapshot, error) {
	out := probeCurlSnapshot{Purpose: "probe-curl-v1", Scope: scope, Method: req.Method, URL: req.URL.String(), Headers: req.Header.Clone()}
	if req.GetBody == nil {
		return out, errors.New("request body unavailable")
	}
	reader, err := req.GetBody()
	if err != nil {
		return out, err
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return out, errors.New("request body unavailable")
	}
	out.Body = string(raw)
	return out, nil
}

// Include the defaults net/http adds on the wire, without changing the live request.
func probeCurlWireRequest(client *http.Client, req *http.Request) *http.Request {
	req = req.Clone(req.Context())
	transport := client.Transport
	if targeted, ok := transport.(configuredProbeTransport); ok {
		req = targeted.prepare(req)
		transport = targeted.base
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	if _, exists := req.Header["User-Agent"]; !exists {
		req.Header.Set("User-Agent", "Go-http-client/1.1")
	}
	if transport, ok := transport.(*http.Transport); ok && !transport.DisableCompression && req.Header.Get("Accept-Encoding") == "" && req.Header.Get("Range") == "" && req.Method != "HEAD" {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	return req
}
func captureProbeCurl(ctx context.Context, client *http.Client, req *http.Request) string {
	capture, ok := ctx.Value(probeCurlContextKey{}).(probeCurlContext)
	if !ok || capture.store == nil {
		return ""
	}
	snapshot, err := snapshotProbeRequest(probeCurlWireRequest(client, req), capture.scope)
	if err == nil {
		var token string
		token, err = capture.store.encrypt(mustJSON(snapshot))
		if err == nil {
			return token
		}
	}
	// A diagnostic export failure must never alter the probe itself.
	log.Print("probe curl snapshot unavailable")
	return ""
}
func probeCurl(snapshot probeCurlSnapshot) string {
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
	parts := []string{"curl --no-buffer --compressed", "--request " + quote(snapshot.Method), "--url " + quote(snapshot.URL)}
	names := make([]string, 0, len(snapshot.Headers))
	for name := range snapshot.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, value := range snapshot.Headers[name] {
			parts = append(parts, "--header "+quote(name+": "+value))
		}
	}
	parts = append(parts, "--data-binary "+quote(snapshot.Body))
	return strings.Join(parts, " \\\n  ")
}
func (s *Service) canCopyProbe(ctx context.Context, owner string, scope probeCurlScope) bool {
	var allowed bool
	if scope.Account != "" && scope.Source == "" {
		return s.control.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND owner=$2)`, scope.Account, owner).Scan(&allowed) == nil && allowed
	}
	if scope.Source != "" && scope.Account == "" {
		return s.control.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_sources WHERE id=$1 AND enabled)`, scope.Source).Scan(&allowed) == nil && allowed
	}
	return false
}

type probeCurlTarget struct {
	Account  string `json:"account_id"`
	Group    int64  `json:"group_id"`
	Source   string `json:"source_id"`
	Provider string `json:"provider"`
}

func (s *Service) copyProbeCurl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "login required", 401)
		return
	}
	var in struct {
		Token   string            `json:"token"`
		ProbeID string            `json:"probe_id"`
		Kind    string            `json:"kind"`
		Targets []probeCurlTarget `json:"targets"`
	}
	if !decodeControlLimit(w, r, &in, 128<<10) {
		return
	}
	if in.Token != "" {
		raw, err := s.control.decrypt(in.Token)
		var snapshot probeCurlSnapshot
		if err != nil || json.Unmarshal([]byte(raw), &snapshot) != nil || snapshot.Purpose != "probe-curl-v1" || snapshot.Method != "POST" || !json.Valid([]byte(snapshot.Body)) || !s.canCopyProbe(r.Context(), owner, snapshot.Scope) {
			http.Error(w, "检测请求不存在或无权访问", 404)
			return
		}
		writeJSON(w, 200, map[string]any{"curl": probeCurl(snapshot), "original": true})
		return
	}
	if in.ProbeID == "" || len(in.ProbeID) > 256 || len(in.Targets) == 0 || len(in.Targets) > 32 || (in.Kind != "availability" && in.Kind != "quality" && in.Kind != "compaction" && in.Kind != "tool-use") {
		http.Error(w, "没有可复制的检测请求，请先完成检测", 400)
		return
	}
	for _, target := range in.Targets {
		snapshot, err := s.legacyProbeCurl(r.Context(), owner, target, in.Kind, in.ProbeID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			http.Error(w, "检测请求读取失败，请重试", 503)
			return
		}
		notice := "历史记录未保存原始请求，已按当前渠道配置和检测模板生成；URL、密钥及动态字段可能与当时不同。"
		writeJSON(w, 200, map[string]any{"curl": "# " + notice + "\n" + probeCurl(snapshot), "original": false, "message": notice})
		return
	}
	http.Error(w, "检测记录已更新或不可访问，请刷新详情后重试", 404)
}

func findLegacyProbe(raw []byte, id, kind string) (*subProbe, bool) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, false
	}
	var visit func(any) (*subProbe, bool)
	visit = func(v any) (*subProbe, bool) {
		switch item := v.(type) {
		case []any:
			for _, child := range item {
				if found, quality := visit(child); found != nil {
					return found, quality
				}
			}
		case map[string]any:
			if _, result := item["availability"]; result {
				// Quality-only Astra checks share one request between both projections.
				field := kind
				if quality, ok := item["quality"].(map[string]any); ok && kind == "availability" && quality["id"] == id {
					if availability, ok := item["availability"].(map[string]any); ok && availability["id"] == id {
						field = "quality"
					}
				}
				found, _ := visit(item[field])
				return found, field == "quality"
			}
			if item["id"] == id {
				var probe subProbe
				if json.Unmarshal([]byte(mustJSON(item)), &probe) == nil && probe.RequestedModel != "" {
					return &probe, false
				}
			}
			for _, child := range item {
				if found, quality := visit(child); found != nil {
					return found, quality
				}
			}
		}
		return nil, false
	}
	return visit(value)
}
func (s *Service) legacyProbeCurl(ctx context.Context, owner string, target probeCurlTarget, kind, id string) (probeCurlSnapshot, error) {
	var base, key string
	var raw []byte
	var err error
	scope := probeCurlScope{Account: target.Account, Source: target.Source}
	if !s.canCopyProbe(ctx, owner, scope) {
		return probeCurlSnapshot{}, sql.ErrNoRows
	}
	if target.Account != "" {
		// Identifiers are selected by this allowlist, not interpolated from input.
		value := `jsonb_build_array(t.result, (SELECT jsonb_agg(m.result) FROM console_sub_models m WHERE m.account_id=t.account_id AND m.group_id=t.group_id))`
		if kind == "compaction" {
			value = "t.compaction"
		}
		if kind == "tool-use" {
			value = "t.tool_use"
		}
		err = s.control.db.QueryRowContext(ctx, `SELECT a.base,t.encrypted_key,`+value+` FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id WHERE t.account_id=$1 AND t.group_id=$2 AND a.owner=$3`, target.Account, target.Group, owner).Scan(&base, &key, &raw)
		if err == nil {
			key, err = s.control.decrypt(key)
		}
	} else {
		kinds := []string{"model", "availability"}
		if kind == "quality" {
			kinds = []string{"model"}
		}
		if kind == "compaction" || kind == "tool-use" {
			kinds = []string{kind}
		}
		err = s.control.db.QueryRowContext(ctx, `SELECT jsonb_agg(result) FROM console_configured_checks WHERE source_id=$1 AND provider=$2 AND kind=ANY($3)`, target.Source, target.Provider, kinds).Scan(&raw)
		if err == nil {
			var src controlSource
			src, err = s.control.source(ctx, target.Source)
			base, key = src.Base, src.Key
		}
	}
	if err != nil {
		return probeCurlSnapshot{}, err
	}
	probe, qualityPrompt := findLegacyProbe(raw, id, kind)
	if probe == nil || key == "" {
		return probeCurlSnapshot{}, sql.ErrNoRows
	}
	mode, prompt := probe.Protocol, "say test"
	if mode == "" {
		mode = subModelProtocol(probe.RequestedModel)
	}
	if kind == "quality" || qualityPrompt {
		mode, prompt = "responses", checkPrompt
	}
	if kind == "compaction" || kind == "tool-use" {
		mode = kind
	}
	req, err := newProbeRequest(ctx, base, key, probe.RequestedModel, mode, prompt, "curl-"+randomID())
	if err != nil {
		return probeCurlSnapshot{}, err
	}
	if target.Source != "" {
		req = (configuredProbeTransport{provider: target.Provider, allowUnconfigured: true}).prepare(req)
	}
	return snapshotProbeRequest(probeCurlWireRequest(http.DefaultClient, req), scope)
}

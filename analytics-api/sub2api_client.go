package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// User-supplied sites must never gain access to the cluster, metadata service,
// or credentials through redirects. Resolve and validate on every connection.
func subPublicIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.Equal(net.ParseIP("100.100.100.200")) && !(&net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}).Contains(ip)
}

func subBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(raw) > 2048 {
		return "", errors.New("请输入不含用户名、查询参数的 HTTPS 站点地址")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !subPublicIP(ip) {
		return "", errors.New("请使用公网 sub2api 地址")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/api/v1", "/v1"} {
		u.Path = strings.TrimSuffix(u.Path, suffix)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func newSubHTTP() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("站点域名无法解析")
		}
		for _, ip := range ips {
			if !subPublicIP(ip.IP) {
				return nil, errors.New("不允许访问内部地址")
			}
		}
		dialer := net.Dialer{Timeout: 10 * time.Second}
		for _, ip := range ips {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			err = e
		}
		return nil, err
	}
	transport.ResponseHeaderTimeout = 25 * time.Second
	transport.MaxConnsPerHost = 8
	return &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

var subHTTP = newSubHTTP()

type subRemoteError struct {
	Status int
	Reason string
}

func (e *subRemoteError) Error() string {
	if strings.Contains(e.Reason, "CAPTCHA") || strings.Contains(e.Reason, "TURNSTILE") {
		return "站点要求人机验证；请在该站点完成登录后，使用会话令牌接入"
	}
	switch e.Status {
	case 401:
		return "站点登录已失效或账号密码不正确，请重新登录"
	case 403:
		return "站点拒绝访问，请检查账号权限、验证要求或站点防护"
	case 429:
		return "站点限流，请稍后重试"
	default:
		return fmt.Sprintf("站点接口返回 HTTP %d", e.Status)
	}
}

// Never forward upstream error messages: they may echo passwords or API keys.
func subJSON(ctx context.Context, client *http.Client, base, method, path, token string, body any, out any, idempotency string) error {
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, buf)
	if err != nil {
		return errors.New("站点地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("无法连接站点或请求超时")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return errors.New("站点响应过大或读取失败")
	}
	var envelope struct {
		Code   int             `json:"code"`
		Reason string          `json:"reason"`
		Data   json.RawMessage `json:"data"`
	}
	parseErr := json.Unmarshal(raw, &envelope)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &subRemoteError{resp.StatusCode, envelope.Reason}
	}
	if parseErr != nil || envelope.Code != 0 || len(envelope.Data) == 0 {
		return errors.New("站点返回了不兼容的 sub2api 数据")
	}
	if out != nil && json.Unmarshal(envelope.Data, out) != nil {
		return errors.New("站点返回的数据格式不兼容")
	}
	return nil
}

type subAuth struct {
	Access      string `json:"access_token"`
	Refresh     string `json:"refresh_token"`
	ExpiresIn   int    `json:"expires_in"`
	ExpiresAt   int64  `json:"expires_at"`
	Requires2FA bool   `json:"requires_2fa"`
	Temp        string `json:"temp_token"`
}
type subRemoteGroup struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Platform string  `json:"platform"`
	Rate     float64 `json:"rate_multiplier"`
	Peak     bool    `json:"peak_rate_enabled"`
}
type subRemoteKey struct {
	ID        int64      `json:"id"`
	Key       string     `json:"key"`
	Name      string     `json:"name"`
	GroupID   int64      `json:"group_id"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type subProbe struct {
	RequestedModel string `json:"requested_model,omitempty"`
	ResponseModel  string `json:"response_model,omitempty"`
	ModelMatch     string `json:"model_match,omitempty"`
	Status         string `json:"status"`
	Text           string `json:"text"`
	Message        string `json:"message,omitempty"`
	TTFT           *int64 `json:"ttft_ms"`
	Duration       int64  `json:"duration_ms"`
	HTTPStatus     int    `json:"http_status,omitempty"`
}
type subResult struct {
	Model        string   `json:"model"`
	CheckedAt    int64    `json:"checked_at"`
	Availability subProbe `json:"availability"`
	Quality      subProbe `json:"quality"`
	Verdict      string   `json:"verdict"`
}

// Only output_text deltas are first text. A stream must end with a successful
// response.completed event and final assistant text; EOF/[DONE] alone is not success.
func subProbeStream(ctx context.Context, client *http.Client, base, key, prompt string, models ...string) (out subProbe) {
	start := time.Now()
	out.Status = "error"
	defer func() { out.Duration = time.Since(start).Milliseconds() }()
	model := checkModel
	if len(models) > 0 {
		model = models[0]
	}
	out.RequestedModel = model
	out.ModelMatch = "unavailable"
	body, _ := json.Marshal(map[string]any{"model": model, "input": []map[string]string{{"role": "user", "content": prompt}}, "stream": true})
	req, _ := http.NewRequestWithContext(ctx, "POST", base+"/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Request-ID", "subcheck-"+randomID())
	resp, err := client.Do(req)
	if err != nil {
		out.Message = "连接失败或检测超时"
		return
	}
	defer resp.Body.Close()
	out.HTTPStatus = resp.StatusCode
	if resp.StatusCode != 200 {
		out.Message = fmt.Sprintf("检测请求返回 HTTP %d", resp.StatusCode)
		return
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		out.Message = "站点未返回 SSE 流式响应"
		return
	}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, (2<<20)+1))
	scanner.Buffer(make([]byte, 4096), 512<<10)
	var data strings.Builder
	var eventName string
	consume := func() bool {
		payload := strings.TrimSpace(data.String())
		data.Reset()
		if payload == "" || payload == "[DONE]" {
			eventName = ""
			return false
		}
		var event struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Response struct {
				Model  json.RawMessage `json:"model"`
				Status string          `json:"status"`
				Error  json.RawMessage `json:"error"`
				Output []struct {
					Type    string `json:"type"`
					Role    string `json:"role"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"output"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(payload), &event) != nil {
			out.Message = "流式事件格式无效"
			return true
		}
		if event.Type == "" {
			event.Type = eventName
		}
		eventName = ""
		switch event.Type {
		case "error", "response.failed", "response.incomplete":
			out.Message = "模型响应失败或未完成"
			return true
		case "response.output_text.delta":
			if event.Delta != "" {
				if out.TTFT == nil {
					ms := time.Since(start).Milliseconds()
					out.TTFT = &ms
				}
				out.Text += event.Delta
				if len(out.Text) > 8192 {
					out.Message = "检测回复超出长度限制"
					return true
				}
			}
		case "response.completed":
			if event.Response.Status != "completed" || (len(event.Response.Error) > 0 && string(event.Response.Error) != "null") {
				out.Message = "模型未完整完成"
				return true
			}
			var final strings.Builder
			for _, item := range event.Response.Output {
				if item.Type == "message" && item.Role == "assistant" {
					for _, c := range item.Content {
						if c.Type == "output_text" {
							final.WriteString(c.Text)
						}
					}
				}
			}
			text := strings.TrimSpace(final.String())
			if len(text) > 8192 || text == "" {
				out.Message = "模型未返回有效最终文本"
				return true
			}
			out.Text = text
			if out.TTFT == nil {
				out.Message = "响应完成，但缺少文本增量，无法测量首字延迟"
			}
			out.Status = "success"
			out.ResponseModel, out.ModelMatch = subCompareModel(model, event.Response.Model)
			// An untrusted endpoint may echo the credential into any string field.
			if key != "" {
				out.ResponseModel = strings.ReplaceAll(out.ResponseModel, key, "[redacted]")
			}
			return true
		}
		return false
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if consume() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(line[6:])
		}
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimPrefix(line[5:], " "))
			data.WriteByte('\n')
			if data.Len() > 512<<10 {
				out.Message = "流式事件过大"
				return
			}
		}
	}
	if data.Len() > 0 && consume() {
		return
	}
	out.Message = "流式连接中断或缺少完成事件"
	return
}

// A completed Responses object is the authoritative response body. Never use
// the requested model as a fallback or infer identity from output/reasoning text.
// Malformed model metadata does not erase a valid availability/latency result.
func subCompareModel(requested string, raw json.RawMessage) (string, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "missing"
	}
	var returned string
	if json.Unmarshal(raw, &returned) != nil || len(returned) > 512 {
		return "", "invalid"
	}
	if strings.TrimSpace(returned) == "" {
		return "", "missing"
	}
	if returned == requested {
		return returned, "match"
	}
	return returned, "mismatch"
}

var subModels = []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5", "codex-auto-review"}

func subModelAllowed(model string) bool {
	for _, m := range subModels {
		if m == model {
			return true
		}
	}
	return false
}
func subRunProbes(ctx context.Context, client *http.Client, base, key string, models ...string) subResult {
	model := checkModel
	if len(models) > 0 {
		model = models[0]
	}
	out := subResult{Model: model, Verdict: "error", Quality: subProbe{Status: "skipped", Message: "可用性检测未通过"}}
	out.Availability = subProbeStream(ctx, client, base, key, "say test", model)
	if model != checkModel {
		out.Verdict = "not_applicable"
		out.Quality = subProbe{Status: "not_applicable", Message: "该模型不执行降智检测"}
		out.CheckedAt = time.Now().Unix()
		return out
	}
	if out.Availability.Status == "success" && ctx.Err() == nil {
		out.Quality = subProbeStream(ctx, client, base, key, checkPrompt)
		if out.Quality.Status == "success" {
			out.Verdict = checkVerdict(out.Quality.Text)
		}
	}
	out.CheckedAt = time.Now().Unix()
	return out
}

func subKeyName(accountID string, groupID int64) string {
	return "uni-console-check-" + accountID + "-" + strconv.FormatInt(groupID, 10)
}

// Billing is a key-scoped snapshot: the upstream applies user overrides and its
// own timezone/peak rules. Older sites fall back only when the panel provided
// user rates and the group has no peak pricing.
type subBilling struct {
	Rate      *float64 `json:"rate"`
	Source    string   `json:"source"`
	CheckedAt int64    `json:"checked_at"`
}

func subKeyBilling(ctx context.Context, client *http.Client, base, key string, fallback subBilling) subBilling {
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, "GET", base+"/v1/sub2api/billing", nil)
	if err != nil {
		return fallback
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fallback
	}
	var doc struct {
		Object string   `json:"object"`
		Scope  string   `json:"billing_scope"`
		Rate   *float64 `json:"effective_rate_multiplier"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&doc) != nil || doc.Object != "sub2api.key_billing" || doc.Scope != "token" || doc.Rate == nil || *doc.Rate < 0 {
		return fallback
	}
	return subBilling{Rate: doc.Rate, Source: "key", CheckedAt: time.Now().Unix()}
}

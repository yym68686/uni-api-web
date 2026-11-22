package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const configuredProtocolField = "model_upstream_protocols"
const systemOneProtocol = "systemone"

// Use real upstream model names so all of its aliases share an endpoint, and
// editing an alias does not reset the routing configuration.
func configuredModelProtocols(document map[string]any, choices, resolved map[string]string) error {
	current := map[string]string{}
	if raw := document[configuredProtocolField]; raw != nil && decodeMap(raw, &current) != nil {
		return errors.New("已保存的模型端点无效")
	}
	wanted := map[string]bool{}
	for _, up := range resolved {
		wanted[up] = true
	}
	for up, protocol := range choices {
		if !wanted[up] {
			return errors.New("模型端点不属于本次选中的上游模型")
		}
		if protocol == "" {
			delete(current, up)
		} else if _, valid := protocolSpec(protocol); valid || protocol == systemOneProtocol {
			current[up] = protocol
		} else {
			return errors.New("不支持的模型上游协议")
		}
	}
	for up := range current {
		if !wanted[up] {
			delete(current, up)
		}
	}
	if len(current) == 0 {
		delete(document, configuredProtocolField)
	} else {
		document[configuredProtocolField] = current
	}
	return nil
}

// This is intentionally a preview. The browser supplies no upstream URL or
// credential; only an authorized gateway may send the pinned request.
func (s *Service) detectConfiguredProtocol(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source   string `json:"source_id"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Upstream string `json:"upstream_model"`
		Revision string `json:"revision"`
	}
	if !decodeConfiguration(w, r, &in) {
		return
	}
	if in.Source == "" || in.Provider == "" || len(in.Provider) > 256 || !validProbeModel(in.Model) || in.Revision == "" {
		http.Error(w, "请选择有效的来源、渠道和模型", 400)
		return
	}
	ctx, cancel := controlWriteContext(w, r, 42*time.Second)
	defer cancel()
	src, err := s.control.source(ctx, in.Source)
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	state, _, err := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if err != nil || state["revision"] != in.Revision {
		http.Error(w, "渠道配置已变化或暂不可读取，请重新读取后再检测", 409)
		return
	}
	if state[configuredProtocolField] != true {
		http.Error(w, "该来源尚不支持逐模型上游端点，请先更新 uni-api", 409)
		return
	}
	confirmed := []string{}
	for _, candidate := range configuredProbeOrder(in.Model, in.Upstream, true) {
		if ctx.Err() != nil {
			break
		}
		attempt, cancel := context.WithTimeout(ctx, 8*time.Second)
		result := probeConfiguredEndpoint(attempt, src, in.Provider, in.Model, candidate)
		cancel()
		if result.Status == "success" {
			confirmed = append(confirmed, candidate)
		}
	}
	preferred := ""
	if len(confirmed) > 0 {
		preferred = confirmed[0]
	}
	writeJSON(w, 200, map[string]any{"protocol": preferred, "confirmed_protocols": confirmed})
}

func configuredProbeOrder(model, upstream string, includeSystemOne bool) []string {
	protocols := []string{"responses", "chat", "messages", "gemini"}
	name := strings.ToLower(upstream)
	if name == "" {
		name = strings.ToLower(model)
	}
	if strings.Contains(name, "gemini") {
		protocols = []string{"gemini", "chat", "responses", "messages"}
	} else if strings.Contains(name, "claude") {
		protocols = []string{"messages", "chat", "responses", "gemini"}
	}
	if includeSystemOne {
		if strings.Contains(name, "jev") {
			return append([]string{systemOneProtocol}, protocols...)
		}
		protocols = append(protocols, systemOneProtocol)
	}
	return protocols
}

func discoverConfiguredProtocol(ctx context.Context, src controlSource, provider, model, upstream string) subProbe {
	// Background text-availability fallback still stops at the first match and
	// does not spend an extra structured decision request.
	for _, protocol := range configuredProbeOrder(model, upstream, false) {
		if ctx.Err() != nil {
			break
		}
		attempt, cancel := context.WithTimeout(ctx, 9*time.Second)
		probe := probeConfiguredEndpoint(attempt, src, provider, model, protocol)
		cancel()
		if probe.Status == "success" {
			return probe
		}
	}
	return subProbe{Status: "error", Message: "没有确认可用的上游端点"}
}

func probeConfiguredEndpoint(ctx context.Context, src controlSource, provider, model, protocol string) subProbe {
	client := &http.Client{
		Transport: configuredProbeTransport{
			base: http.DefaultTransport, provider: provider, allowUnconfigured: true,
			fingerprintProtocol: "endpoint-" + protocol,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	switch protocol {
	case "responses":
		return subProbeStream(ctx, client, src.Base, src.Key, "Reply with test.", model).qualityResult()
	case "chat":
		return probeConfiguredChat(ctx, client, src, model)
	case systemOneProtocol:
		return probeConfiguredSystemOne(ctx, client, src, model)
	default:
		return subProbeNativeProtocol(ctx, client, src.Base, src.Key, model, protocol)
	}
}

func probeConfiguredSystemOne(ctx context.Context, client *http.Client, src controlSource, model string) subProbe {
	out := subProbe{Protocol: systemOneProtocol, Status: "error"}
	req, err := newProbeRequest(ctx, src.Base, src.Key, model, systemOneProtocol, "This is a test.", "subcheck-"+randomID())
	if err != nil {
		return out
	}
	resp, err := client.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	out.HTTPStatus = resp.StatusCode
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		return out
	}
	var result struct {
		Error   json.RawMessage `json:"error"`
		Answers map[string]struct {
			Type string   `json:"type"`
			Noul *float64 `json:"noul"`
		} `json:"answers"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) == nil &&
		(len(result.Error) == 0 || string(result.Error) == "null") {
		if answer := result.Answers["ok"]; answer.Type == "noul" && answer.Noul != nil && *answer.Noul >= 0 && *answer.Noul <= 1 {
			out.Status = "success"
		}
	}
	return out
}

func probeConfiguredChat(ctx context.Context, client *http.Client, src controlSource, model string) (out subProbe) {
	start := time.Now()
	id := "subcheck-" + randomID()
	out = subProbe{ID: id, StartedAt: start.Unix(), Protocol: "chat", RequestedModel: model, Status: "error", Message: "Chat Completions 未返回完整的文本回复"}
	defer func() { out.Duration = time.Since(start).Milliseconds() }()
	req, err := newProbeRequest(ctx, src.Base, src.Key, model, "chat", "Reply with test.", id)
	if err != nil {
		return out
	}
	out.CurlToken = captureProbeCurl(ctx, client, req)
	resp, err := client.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	out.HTTPStatus = resp.StatusCode
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return out
	}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	var text strings.Builder
	var stopped bool
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			if stopped && strings.TrimSpace(text.String()) != "" {
				out.Status, out.Message, out.TerminalStatus = "success", "", "complete"
				out.Text = strings.ReplaceAll(text.String(), src.Key, "[redacted]")
			}
			return out
		}
		var chunk struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Index  int    `json:"index"`
				Finish string `json:"finish_reason"`
				Delta  struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || (len(chunk.Error) != 0 && string(chunk.Error) != "null") {
			return out
		}
		for _, choice := range chunk.Choices {
			if choice.Index == 0 {
				text.WriteString(choice.Delta.Content)
				if text.Len() > 8192 {
					return out
				}
				if choice.Finish != "" {
					stopped = choice.Finish == "stop"
				}
			}
		}
	}
	return out
}

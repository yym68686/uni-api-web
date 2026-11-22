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
		} else if _, valid := protocolSpec(protocol); valid {
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
	result := discoverConfiguredProtocol(ctx, src, in.Provider, in.Model, in.Upstream)
	protocol := ""
	if result.Status == "success" {
		protocol = result.Protocol
	}
	writeJSON(w, 200, map[string]string{"protocol": protocol})
}

func discoverConfiguredProtocol(ctx context.Context, src controlSource, provider, model, upstream string) subProbe {
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
	for _, protocol := range protocols {
		if ctx.Err() != nil {
			break
		}
		attempt, cancel := context.WithTimeout(ctx, 9*time.Second)
		client := &http.Client{
			Transport: configuredProbeTransport{
				base: http.DefaultTransport, provider: provider, allowUnconfigured: true,
				fingerprintProtocol: "endpoint-" + protocol,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		var probe subProbe
		switch protocol {
		case "responses":
			probe = subProbeStream(attempt, client, src.Base, src.Key, "Reply with test.", model).qualityResult()
		case "chat":
			probe = probeConfiguredChat(attempt, client, src, model)
		default:
			probe = subProbeNativeProtocol(attempt, client, src.Base, src.Key, model, protocol)
		}
		cancel()
		if probe.Status == "success" {
			return probe
		}
	}
	return subProbe{Status: "error", Message: "没有确认可用的上游端点"}
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

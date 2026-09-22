package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Same additional_tools/custom exec fixture as the September 17 investigation.
// This is a wire-protocol test only: returned code is never executed.
//
//go:embed probes/tool-use.json
var toolUsePayload []byte

func toolUseBody(model string) []byte {
	var body map[string]any
	if err := json.Unmarshal(toolUsePayload, &body); err != nil {
		panic(err)
	}
	body["model"] = model
	raw, _ := json.Marshal(body)
	return raw
}

type toolUseItem struct {
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Input     string `json:"input"`
	Arguments string `json:"arguments"`
}

// Compare protocol fields, never prose claiming the tool was called. Namespaced
// and bare tool names are accepted; an explicitly wrong namespace is not.
func (item toolUseItem) named(namespace, name string) bool {
	return (item.Namespace == "" || item.Namespace == namespace) && (item.Name == name || item.Name == namespace+"."+name)
}

func subProbeToolUse(ctx context.Context, client *http.Client, base, key, model string) (out subProbe) {
	start := time.Now()
	out = subProbe{ID: "subtools-" + randomID(), StartedAt: start.Unix(), RequestedModel: model, Protocol: "responses", Status: "error", ModelMatch: "unavailable"}
	out.RequestIDs = []string{out.ID, "local:" + out.ID}
	defer func() { out.Duration = time.Since(start).Milliseconds() }()
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/v1/responses", bytes.NewReader(toolUseBody(model)))
	if err != nil {
		out.Message = "站点地址无效"
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Request-ID", out.ID)
	resp, err := client.Do(req)
	if err != nil {
		out.Message = "连接失败或 Tool use 检测超时"
		return
	}
	defer resp.Body.Close()
	out.HTTPStatus = resp.StatusCode
	for _, header := range []string{"X-Client-Request-ID", "X-Request-ID", "Request-ID"} {
		if id := subSafeRequestID(resp.Header.Get(header), key); id != "" {
			out.addRequestIDs(id, "client:"+id, "local:"+id)
		}
	}
	if resp.StatusCode != 200 {
		out.Message = fmt.Sprintf("Tool use 请求返回 HTTP %d，无法确认工具兼容性", resp.StatusCode)
		// Only explicit protocol rejection is evidence of incompatibility. An
		// arbitrary 400/404 can also be a model, balance or routing failure.
		if resp.StatusCode == 400 || resp.StatusCode == 422 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			var problem struct {
				Error struct {
					Code    string `json:"code"`
					Param   string `json:"param"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(raw, &problem) == nil {
				e := problem.Error
				param := strings.ToLower(e.Param)
				message := strings.ToLower(e.Message)
				toolParam := param == "tools" || strings.HasPrefix(param, "tools.") || strings.Contains(param, "additional_tools") || strings.Contains(message, "additional_tools") || strings.Contains(message, "custom tool")
				rejected := e.Code == "unsupported_parameter" || e.Code == "unsupported_value" || strings.Contains(message, "not supported") || strings.Contains(message, "unsupported") || strings.Contains(message, "unknown parameter")
				if toolParam && rejected {
					out.Status = "unsupported"
					out.Message = "上游明确拒绝 additional_tools / custom 工具协议"
				}
			}
		}
		return
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		out.Message = "未返回 Tool use SSE 响应"
		return
	}
	items := map[string]toolUseItem{}
	add := func(item toolUseItem) bool {
		if item.Type != "custom_tool_call" && item.Type != "function_call" {
			return true
		}
		identity := item.CallID
		if identity != "" {
			item.ID = ""
		} // done and completed may differ only in item metadata.
		if identity == "" {
			identity = item.ID
		}
		if identity == "" {
			identity = mustJSON(item)
		}
		if previous, ok := items[identity]; ok && previous != item {
			out.Message = "工具调用事件前后不一致"
			return false
		}
		if len(items) >= 32 {
			out.Message = "工具调用数量超出检测限制"
			return false
		}
		items[identity] = item
		return true
	}
	var data strings.Builder
	var eventName string
	consume := func() bool {
		payload := strings.TrimSpace(data.String())
		data.Reset()
		name := eventName
		eventName = ""
		if payload == "" || payload == "[DONE]" {
			return false
		}
		var event struct {
			Type     string      `json:"type"`
			Item     toolUseItem `json:"item"`
			Response struct {
				ID     string          `json:"id"`
				Model  json.RawMessage `json:"model"`
				Status string          `json:"status"`
				Error  json.RawMessage `json:"error"`
				Output []toolUseItem   `json:"output"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(payload), &event) != nil {
			out.Message = "Tool use 流式事件格式无效"
			return true
		}
		if event.Type == "" {
			event.Type = name
		}
		if id := subSafeRequestID(event.Response.ID, key); id != "" {
			out.addRequestIDs(id)
		}
		switch event.Type {
		case "response.created":
			if out.ResponseCreatedMS == nil {
				ms := time.Since(start).Milliseconds()
				out.ResponseCreatedMS = &ms
			}
		case "response.output_item.done":
			if !add(event.Item) {
				return true
			}
		case "error", "response.failed", "response.incomplete":
			out.Message = "Tool use 响应失败或未完成，无法确认工具兼容性"
			return true
		case "response.completed":
			if event.Response.Status != "completed" || (len(event.Response.Error) > 0 && string(event.Response.Error) != "null") {
				out.Message = "Tool use 响应未完整完成"
				return true
			}
			for _, item := range event.Response.Output {
				if !add(item) {
					return true
				}
			}
			out.ResponseModel, out.ModelMatch = subCompareModel(model, event.Response.Model)
			if key != "" {
				out.ResponseModel = strings.ReplaceAll(out.ResponseModel, key, "[redacted]")
			}
			out.Status = "unsupported"
			out.Message = "响应已完成，但未返回指定的 custom_tool_call(exec)；本次 Tool use 探针未通过"
			for _, item := range items {
				if len(items) == 1 && item.Type == "custom_tool_call" && item.named("functions", "exec") && strings.TrimSpace(item.CallID) != "" && strings.TrimSpace(item.Input) == `text("TOOL_PROBE_OK");` {
					out.Status = "supported"
					out.Message = "已返回有效 custom_tool_call(exec) 和指定输入；未执行任何工具代码"
					out.Text = `text("TOOL_PROBE_OK");`
					return true
				}
				if item.Type == "function_call" && item.named("mcp__cua_repl", "js") {
					var args struct {
						Code string `json:"code"`
					}
					if json.Unmarshal([]byte(item.Arguments), &args) == nil && args.Code == "NO_EXEC" {
						out.Message = "已提供 exec，但响应返回 NO_EXEC；本次 Tool use 探针未通过"
						out.Text = "NO_EXEC"
					}
				}
			}
			return true
		}
		return false
	}
	limited := &io.LimitedReader{R: resp.Body, N: (2 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 512<<10)
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
				out.Message = "Tool use 事件过大"
				return
			}
		}
	}
	if scanner.Err() == nil && limited.N > 0 && data.Len() > 0 && consume() {
		return
	}
	out.Message = "Tool use 流中断、超出大小限制或缺少完成事件"
	return
}

func (s *Service) subToolUseCheck(w http.ResponseWriter, r *http.Request) {
	s.subQueueChecksKind(w, r, "tool_use")
}

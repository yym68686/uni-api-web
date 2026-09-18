package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Native protocols have no response.created. Keep their first response and
// first visible text clocks separate from the Responses-specific field.
func subProbeNative(ctx context.Context, client *http.Client, base, key, model string) (out subProbe) {
	start := time.Now()
	out = subProbe{ID: "subcheck-" + randomID(), StartedAt: start.Unix(), RequestedModel: model, Protocol: subModelProtocol(model), Status: "error", ModelMatch: "unavailable"}
	out.RequestIDs = []string{out.ID, "local:" + out.ID}
	defer func() {
		out.Duration = time.Since(start).Milliseconds()
		if key != "" {
			out.Text = strings.ReplaceAll(out.Text, key, "[redacted]")
			out.ResponseModel = strings.ReplaceAll(out.ResponseModel, key, "[redacted]")
		}
	}()
	path := "/v1/messages"
	body := map[string]any{"model": model, "max_tokens": 256, "stream": true, "messages": []map[string]string{{"role": "user", "content": "say test"}}}
	if out.Protocol == "gemini" {
		path = "/v1beta/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
		body = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": "say test"}}}}}
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewReader(raw))
	if err != nil {
		out.Message = "站点地址无效"
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Request-ID", out.ID)
	if out.Protocol == "gemini" {
		req.Header.Set("x-goog-api-key", key)
	} else {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, err := client.Do(req)
	if err != nil {
		out.Message = "连接失败或检测超时"
		return
	}
	defer resp.Body.Close()
	out.HTTPStatus = resp.StatusCode
	for _, header := range []string{"X-Client-Request-ID", "X-Request-ID", "Request-ID"} {
		if value := subSafeRequestID(resp.Header.Get(header), key); value != "" {
			out.addRequestIDs(value, "client:"+value, "local:"+value)
		}
	}
	if resp.StatusCode != http.StatusOK {
		out.Message = fmt.Sprintf("检测请求返回 HTTP %d", resp.StatusCode)
		return
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		out.Message = "站点未返回 SSE 流式响应"
		return
	}
	first := func() {
		if out.FirstResponseMS == nil {
			ms := time.Since(start).Milliseconds()
			out.FirstResponseMS = &ms
		}
	}
	appendText := func(text string) bool {
		if text != "" {
			if out.TTFT == nil {
				ms := time.Since(start).Milliseconds()
				out.TTFT = &ms
			}
			out.Text += text
		}
		if len(out.Text) > 8192 {
			out.Message = "检测回复超出长度限制"
			return false
		}
		return true
	}
	var responseModel json.RawMessage
	var started, stopped, completed bool
	textBlocks := map[int]bool{}
	var data strings.Builder
	var eventName string
	consume := func() bool {
		payload := strings.TrimSpace(data.String())
		data.Reset()
		name := eventName
		eventName = ""
		if payload == "" || payload == "[DONE]" {
			return true
		}
		if name == "error" {
			out.Message = "模型响应失败"
			return false
		}
		if out.Protocol == "gemini" {
			var event struct {
				Model    json.RawMessage `json:"modelVersion"`
				ID       string          `json:"responseId"`
				Error    json.RawMessage `json:"error"`
				Feedback struct {
					BlockReason string `json:"blockReason"`
				} `json:"promptFeedback"`
				Candidates []struct {
					Index   int    `json:"index"`
					Finish  string `json:"finishReason"`
					Content struct {
						Parts []struct {
							Text    string `json:"text"`
							Thought bool   `json:"thought"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			if json.Unmarshal([]byte(payload), &event) != nil {
				out.Message = "流式事件格式无效"
				return false
			}
			if (len(event.Error) > 0 && string(event.Error) != "null") || event.Feedback.BlockReason != "" {
				out.Message = "模型响应失败或被拦截"
				return false
			}
			if len(event.Candidates) > 0 || len(event.Model) > 0 || event.ID != "" {
				first()
				started = true
			}
			if len(event.Model) > 0 {
				responseModel = event.Model
			}
			if id := subSafeRequestID(event.ID, key); id != "" {
				out.addRequestIDs(id)
			}
			for _, candidate := range event.Candidates {
				if candidate.Index != 0 {
					continue
				}
				for _, part := range candidate.Content.Parts {
					if !part.Thought && !appendText(part.Text) {
						out.Message = "检测回复超出长度限制"
						return false
					}
				}
				if candidate.Finish != "" {
					if candidate.Finish != "STOP" {
						out.Message = "模型未完整完成"
						return false
					}
					completed = true
				}
			}
			return true
		}
		var event struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				ID    string          `json:"id"`
				Role  string          `json:"role"`
				Model json.RawMessage `json:"model"`
			} `json:"message"`
			Block struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content_block"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(payload), &event) != nil {
			out.Message = "流式事件格式无效"
			return false
		}
		if event.Type == "" {
			event.Type = name
		}
		switch event.Type {
		case "error":
			out.Message = "模型响应失败"
			return false
		case "message_start":
			if started || event.Message.Role != "assistant" {
				out.Message = "消息开始事件无效"
				return false
			}
			started = true
			first()
			responseModel = event.Message.Model
			if id := subSafeRequestID(event.Message.ID, key); id != "" {
				out.addRequestIDs(id)
			}
		case "content_block_start":
			textBlocks[event.Index] = event.Block.Type == "text"
			if textBlocks[event.Index] && !appendText(event.Block.Text) {
				return false
			}
		case "content_block_delta":
			if textBlocks[event.Index] && event.Delta.Type == "text_delta" && !appendText(event.Delta.Text) {
				return false
			}
		case "content_block_stop":
			delete(textBlocks, event.Index)
		case "message_delta":
			if event.Delta.StopReason != "" {
				if event.Delta.StopReason != "end_turn" && event.Delta.StopReason != "stop_sequence" {
					out.Message = "模型未完整完成"
					return false
				}
				stopped = true
			}
		case "message_stop":
			completed = started && stopped && len(textBlocks) == 0
		}
		return true
	}
	limited := &io.LimitedReader{R: resp.Body, N: (2 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 512<<10)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if !consume() {
				return
			}
		} else if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(line[6:])
		} else if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimPrefix(line[5:], " "))
			data.WriteByte('\n')
			if data.Len() > 512<<10 {
				out.Message = "流式事件过大"
				return
			}
		}
	}
	if data.Len() > 0 && !consume() {
		return
	}
	if scanner.Err() != nil || limited.N <= 0 || ctx.Err() != nil || !started || !completed {
		out.Message = "流式连接中断或缺少完成事件"
		return
	}
	out.Text = strings.TrimSpace(out.Text)
	if out.Text == "" {
		out.Message = "模型未返回有效最终文本"
		return
	}
	out.Status = "success"
	out.ResponseModel, out.ModelMatch = subCompareModel(model, responseModel)
	return
}

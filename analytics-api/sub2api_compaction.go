package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type subCapabilityResult struct {
	Models    []subToolUseModel `json:"models,omitempty"`
	Status    string            `json:"status"`
	Model     string            `json:"model,omitempty"`
	CheckedAt int64             `json:"checked_at"`
	Message   string            `json:"message,omitempty"`
	Attempts  []subProbe        `json:"attempts"`
}

type subToolUseModel struct {
	Model  string               `json:"model"`
	State  string               `json:"state"`
	Result *subCapabilityResult `json:"result,omitempty"`
}

type subCompaction = subCapabilityResult

func compactionBody(model string) map[string]any {
	session, installation, thread, turn := randomID(), randomID(), randomID(), randomID()
	metadata, _ := json.Marshal(map[string]any{
		"installation_id": installation, "session_id": session, "thread_id": thread, "turn_id": turn, "window_id": session,
		"request_kind": "compaction", "compaction": map[string]string{"trigger": "manual", "reason": "user_requested", "implementation": "responses_compaction_v2", "phase": "standalone_turn", "strategy": "memento"},
	})
	return map[string]any{
		"model": model, "instructions": "", "input": []any{
			map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{}},
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "Preserve this context and produce a compacted continuation state."}}},
			map[string]string{"type": "compaction_trigger"},
		},
		"tool_choice": "auto", "parallel_tool_calls": false, "reasoning": map[string]string{"effort": "medium", "context": "all_turns"},
		"store": false, "stream": true, "include": []string{"reasoning.encrypted_content"}, "prompt_cache_key": session,
		"text":            map[string]string{"verbosity": "low"},
		"client_metadata": map[string]string{"x-codex-installation-id": installation, "session_id": session, "thread_id": thread, "turn_id": turn, "x-codex-window-id": session, "x-codex-turn-metadata": string(metadata)},
	}
}

// Ordinary assistant text is not a compaction result. Require a successful
// terminal event with exactly one usable compaction item; never persist its
// opaque encrypted state or arbitrary upstream error bodies.
func subProbeCompaction(ctx context.Context, client *http.Client, base, key, model string) (out subProbe) {
	start := time.Now()
	out = subProbe{ID: "subcompact-" + randomID(), StartedAt: start.Unix(), RequestedModel: model, Protocol: "responses", Status: "error", ModelMatch: "unavailable"}
	out.RequestIDs = []string{out.ID, "local:" + out.ID}
	defer func() { out.Duration = time.Since(start).Milliseconds() }()
	raw, _ := json.Marshal(compactionBody(model))
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/v1/responses", bytes.NewReader(raw))
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
		out.Message = "连接失败或压缩检测超时"
		return
	}
	defer resp.Body.Close()
	out.HTTPStatus = resp.StatusCode
	for _, h := range []string{"X-Client-Request-ID", "X-Request-ID", "Request-ID"} {
		if id := subSafeRequestID(resp.Header.Get(h), key); id != "" {
			out.addRequestIDs(id, "client:"+id, "local:"+id)
		}
	}
	if resp.StatusCode != 200 {
		out.Message = fmt.Sprintf("压缩请求返回 HTTP %d", resp.StatusCode)
		if resp.StatusCode == 400 || resp.StatusCode == 404 || resp.StatusCode == 422 {
			out.Status = "unsupported"
		}
		return
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		out.Message = "未返回压缩 SSE 响应"
		return
	}
	limited := &io.LimitedReader{R: resp.Body, N: (4 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var data strings.Builder
	var name string
	consume := func() bool {
		payload := strings.TrimSpace(data.String())
		data.Reset()
		eventName := name
		name = ""
		if payload == "" || payload == "[DONE]" {
			return false
		}
		var event struct {
			Type  string `json:"type"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Response struct {
				ID     string          `json:"id"`
				Status string          `json:"status"`
				Model  json.RawMessage `json:"model"`
				Error  json.RawMessage `json:"error"`
				Output []struct {
					Type      string `json:"type"`
					Encrypted string `json:"encrypted_content"`
				} `json:"output"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(payload), &event) != nil {
			out.Message = "压缩事件格式无效"
			return true
		}
		if event.Type == "" {
			event.Type = eventName
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
		case "response.failed", "response.incomplete", "error":
			out.Message = "压缩响应失败或未完成"
			if strings.Contains(payload, "expected exactly one compaction output item") {
				out.Status = "unsupported"
				out.Message = "上游未生成恰好一个压缩输出项"
			}
			return true
		case "response.completed":
			if event.Response.Status != "completed" || (len(event.Response.Error) > 0 && string(event.Response.Error) != "null") {
				out.Message = "压缩响应未完整完成"
				return true
			}
			count, usable := 0, false
			for _, item := range event.Response.Output {
				if item.Type == "compaction" {
					count++
					usable = strings.TrimSpace(item.Encrypted) != ""
				}
			}
			out.Status = "unsupported"
			out.Message = fmt.Sprintf("期望 1 个有效压缩输出项，实际 %d 个（共 %d 个输出项）", count, len(event.Response.Output))
			if count == 1 && usable {
				out.Status = "supported"
				out.Message = "已返回 1 个有效压缩输出项"
			}
			out.ResponseModel, out.ModelMatch = subCompareModel(model, event.Response.Model)
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
			name = strings.TrimSpace(line[6:])
		}
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimPrefix(line[5:], " "))
			data.WriteByte('\n')
			if data.Len() > 2<<20 {
				out.Message = "压缩事件过大"
				return
			}
		}
	}
	if scanner.Err() == nil && limited.N > 0 && data.Len() > 0 && consume() {
		return
	}
	out.Message = "压缩流中断、超出大小限制或缺少完成事件"
	return
}

func subCompactionModels(models []subModelResult) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range models {
		if m.State == "done" && m.Result != nil && m.Result.Availability.Status == "success" && !seen[m.Model] {
			out = append(out, m.Model)
			seen[m.Model] = true
		}
	}
	rank := func(m string) int {
		if m == "gpt-5.6-terra" {
			return 0
		}
		if strings.HasPrefix(m, "gpt-") {
			return 1
		}
		if subModelProtocol(m) == "responses" {
			return 2
		}
		return 3
	}
	sort.SliceStable(out, func(i, j int) bool {
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

func (s *Service) subCompactionCheck(w http.ResponseWriter, r *http.Request) {
	s.subQueueChecksKind(w, r, "compaction")
}

// Account leases and the same two workers as ordinary checks bound concurrency.
// A standalone compaction job never overwrites say-test/quality results.
func (s *Service) subTestCompactions(ctx context.Context, id, base, job string) error {
	return s.subTestCapability(ctx, id, base, job, "compaction")
}

func (s *Service) subTestCapability(ctx context.Context, id, base, job, kind string) error {
	if kind == "tool_use" {
		return s.subTestAllModelTools(ctx, id, base, job)
	}
	// SQL identifiers come only from this allowlist, never request data.
	column, label, probeFn := "compaction", "压缩", subProbeCompaction
	if kind != "compaction" {
		return errors.New("无效能力检测类型")
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT group_id,encrypted_key,remote_key_id,COALESCE((SELECT jsonb_agg(jsonb_build_object('model',m.model,'state',m.state,'result',m.result)) FROM console_sub_models m WHERE m.account_id=t.account_id AND m.group_id=t.group_id),'[]'::jsonb) FROM console_sub_targets t WHERE account_id=$1 AND active AND `+column+`_state='queued'`, id)
	if err != nil {
		return err
	}
	type target struct {
		group, keyID int64
		key          string
		models       []subModelResult
	}
	var targets []target
	for rows.Next() {
		var t target
		var raw []byte
		if err = rows.Scan(&t.group, &t.key, &t.keyID, &raw); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &t.models); err != nil {
			break
		}
		targets = append(targets, t)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	// Each group tries candidates serially; at most two groups run per account.
	run := func(t target) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res, e := s.control.db.ExecContext(ctx, `UPDATE console_sub_targets SET `+column+`_state='running' WHERE account_id=$1 AND group_id=$2 AND `+column+`_state='queued' AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$3 AND state='running')`, id, t.group, job)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return nil
		}
		key, e := s.control.decrypt(t.key)
		if e != nil {
			return errors.New(label + " 检测 key 无法解密")
		}
		result := subCompaction{Status: "unsupported", Attempts: []subProbe{}}
		models := subCompactionModels(t.models)
		if len(models) == 0 {
			result.Status = "error"
			result.Message = "没有已检测可用的模型，请先运行模型检测"
		}
		for _, model := range models {
			probeCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			probe := probeFn(probeCtx, subHTTP, base, key, model)
			cancel()
			usageResult := subResult{Model: model, Availability: probe}
			if s.subQueueUsage(ctx, id, t.group, t.keyID, &usageResult) == nil {
				probe = usageResult.Availability
			}
			result.Attempts = append(result.Attempts, probe)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if probe.Status == "supported" {
				result.Status = "supported"
				result.Model = model
				result.Message = probe.Message
				break
			}
			if probe.Status == "error" {
				result.Status = "error"
			}
		}
		if result.Message == "" {
			if result.Status == "unsupported" {
				result.Message = "已尝试的可用模型均未返回有效压缩输出"
			} else {
				result.Message = "尚无成功压缩结果，部分请求失败，可重新检测"
			}
		}
		result.CheckedAt = time.Now().Unix()
		_, e = s.control.db.ExecContext(ctx, `UPDATE console_sub_targets SET `+column+`_state='done',`+column+`=$4,state=CASE WHEN state IN ('queued','running') THEN 'done' ELSE state END WHERE account_id=$1 AND group_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$3 AND state='running')`, id, t.group, job, mustJSON(result))
		if e != nil {
			return e
		}
		return nil
	}
	jobs := make(chan target)
	errs := make(chan error, len(targets))
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for t := range jobs {
				if e := run(t); e != nil {
					errs <- e
				}
			}
		}()
	}
send:
	for _, t := range targets {
		select {
		case jobs <- t:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	workers.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			return e
		}
	}
	return ctx.Err()
}

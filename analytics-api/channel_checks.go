package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const checkModel = "gpt-6-astra"
const checkPrompt = `在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）

苹果味 桃子味 西瓜味
圆形 7 9 8
五角星形 7 6 4`

type ChannelCheck struct {
	SourceID   string `json:"source_id"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Verdict    string `json:"verdict"`
	Text       string `json:"text"`
	Message    string `json:"message,omitempty"`
	CheckedAt  int64  `json:"checked_at"`
	DurationMS int64  `json:"duration_ms"`
	RequestID  string `json:"request_id,omitempty"`
}

var checkHTTP = &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// Interpret only the final assistant output. Reasoning, tool output and error
// bodies must never turn an unsuccessful check into a green pass.
func checkVerdict(text string) string {
	if strings.Contains(text, "21") {
		return "pass"
	}
	return "fail"
}
func runChannelCheck(ctx context.Context, src controlSource, provider string) ChannelCheck {
	start := time.Now()
	out := ChannelCheck{SourceID: src.ID, Provider: provider, Model: checkModel, Verdict: "error"}
	finish := func() ChannelCheck {
		out.CheckedAt = time.Now().Unix()
		out.DurationMS = time.Since(start).Milliseconds()
		return out
	}
	// Older gateways ignore unknown headers. Require an explicit capability
	// before sending a billable request so a fallback can never be mislabelled.
	req, err := http.NewRequestWithContext(ctx, "GET", src.Base+"/v1/observability/runtime", nil)
	if err != nil {
		out.Message = "来源地址无效"
		return finish()
	}
	req.Header.Set("Authorization", "Bearer "+src.Key)
	resp, err := checkHTTP.Do(req)
	if err != nil {
		out.Message = "无法确认来源的渠道定向能力"
		return finish()
	}
	var runtime struct {
		Capabilities struct {
			Targeted bool `json:"targeted_responses"`
		} `json:"capabilities"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&runtime)
	resp.Body.Close()
	if resp.StatusCode != 200 || decodeErr != nil || !runtime.Capabilities.Targeted {
		out.Message = "来源版本不支持渠道定向检测，请更新 uni-api"
		return finish()
	}
	body, _ := json.Marshal(map[string]any{"model": checkModel, "input": []map[string]string{{"role": "user", "content": checkPrompt}}})
	req, err = http.NewRequestWithContext(ctx, "POST", src.Base+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		out.Message = "来源地址无效"
		return finish()
	}
	req.Header.Set("Authorization", "Bearer "+src.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Uni-API-Provider", provider)
	out.RequestID = "check-" + randomID()
	req.Header.Set("X-Request-ID", out.RequestID)
	resp, err = checkHTTP.Do(req)
	if err != nil {
		out.Message = "检测请求失败或超时，请稍后重试"
		return finish()
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		out.Message = fmt.Sprintf("检测请求失败（HTTP %d）", resp.StatusCode)
		return finish()
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		out.Message = "检测响应无效或超过大小限制"
		return finish()
	}
	var response struct {
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
	}
	if json.Unmarshal(raw, &response) != nil || response.Status != "completed" || (len(response.Error) > 0 && string(response.Error) != "null") {
		out.Message = "模型未返回完整的成功响应"
		return finish()
	}
	texts := []string{}
	for _, item := range response.Output {
		if item.Type == "message" && item.Role == "assistant" {
			for _, part := range item.Content {
				if part.Type == "output_text" {
					texts = append(texts, part.Text)
				}
			}
		}
	}
	out.Text = strings.TrimSpace(strings.Join(texts, "\n"))
	if len(out.Text) > 8192 {
		out.Text = ""
		out.Message = "模型回复过长，无法判定"
		return finish()
	}
	out.Verdict = checkVerdict(out.Text)
	return finish()
}

func (s *Service) channelChecks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rows, err := s.control.db.QueryContext(r.Context(), `SELECT c.result FROM console_channel_checks c JOIN console_sources s ON s.id=c.source_id WHERE s.enabled AND ($1='all' OR c.source_id=$1) ORDER BY c.source_id,c.provider`, id)
	if err != nil {
		http.Error(w, "检测记录暂不可用", 503)
		return
	}
	defer rows.Close()
	data := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			http.Error(w, "检测记录暂不可用", 503)
			return
		}
		data = append(data, json.RawMessage(raw))
	}
	if rows.Err() != nil {
		http.Error(w, "检测记录暂不可用", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"data": data})
}
func (s *Service) checkChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider string `json:"provider"`
	}
	if !decodeControl(w, r, &in) {
		return
	}
	if in.Provider == "" || len(in.Provider) > 256 || strings.ContainsAny(in.Provider, "\r\n") {
		http.Error(w, "无效渠道", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(55 * time.Second))
	src, err := s.control.source(ctx, r.PathValue("id"))
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	// A short-lived database lease prevents duplicate checks across replicas
	// without holding a database connection during the upstream request.
	runID := randomID()
	var claimed string
	err = s.control.db.QueryRowContext(ctx, `INSERT INTO console_channel_check_runs(source_id,provider,run_id,expires_at) VALUES($1,$2,$3,now()+interval '70 seconds') ON CONFLICT(source_id,provider) DO UPDATE SET run_id=excluded.run_id,expires_at=excluded.expires_at WHERE console_channel_check_runs.expires_at < now() RETURNING run_id`, src.ID, in.Provider, runID).Scan(&claimed)
	if err == sql.ErrNoRows {
		http.Error(w, "该渠道已有检测进行中", 409)
		return
	}
	if err != nil {
		http.Error(w, "检测记录暂不可用", 503)
		return
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = s.control.db.ExecContext(cleanup, `DELETE FROM console_channel_check_runs WHERE source_id=$1 AND provider=$2 AND run_id=$3`, src.ID, in.Provider, runID)
	}()
	result := runChannelCheck(ctx, src, in.Provider)
	if ctx.Err() != nil {
		http.Error(w, "检测超时或取消", 504)
		return
	}
	raw, _ := json.Marshal(result)
	_, err = s.control.db.ExecContext(ctx, `INSERT INTO console_channel_checks(source_id,provider,result) VALUES($1,$2,$3) ON CONFLICT(source_id,provider) DO UPDATE SET result=excluded.result`, src.ID, in.Provider, string(raw))
	if err != nil {
		http.Error(w, "保存检测结果失败", 503)
		return
	}
	writeJSON(w, 200, result)
}

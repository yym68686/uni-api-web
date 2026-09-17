package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// The account service only forwards this narrow runtime-control endpoint.
// Credentials and persistent configuration never enter the browser.
func (s *Service) channelControls(w http.ResponseWriter, r *http.Request) {
	src, err := s.control.source(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "请选择一个有效的 uni-api 来源", 404)
		return
	}
	var body []byte
	if r.Method == http.MethodPost {
		var input struct {
			Revision string   `json:"revision"`
			Action   string   `json:"action"`
			KeyID    string   `json:"api_key_id"`
			Model    string   `json:"model"`
			Order    []string `json:"order"`
			Disabled []string `json:"disabled"`
		}
		if !decodeControl(w, r, &input) {
			return
		}
		if len(input.Revision) > 256 || len(input.KeyID) > 256 || len(input.Model) > 512 || len(input.Order) > 1024 || len(input.Disabled) > 1024 || (input.Action != "set" && input.Action != "reset" && input.Action != "reset_all") {
			http.Error(w, "无效的临时控制参数", 400)
			return
		}
		if parts := strings.SplitN(input.KeyID, "::", 2); len(parts) == 2 {
			if parts[0] != src.ID {
				http.Error(w, "API key 不属于所选来源", 400)
				return
			}
			input.KeyID = parts[1]
		}
		body, _ = json.Marshal(input)
	}
	if r.Method == http.MethodPost {
		unlock, lockErr := s.control.lockControls(r.Context(), src.ID)
		if lockErr != nil {
			http.Error(w, lockErr.Error(), 409)
			return
		}
		defer unlock()
		if _, e := s.reconcileControls(r.Context(), src); e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, src.Base+"/v1/channel-controls", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "来源地址无效", 502)
		return
	}
	req.Header.Set("Authorization", "Bearer "+src.Key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := sourceHTTP.Do(req)
	if err != nil {
		http.Error(w, "无法确认来源状态，请刷新后核对；请勿直接重复提交", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		switch resp.StatusCode {
		case 404:
			http.Error(w, "该来源尚不支持临时控制，请更新 uni-api", 404)
		case 409:
			http.Error(w, "临时规则或配置已变化，或实例已重启。请刷新当前规则后重新编辑", 409)
		case 400:
			http.Error(w, "所选渠道或 API key 已失效，请刷新后重试", 400)
		case 401, 403:
			http.Error(w, "来源密钥没有临时控制权限，请在来源设置中配置首个密钥或管理员密钥", 403)
		default:
			http.Error(w, "来源未完成修改，请刷新后核对当前规则", 502)
		}
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 || !json.Valid(raw) {
		http.Error(w, "来源返回无效状态，请刷新后核对", 502)
		return
	}
	var observed map[string]any
	if json.Unmarshal(raw, &observed) == nil {
		if r.Method == http.MethodPost {
			if e := s.saveLiveControls(r.Context(), src, observed); e != nil {
				retentionFailure(w, e)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(raw)
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type draftModelDiscovery struct {
	Base   string `json:"base_url"`
	API    string `json:"api"`
	Engine string `json:"engine"`
}

// A new channel has no gateway provider yet. Read its catalog without creating
// a provider, changing routes, or persisting the supplied credential.
func (s *Service) channelDraftModels(w http.ResponseWriter, r *http.Request) {
	var in draftModelDiscovery
	if !decodeConfiguration(w, r, &in) {
		return
	}
	if _, err := s.control.source(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, "来源不存在", http.StatusNotFound)
		return
	}
	models, endpoint, err := discoverDraftModels(r.Context(), subHTTP, in)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"models": models, "endpoint": endpoint})
}

func draftModelsURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("请输入不含用户名、查询参数或片段的上游 HTTP(S) 地址")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !subPublicIP(ip) {
		return "", errors.New("获取模型需要公网地址")
	}
	path := strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/responses/compact", "/chat/completions", "/responses", "/messages", "/embeddings", "/moderations", "/images/generations", "/images/edits", "/audio/speech", "/audio/transcriptions", "/audio/translations", "/systemone"} {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimSuffix(path, suffix)
			break
		}
	}
	if !strings.HasSuffix(path, "/v1/models") {
		if strings.HasSuffix(path, "/v1") {
			path += "/models"
		} else {
			path += "/v1/models"
		}
	}
	u.Path, u.RawPath = path, ""
	return u.String(), nil
}

func discoverDraftModels(ctx context.Context, client *http.Client, in draftModelDiscovery) ([]string, string, error) {
	endpoint, err := draftModelsURL(in.Base)
	if err != nil {
		return nil, "", err
	}
	key := strings.TrimSpace(in.API)
	if strings.ContainsAny(key, "\r\n") {
		return nil, "", errors.New("上游 API key 格式无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", errors.New("上游地址无效")
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		switch in.Engine {
		case "claude", "vertex-claude":
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		case "azure":
			req.Header.Set("api-key", key)
		case "gemini", "vertex-gemini":
			req.Header.Set("x-goog-api-key", key)
		default:
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	// Credentials must never follow a redirect, including same-host redirects.
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := safeClient.Do(req)
	if err != nil {
		return nil, "", errors.New("无法获取模型，请检查上游地址和网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		switch resp.StatusCode {
		case 401, 403:
			return nil, "", fmt.Errorf("上游拒绝访问模型列表（HTTP %d），请检查上游 API key 权限", resp.StatusCode)
		case 404:
			return nil, "", errors.New("上游未提供 /v1/models，请核对地址或手动添加模型")
		default:
			return nil, "", fmt.Errorf("获取模型失败（HTTP %d）", resp.StatusCode)
		}
	}
	raw, err := readConfiguration(resp.Body)
	if err != nil {
		return nil, "", errors.New("模型列表读取失败")
	}
	var data struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return nil, "", errors.New("上游返回了无效的模型列表")
	}
	items := data.Data
	if items == nil {
		items = data.Models
	}
	if items == nil {
		return nil, "", errors.New("上游返回了无效的模型列表")
	}
	models := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		var model string
		if json.Unmarshal(item, &model) != nil {
			var obj struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if json.Unmarshal(item, &obj) != nil {
				continue
			}
			model = obj.ID
			if model == "" {
				model = obj.Name
			}
		}
		model = strings.TrimSpace(model)
		if model == "" || strings.ContainsAny(model, "\r\n\t") || seen[model] {
			continue
		}
		seen[model] = true
		models = append(models, model)
	}
	return models, endpoint, nil
}

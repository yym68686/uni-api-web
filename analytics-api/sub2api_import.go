package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type subImportInput struct {
	Action    string   `json:"action"`
	AccountID string   `json:"account_id"`
	GroupID   int64    `json:"group_id"`
	SourceID  string   `json:"source_id"`
	KeyID     string   `json:"api_key_id"`
	Revision  string   `json:"revision"`
	Models    []string `json:"models"`
	Position  int      `json:"position"`
}

func subProviderName(account string, group int64, key string) string {
	return "sub2api-" + tokenHash(account + ":" + strconv.FormatInt(group, 10) + ":" + key)[:24]
}
func subRawKey(source, key string) (string, error) {
	if parts := strings.SplitN(key, "::", 2); len(parts) == 2 {
		if parts[0] != source {
			return "", errors.New("API key 不属于所选来源")
		}
		key = parts[1]
	}
	if !strings.HasPrefix(key, "key-") || len(key) != 68 {
		return "", errors.New("请选择有效 API key")
	}
	return key, nil
}
func subGateway(ctx context.Context, src controlSource, method, path string, body any) (map[string]any, int, error) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, src.Base+path, bytes.NewReader(raw))
	if err != nil {
		return nil, 502, errors.New("来源地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+src.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := sourceHTTP.Do(req)
	if err != nil {
		return nil, 502, errors.New("无法确认来源结果，请刷新后核对，勿重复提交")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		switch resp.StatusCode {
		case 404:
			return nil, 404, errors.New("来源尚不支持临时添加渠道，请更新 uni-api")
		case 409:
			return nil, 409, errors.New("渠道顺序已变化或来源已重启，请刷新弹窗后重试")
		case 401, 403:
			return nil, 403, errors.New("来源密钥无管理权限")
		case 400:
			return nil, 400, errors.New("所选 key、模型或位置已失效，请刷新弹窗后重试")
		}
		return nil, 502, errors.New("来源未完成添加，请刷新后核对")
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	var data map[string]any
	if err != nil || len(raw) > 2<<20 || json.Unmarshal(raw, &data) != nil {
		return nil, 502, errors.New("来源返回无效状态，请刷新核对")
	}
	return data, 200, nil
}
func (s *Service) subChannelOptions(w http.ResponseWriter, r *http.Request) {
	src, err := s.control.source(r.Context(), r.URL.Query().Get("source_id"))
	if err != nil {
		http.Error(w, "请选择 uni-api 来源", 404)
		return
	}
	state, status, err := subGateway(r.Context(), src, "GET", "/v1/channel-controls", nil)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	keys, status, err := fetchSource(r.Context(), src, "/v1/api-keys", nil)
	if err != nil {
		http.Error(w, "无法读取来源 API key", status)
		return
	}
	channels := []any{}
	if key := r.URL.Query().Get("api_key_id"); key != "" {
		key, err = subRawKey(src.ID, key)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		catalog, code, e := fetchSource(r.Context(), src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
		if e != nil {
			http.Error(w, "无法读取当前渠道顺序", code)
			return
		}
		if data, ok := catalog["data"].([]any); ok {
			channels = data
		}
	}
	writeJSON(w, 200, map[string]any{"revision": state["revision"], "supported": state["temporary_channel_import"] == true, "manageable": state["temporary_channel_management"] == true, "keys": keys["data"], "channels": channels, "provider": subProviderName(r.URL.Query().Get("account_id"), int64Param(r.URL.Query().Get("group_id")), strings.TrimPrefix(r.URL.Query().Get("api_key_id"), src.ID+"::"))})
}
func (s *Service) subImportChannel(w http.ResponseWriter, r *http.Request) {
	var in subImportInput
	if !decodeControl(w, r, &in) {
		return
	}
	owner, _ := s.controlUser(r)
	if len(in.Models) == 0 || len(in.Models) > len(subModels) || in.Position < 1 || in.Position > 1025 || len(in.Revision) > 256 {
		http.Error(w, "请选择模型和有效位置", 400)
		return
	}
	seen := map[string]bool{}
	for _, model := range in.Models {
		if !subModelAllowed(model) || seen[model] {
			http.Error(w, "模型无效或重复", 400)
			return
		}
		seen[model] = true
	}
	key, err := subRawKey(in.SourceID, in.KeyID)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(50 * time.Second))
	src, err := s.control.source(ctx, in.SourceID)
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	unlock, err := s.control.lockControls(ctx, src.ID)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	defer unlock()
	state, err := s.reconcileControls(ctx, src)
	status := 409
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if state["temporary_channel_import"] != true {
		http.Error(w, "该来源尚不支持临时添加渠道", 400)
		return
	}
	if state["revision"] != in.Revision {
		http.Error(w, "渠道顺序已变化，请刷新弹窗后重试", 409)
		return
	}
	// Claim account to serialize refresh-token rotation, key creation and stop.
	job := randomID()
	var base, auth string
	err = s.control.db.QueryRowContext(ctx, `UPDATE console_sub_accounts SET state='running',job_id=$3,job_kind='import',lease_until=now()+interval '90 seconds',message='' WHERE id=$1 AND owner=$2 AND state NOT IN ('running','queued') RETURNING base,encrypted_auth`, in.AccountID, owner, job).Scan(&base, &auth)
	if err != nil {
		http.Error(w, "账号不存在或有任务进行中", 409)
		return
	}
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_, _ = s.control.db.ExecContext(cleanup, `UPDATE console_sub_accounts SET state='idle',job_id='',job_kind='',lease_until=NULL WHERE id=$1 AND job_id=$2`, in.AccountID, job)
	}()
	var active bool
	var storedKey string
	err = s.control.db.QueryRowContext(ctx, `SELECT active,encrypted_routing_key FROM console_sub_targets WHERE account_id=$1 AND group_id=$2`, in.AccountID, in.GroupID).Scan(&active, &storedKey)
	if err != nil || !active {
		http.Error(w, "分组不可用，请同步后重试", 400)
		return
	}
	for _, model := range in.Models {
		var success bool
		err = s.control.db.QueryRowContext(ctx, `SELECT state='done' AND result->'availability'->>'status'='success' FROM console_sub_models WHERE account_id=$1 AND group_id=$2 AND model=$3`, in.AccountID, in.GroupID, model).Scan(&success)
		if err != nil || !success {
			http.Error(w, "只能添加最近检测可用的模型，请重新检测", 400)
			return
		}
	}
	// A separate business key avoids exhausting the $1 test key in live traffic.
	call, groups, err := s.subPanel(ctx, in.AccountID, base, job, auth)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	allowed := false
	for _, g := range groups {
		if g.ID == in.GroupID {
			allowed = true
		}
	}
	if !allowed {
		http.Error(w, "该账号已无分组权限", 400)
		return
	}
	routeName := "uni-console-route-" + in.AccountID + "-" + strconv.FormatInt(in.GroupID, 10)
	var routeKey subRemoteKey
	if storedKey != "" {
		// Read the dedicated upstream record, so revoked/disabled keys cannot be silently imported.
		var remoteID int64
		s.control.db.QueryRowContext(ctx, `SELECT routing_key_id FROM console_sub_targets WHERE account_id=$1 AND group_id=$2`, in.AccountID, in.GroupID).Scan(&remoteID)
		err = call("GET", "/api/v1/keys/"+strconv.FormatInt(remoteID, 10), nil, &routeKey, "")
	} else {
		for page := 1; page <= 100; page++ {
			var listing struct {
				Items []subRemoteKey `json:"items"`
				Pages int            `json:"pages"`
			}
			err = call("GET", "/api/v1/keys?search="+url.QueryEscape(routeName)+"&page_size=100&page="+strconv.Itoa(page), nil, &listing, "")
			if err != nil {
				break
			}
			for _, k := range listing.Items {
				if k.Name == routeName && k.GroupID == in.GroupID {
					routeKey = k
					break
				}
			}
			if routeKey.ID > 0 || len(listing.Items) < 100 || (listing.Pages > 0 && page >= listing.Pages) {
				break
			}
			if page == 100 {
				err = errors.New("业务 key 列表未读取完整")
			}
		}
		if err == nil && routeKey.ID == 0 {
			err = call("POST", "/api/v1/keys", map[string]any{"name": routeName, "group_id": in.GroupID}, &routeKey, "route-"+in.AccountID+"-"+strconv.FormatInt(in.GroupID, 10))
		}
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if routeKey.Key == "" || routeKey.GroupID != in.GroupID || routeKey.Name != routeName || routeKey.Status != "active" || (routeKey.ExpiresAt != nil && routeKey.ExpiresAt.Before(time.Now())) {
		http.Error(w, "专用业务 key 已失效，请在上游恢复后重试", 400)
		return
	}
	encrypted, err := s.control.encrypt(routeKey.Key)
	if err != nil {
		http.Error(w, "业务 key 保存失败", 503)
		return
	}
	result, err := s.control.db.ExecContext(ctx, `UPDATE console_sub_targets SET encrypted_routing_key=$3,routing_key_id=$4 WHERE account_id=$1 AND group_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$5)`, in.AccountID, in.GroupID, encrypted, routeKey.ID, job)
	if err != nil {
		http.Error(w, "业务 key 保存失败", 503)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		http.Error(w, "操作已停止，请刷新核对", 409)
		return
	}
	provider := subProviderName(in.AccountID, in.GroupID, key)
	applied, status, err := subGateway(ctx, src, "POST", "/v1/temporary-channels", map[string]any{"revision": in.Revision, "api_key_id": key, "provider": provider, "base_url": base + "/v1/responses", "api_key": routeKey.Key, "models": in.Models, "position": in.Position})
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if e := s.saveLiveControls(ctx, src, applied); e != nil {
		retentionFailure(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"provider": provider, "models": in.Models, "position": in.Position, "message": fmt.Sprintf("已临时添加至第 %d 位", in.Position)})
}

func int64Param(v string) int64 { n, _ := strconv.ParseInt(v, 10, 64); return n }

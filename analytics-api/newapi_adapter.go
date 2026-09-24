package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type newAPIGroup struct {
	Ratio json.RawMessage `json:"ratio"`
	Desc  string          `json:"desc"`
}
type newAPIToken struct {
	ID        int64   `json:"id"`
	UserID    int64   `json:"user_id"`
	Name      string  `json:"name"`
	Key       string  `json:"key"`
	Group     string  `json:"group"`
	Status    int     `json:"status"`
	Unlimited bool    `json:"unlimited_quota"`
	Remain    float64 `json:"remain_quota"`
	Used      float64 `json:"used_quota"`
	Created   int64   `json:"created_time"`
	Expires   int64   `json:"expired_time"`
}

func (s *Service) newAPIGroupID(ctx context.Context, account, remote string) (int64, error) {
	var id int64
	if remote == "" || len(remote) > 256 {
		return 0, errors.New("站点分组标识无效")
	}
	err := s.control.db.QueryRowContext(ctx, `INSERT INTO console_site_groups(account_id,remote_key) VALUES($1,$2) ON CONFLICT(account_id,remote_key) DO UPDATE SET remote_key=excluded.remote_key RETURNING id`, account, remote).Scan(&id)
	return id, err
}
func (s *Service) newAPIGroups(ctx context.Context, account, base string) ([]subRemoteGroup, error) {
	var remote map[string]newAPIGroup
	if err := s.newAPICall(ctx, account, base, "GET", "/api/user/self/groups", nil, &remote); err != nil {
		return nil, err
	}
	if remote == nil || len(remote) > 500 {
		return nil, errors.New("站点分组列表无效")
	}
	names := make([]string, 0, len(remote))
	for name := range remote {
		names = append(names, name)
	}
	sort.Strings(names)
	groups := []subRemoteGroup{}
	for _, name := range names {
		id, err := s.newAPIGroupID(ctx, account, name)
		if err != nil {
			return nil, err
		}
		var rate float64
		e := json.Unmarshal(remote[name].Ratio, &rate)
		unknown := e != nil || rate < 0 || math.IsInf(rate, 0) || math.IsNaN(rate) || name == "auto"
		if unknown {
			rate = 1
		}
		groups = append(groups, subRemoteGroup{ID: id, Name: name, Platform: "openai", Rate: rate, Peak: unknown})
	}
	return groups, nil
}
func (s *Service) newAPIPanel(ctx context.Context, account, base string) (func(string, string, any, any, string) error, []subRemoteGroup, error) {
	groups, err := s.newAPIGroups(ctx, account, base)
	if err != nil {
		return nil, nil, err
	}
	call := func(method, path string, body, out any, idem string) error {
		return s.newAPIAdapterCall(ctx, account, base, method, path, body, out)
	}
	return call, groups, nil
}
func newAPIKeyValue(key string) string {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "*•… \r\n\t") {
		return ""
	}
	if !strings.HasPrefix(key, "sk-") {
		key = "sk-" + key
	}
	return key
}
func (s *Service) newAPINormalizeKey(ctx context.Context, account, base string, key newAPIToken, reveal bool) (subRemoteKey, error) {
	if key.ID <= 0 || key.Name == "" {
		return subRemoteKey{}, errors.New("站点令牌数据无效")
	}
	auth, _, err := s.newAPIAuth(ctx, account)
	if err != nil {
		return subRemoteKey{}, err
	}
	if key.UserID != 0 && key.UserID != auth.UserID {
		return subRemoteKey{}, errors.New("令牌不属于当前账号")
	}
	if key.Group == "" {
		var user newAPIUser
		if err := s.newAPICall(ctx, account, base, "GET", "/api/user/self", nil, &user); err != nil {
			return subRemoteKey{}, err
		}
		if user.ID != auth.UserID || user.Group == "" {
			return subRemoteKey{}, errors.New("默认分组无法确认")
		}
		key.Group = user.Group
	}
	group, err := s.newAPIGroupID(ctx, account, key.Group)
	if err != nil {
		return subRemoteKey{}, err
	}
	secret := newAPIKeyValue(key.Key)
	if reveal && secret == "" {
		var data struct {
			Key string `json:"key"`
		}
		err = s.newAPICall(ctx, account, base, "POST", fmt.Sprintf("/api/token/%d/key", key.ID), nil, &data)
		if err != nil {
			return subRemoteKey{}, err
		}
		secret = newAPIKeyValue(data.Key)
		if secret == "" {
			return subRemoteKey{}, errors.New("站点未返回有效完整 key")
		}
	}
	state := map[int]string{1: "active", 2: "disabled", 3: "expired", 4: "quota_exhausted"}[key.Status]
	out := subRemoteKey{ID: key.ID, Name: key.Name, GroupID: group, Key: secret, Status: state}
	if key.Created > 0 {
		at := time.Unix(key.Created, 0)
		out.CreatedAt = &at
	}
	if key.Expires > 0 {
		at := time.Unix(key.Expires, 0)
		out.ExpiresAt = &at
	}
	// Unlimited remains nil: never reuse the sub2api legacy-$1 upgrade logic.
	if !key.Unlimited && auth.QuotaPerUnit > 0 {
		quota := (key.Remain + key.Used) / auth.QuotaPerUnit
		used := key.Used / auth.QuotaPerUnit
		out.Quota = &quota
		out.QuotaUsed = &used
	}
	return out, nil
}
func (s *Service) newAPIKeys(ctx context.Context, account, base, search string, page int) ([]subRemoteKey, int, error) {
	q := url.Values{"p": {strconv.Itoa(page)}, "page_size": {"100"}}
	path := "/api/token/"
	if search != "" {
		path = "/api/token/search"
		q.Set("keyword", search)
	}
	var list struct {
		Items []newAPIToken `json:"items"`
		Total int           `json:"total"`
	}
	if err := s.newAPICall(ctx, account, base, "GET", path+"?"+q.Encode(), nil, &list); err != nil {
		return nil, 0, err
	}
	if list.Items == nil || list.Total < 0 {
		return nil, 0, errors.New("站点令牌分页数据无效")
	}
	out := make([]subRemoteKey, len(list.Items))
	errs := make([]error, len(list.Items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, token := range list.Items {
		wg.Add(1)
		go func(i int, key newAPIToken) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			defer func() { <-sem }()
			out[i], errs[i] = s.newAPINormalizeKey(ctx, account, base, key, true)
		}(i, token)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, 0, e
		}
	}
	return out, list.Total, nil
}
func (s *Service) newAPIEnsureKey(ctx context.Context, account, base, name string, group int64) (subRemoteKey, error) {
	if len(name) > 50 || !(name == subKeyName(account, group) || name == "uni-console-route-"+account+"-"+strconv.FormatInt(group, 10)) {
		return subRemoteKey{}, errors.New("专用 key 名称无效")
	}
	var remote string
	if err := s.control.db.QueryRowContext(ctx, `SELECT remote_key FROM console_site_groups WHERE account_id=$1 AND id=$2`, account, group).Scan(&remote); err != nil {
		return subRemoteKey{}, errors.New("分组不存在")
	}
	find := func() (subRemoteKey, bool, error) {
		var found subRemoteKey
		for page := 1; page <= 100; page++ {
			list, total, e := s.newAPIKeys(ctx, account, base, name, page)
			if e != nil {
				return found, false, e
			}
			for _, key := range list {
				if key.Name == name {
					if key.GroupID != group || found.ID != 0 {
						return found, false, errors.New("同名专用 key 的分组不匹配或重复，请在原站核对")
					}
					found = key
				}
			}
			if page*100 >= total || len(list) < 100 {
				return found, found.ID > 0, nil
			}
		}
		return found, false, errors.New("令牌列表未读取完整")
	}
	if key, ok, e := find(); e != nil || ok {
		return key, e
	}
	// Refresh/validate before mutation; never retry a POST on authentication or
	// network uncertainty. Persist intent first, so crashes cannot duplicate keys.
	var profile newAPIUser
	if err := s.newAPICall(ctx, account, base, "GET", "/api/user/self", nil, &profile); err != nil {
		return subRemoteKey{}, err
	}
	result, err := s.control.db.ExecContext(ctx, `INSERT INTO console_site_key_creates(account_id,name) VALUES($1,$2) ON CONFLICT DO NOTHING`, account, name)
	if err != nil {
		return subRemoteKey{}, err
	}
	claimed, _ := result.RowsAffected()
	if claimed == 0 {
		return subRemoteKey{}, errors.New("上次创建结果尚未确认，已重查；请稍后同步，避免重复创建 key")
	}
	err = s.newAPICall(ctx, account, base, "POST", "/api/token/", map[string]any{"name": name, "group": remote, "unlimited_quota": true, "expired_time": -1, "remain_quota": 0, "model_limits_enabled": false, "model_limits": "", "allow_ips": "", "auto_ban": false, "auto_groups": []string{}}, nil)
	if key, ok, e := find(); e == nil && ok {
		return key, nil
	}
	var upstream *subRemoteError
	if errors.As(err, &upstream) && upstream.Status >= 400 && upstream.Status < 500 {
		_, _ = s.control.db.ExecContext(ctx, `DELETE FROM console_site_key_creates WHERE account_id=$1 AND name=$2`, account, name)
		return subRemoteKey{}, err
	}
	return subRemoteKey{}, errors.New("创建 key 的结果未确认，下次同步将先重查，不会重复创建")
}
func (s *Service) newAPIAdapterCall(ctx context.Context, account, base, method, path string, body, out any) error {
	u, err := url.Parse(path)
	if err != nil {
		return err
	}
	q := u.Query()
	switch {
	case u.Path == "/api/v1/groups/rates":
		groups, e := s.newAPIGroups(ctx, account, base)
		if e != nil {
			return e
		}
		rates := map[string]float64{}
		for _, g := range groups {
			if !g.Peak {
				rates[strconv.FormatInt(g.ID, 10)] = g.Rate
			}
		}
		return decodeMap(rates, out)
	case u.Path == "/api/v1/channels/available":
		return decodeMap([]any{}, out)
	case u.Path == "/api/v1/auth/me":
		var user newAPIUser
		if err = s.newAPICall(ctx, account, base, "GET", "/api/user/self", nil, &user); err != nil {
			return err
		}
		auth, _, e := s.newAPIAuth(ctx, account)
		if e != nil {
			return e
		}
		var balance *float64
		if user.Quota != nil && auth.QuotaPerUnit > 0 {
			v := *user.Quota / auth.QuotaPerUnit
			balance = &v
		}
		return decodeMap(map[string]any{"email": user.Username, "balance": balance}, out)
	case u.Path == "/api/v1/keys" && method == "GET":
		page, _ := strconv.Atoi(q.Get("page"))
		if page < 1 {
			page = 1
		}
		items, total, e := s.newAPIKeys(ctx, account, base, q.Get("search"), page)
		if e != nil {
			return e
		}
		return decodeMap(map[string]any{"items": items, "total": total, "pages": (total + 99) / 100, "page": page, "page_size": 100}, out)
	case u.Path == "/api/v1/keys" && method == "POST":
		var input struct {
			Name  string `json:"name"`
			Group int64  `json:"group_id"`
		}
		if decodeMap(body, &input) != nil {
			return errors.New("令牌输入无效")
		}
		key, e := s.newAPIEnsureKey(ctx, account, base, input.Name, input.Group)
		if e != nil {
			return e
		}
		return decodeMap(key, out)
	case strings.HasPrefix(u.Path, "/api/v1/keys/") && method == "GET":
		id, e := strconv.ParseInt(strings.TrimPrefix(u.Path, "/api/v1/keys/"), 10, 64)
		if e != nil || id <= 0 {
			return errors.New("令牌 ID 无效")
		}
		var token newAPIToken
		if err = s.newAPICall(ctx, account, base, "GET", fmt.Sprintf("/api/token/%d", id), nil, &token); err != nil {
			return err
		}
		if token.ID != id {
			return errors.New("令牌 ID 不匹配")
		}
		key, e := s.newAPINormalizeKey(ctx, account, base, token, true)
		if e != nil {
			return e
		}
		return decodeMap(key, out)
	case u.Path == "/api/v1/usage":
		return s.newAPIUsage(ctx, account, base, q, out)
	default:
		return errors.New("此站点暂不支持该管理操作")
	}
}

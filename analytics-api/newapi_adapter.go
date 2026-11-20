package main

import (
	"context"
	"database/sql"
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
	Accessed  int64   `json:"accessed_time"`
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
	if remote == nil {
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
	if len(names) > 0 {
		if err := s.clearNewAPIPresentObservations(ctx, account, base, names); err != nil {
			return nil, err
		}
	}
	return groups, nil
}

type newAPIKeyJobContext struct{}

func (s *Service) newAPIPanel(ctx context.Context, account, base, job string) (func(string, string, any, any, string) error, []subRemoteGroup, error) {
	groups, err := s.newAPIGroups(ctx, account, base)
	if err != nil {
		return nil, nil, err
	}
	call := func(method, path string, body, out any, idem string) error {
		return s.newAPIAdapterCall(context.WithValue(ctx, newAPIKeyJobContext{}, job), account, base, method, path, body, out)
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
	if reveal && secret == "" && (key.Name == subKeyName(account, group) || key.Name == "uni-console-route-"+account+"-"+strconv.FormatInt(group, 10)) {
		// New API token secrets are immutable for a given token ID. Reuse only
		// our exact account/group/ID/name binding, after reading current remote
		// metadata. Revoked, expired and disabled states still come from above.
		// This avoids spending the IP-wide critical-operation budget on every sync.
		var saved string
		query := `SELECT encrypted_key FROM console_sub_targets WHERE account_id=$1 AND group_id=$2 AND remote_key_id=$3`
		if strings.HasPrefix(key.Name, "uni-console-route-") {
			query = `SELECT encrypted_routing_key FROM console_sub_targets WHERE account_id=$1 AND group_id=$2 AND routing_key_id=$3`
		}
		err := s.control.db.QueryRowContext(ctx, query, account, group, key.ID).Scan(&saved)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return subRemoteKey{}, err
		}
		if saved != "" {
			plain, err := s.control.decrypt(saved)
			if err != nil {
				return subRemoteKey{}, errors.New("已保存密钥无法解密，请重新同步核对")
			}
			secret = newAPIKeyValue(plain)
		}
	}
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
	return newAPIKeyMetadata(key, group, secret, auth.QuotaPerUnit), nil
}

// Materialize metadata only after the token's account and group have been
// verified. Batch sync already holds both of those lookups for its entire plan.
func newAPIKeyMetadata(key newAPIToken, group int64, secret string, quotaPerUnit float64) subRemoteKey {
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
	if !key.Unlimited && quotaPerUnit > 0 {
		quota := (key.Remain + key.Used) / quotaPerUnit
		used := key.Used / quotaPerUnit
		out.Quota = &quota
		out.QuotaUsed = &used
	}
	return out
}
func (s *Service) newAPIKeys(ctx context.Context, account, base, search string, page int) ([]subRemoteKey, int, error) {
	if page < 1 {
		return nil, 0, errors.New("站点令牌页码无效")
	}
	// This is an inventory reconciliation, not an interactive search. New API's
	// search endpoint has a separate, low per-user limit and treats a keyword
	// without '%' as an exact name. Listing metadata and matching locally avoids
	// exhausting that limit during create/reconcile and correctly finds prefixes.
	// Complete the upstream scan before paginating matches: a page containing
	// only unrelated user keys must not imply that our dedicated key is absent.
	tokens, err := s.newAPIListTokens(ctx, account, base)
	if err != nil {
		return nil, 0, err
	}
	var matches []newAPIToken
	for _, token := range tokens {
		if strings.HasPrefix(token.Name, search) {
			matches = append(matches, token)
		}
	}
	total := len(matches)
	if total == 0 || page > (total+99)/100 {
		return []subRemoteKey{}, total, nil
	}
	start := (page - 1) * 100
	matches = matches[start:min(start+100, total)]
	out := make([]subRemoteKey, len(matches))
	errs := make([]error, len(matches))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, token := range matches {
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
	return out, total, nil
}
func (s *Service) newAPIListTokens(ctx context.Context, account, base string) ([]newAPIToken, error) {
	return s.newAPIReadTokenInventory(ctx, account, base, false)
}

func (s *Service) newAPIReadTokenInventory(ctx context.Context, account, base string, strict bool) ([]newAPIToken, error) {
	tokens := []newAPIToken{}
	matched := map[int64]bool{}
	progress := keyPageProgress{}
	total := -1
	for upstreamPage := 1; ; upstreamPage++ {
		q := url.Values{"p": {strconv.Itoa(upstreamPage)}, "page_size": {"100"}}
		var list struct {
			Items []newAPIToken `json:"items"`
			Total *int          `json:"total"`
		}
		if err := s.newAPICall(ctx, account, base, "GET", "/api/token/?"+q.Encode(), nil, &list); err != nil {
			return nil, err
		}
		pageTotal := 0
		if list.Total != nil {
			pageTotal = *list.Total
		}
		if list.Items == nil || pageTotal < 0 || strict && (list.Total == nil || total >= 0 && total != pageTotal) {
			return nil, errors.New("站点令牌分页数据无效")
		}
		total = pageTotal
		identities := make([]subRemoteKey, len(list.Items))
		for i, token := range list.Items {
			identities[i].ID = token.ID
		}
		if err := progress.advance(identities); err != nil {
			return nil, err
		}
		for _, token := range list.Items {
			if strict && (token.ID <= 0 || matched[token.ID]) {
				return nil, errors.New("站点令牌清单有重复或无效记录")
			}
			if !matched[token.ID] {
				matched[token.ID] = true
				tokens = append(tokens, token)
			}
		}
		if len(list.Items) == 0 || (total > 0 && len(progress) >= total) || (total == 0 && len(list.Items) < 100) {
			if strict && len(tokens) != total {
				return nil, errors.New("站点令牌清单不完整")
			}
			break
		}
	}
	return tokens, nil
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
		progress := keyPageProgress{}
		for page := 1; ; page++ {
			list, total, e := s.newAPIKeys(ctx, account, base, name, page)
			if e != nil {
				return found, false, e
			}
			if e = progress.advance(list); e != nil {
				return found, false, e
			}
			for _, key := range list {
				if key.Name == name {
					if key.GroupID != group || (found.ID != 0 && found.ID != key.ID) {
						return found, false, errors.New("同名专用 key 的分组不匹配或重复，请在原站核对")
					}
					found = key
				}
			}
			if len(list) == 0 || (total > 0 && len(progress) >= total) || (total == 0 && len(list) < 100) {
				return found, found.ID > 0, nil
			}
		}
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
	job, _ := ctx.Value(newAPIKeyJobContext{}).(string)
	err := s.newAPICreateWithReclaim(ctx, account, base, job, name, remote)
	var upstream *subRemoteError
	if err != nil && !errors.Is(err, errNewAPIKeyCreateUnconfirmed) && !errors.As(err, &upstream) {
		return subRemoteKey{}, err
	}
	if key, ok, e := find(); e == nil && ok {
		return key, nil
	}
	if upstream != nil {
		return subRemoteKey{}, err
	}
	return subRemoteKey{}, errNewAPIKeyCreateUnconfirmed
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

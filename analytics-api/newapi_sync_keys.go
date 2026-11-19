package main

import (
	"context"
	"errors"
	"fmt"
)

type newSyncKeyGroup struct {
	group   subRemoteGroup
	remote  string
	savedID int64
	saved   string
	key     subRemoteKey
}

// List token metadata once before creation and once after it, rather than
// scanning every page twice for each missing group. The second read is the
// authority even when a create response is lost. Only exact owned bindings
// may be adopted or passed to either reveal endpoint.
func (s *Service) subSyncNewAPIKeys(ctx context.Context, account, base, job string, groups []subRemoteGroup, rates map[string]float64, ratesKnown bool) error {
	failures := map[string]int{}
	if len(groups) == 0 {
		return s.subFinishSyncKeys(ctx, account, job, failures)
	}
	index := make(map[int64]*newSyncKeyGroup, len(groups))
	for _, group := range groups {
		index[group.ID] = &newSyncKeyGroup{group: group}
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT t.group_id,g.remote_key,t.remote_key_id,t.encrypted_key
 FROM console_sub_targets t JOIN console_site_groups g ON g.account_id=t.account_id AND g.id=t.group_id
 WHERE t.account_id=$1 AND t.active`, account)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, savedID int64
		var remote, saved string
		if err = rows.Scan(&id, &remote, &savedID, &saved); err != nil {
			rows.Close()
			return err
		}
		if entry := index[id]; entry != nil {
			entry.remote, entry.savedID, entry.saved = remote, savedID, saved
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	auth, _, err := s.newAPIAuthAt(ctx, account, base)
	if err != nil {
		return err
	}
	read := func() (map[string][]newAPIToken, error) {
		tokens, e := s.newAPIListTokens(ctx, account, base)
		if e != nil {
			return nil, e
		}
		byName := map[string][]newAPIToken{}
		for _, token := range tokens {
			byName[token.Name] = append(byName[token.Name], token)
		}
		return byName, nil
	}
	tokens, err := read()
	if err != nil {
		return err
	}
	var missing, reveal []*newSyncKeyGroup
	fail := func(g *newSyncKeyGroup, e error) error {
		failures[subTestKeyFailureKind(e)]++
		return s.subSyncKeyFailed(ctx, account, job, g.group.ID, e.Error())
	}
	save := func(g *newSyncKeyGroup) error {
		return s.subSaveSyncKey(ctx, account, job, g.group.ID, g.key, syncGroupBilling(g.group, rates, ratesKnown))
	}
	adopt := func(g *newSyncKeyGroup, found []newAPIToken) error {
		if g.remote == "" || len(found) != 1 || found[0].Group != g.remote || found[0].ID <= 0 || (found[0].UserID != 0 && found[0].UserID != auth.UserID) {
			return fail(g, errors.New("同名专用 key 的账号或分组不匹配，或存在重复"))
		}
		token := found[0]
		g.key = newAPIKeyMetadata(token, g.group.ID, newAPIKeyValue(token.Key), auth.QuotaPerUnit)
		if err := subTestKeyStateError(g.key); err != nil {
			return fail(g, err)
		}
		if g.key.Key == "" && g.savedID == token.ID && g.saved != "" {
			plain, e := s.control.decrypt(g.saved)
			if e != nil {
				return fail(g, errors.New("已保存测试密钥无法解密"))
			}
			g.key.Key = newAPIKeyValue(plain)
		}
		if g.key.Key != "" {
			return save(g)
		}
		reveal = append(reveal, g)
		return nil
	}
	for _, group := range groups {
		g := index[group.ID]
		found := tokens[subKeyName(account, group.ID)]
		if len(found) == 0 && g.remote != "" {
			missing = append(missing, g)
			continue
		}
		if err := adopt(g, found); err != nil {
			return err
		}
	}
	var createPause *subSyncPause
	rejections := map[int64]error{}
	if len(missing) > 0 {
		var profile newAPIUser
		if err := s.newAPICall(ctx, account, base, "GET", "/api/user/self", nil, &profile); err != nil {
			return err
		}
		if profile.ID != auth.UserID {
			return errors.New("站点会话身份变化，无法创建测试密钥")
		}
		for _, g := range missing {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := subKeyName(account, g.group.ID)
			e := s.newAPICreateOwnedKey(ctx, account, base, job, name, g.remote)
			if createPause = syncRatePause(e, waitingKeyCreate); createPause != nil {
				break
			}
			var remote *subRemoteError
			if errors.As(e, &remote) && remote.Status >= 400 && remote.Status < 500 {
				rejections[g.group.ID] = &subTestKeyError{"create_failed", "创建专用测试 key 失败：" + e.Error()}
				if remote.Status == 401 || remote.Status == 403 {
					return e
				}
			} else if e != nil && !errors.Is(e, errNewAPIKeyCreatePending) && !errors.Is(e, errNewAPIKeyCreateUnconfirmed) {
				return e
			}
		}
		tokens, err = read()
		if err != nil {
			return err
		}
		for _, g := range missing {
			found := tokens[subKeyName(account, g.group.ID)]
			if len(found) == 0 {
				e := rejections[g.group.ID]
				if e == nil && createPause != nil {
					continue
				}
				if e == nil {
					e = &subTestKeyError{"create_failed", errNewAPIKeyCreateUnconfirmed.Error()}
				}
				if err := fail(g, e); err != nil {
					return err
				}
				continue
			}
			if err := adopt(g, found); err != nil {
				return err
			}
		}
	}
	var unsupported bool
	for offset := 0; offset < len(reveal); offset += 100 {
		chunk := reveal[offset:min(offset+100, len(reveal))]
		var decoded struct {
			Keys map[int64]string `json:"keys"`
		}
		if !unsupported {
			ids := make([]int64, 0, len(chunk))
			for _, g := range chunk {
				ids = append(ids, g.key.ID)
			}
			e := s.newAPICall(ctx, account, base, "POST", newAPIBatchKeyPath, map[string]any{"ids": ids}, &decoded)
			var remote *subRemoteError
			switch {
			case e == nil && decoded.Keys == nil:
				return errors.New("批量密钥响应无效")
			case e == nil:
			case errors.As(e, &remote) && (remote.Status == 404 || remote.Status == 405 || remote.Status == 501):
				unsupported = true
			default:
				return e
			}
		}
		for _, g := range chunk {
			if unsupported {
				var data struct {
					Key string `json:"key"`
				}
				e := s.newAPICall(ctx, account, base, "POST", fmt.Sprintf("/api/token/%d/key", g.key.ID), nil, &data)
				if e != nil {
					if pause := syncRatePause(e, "waiting_keys"); pause != nil {
						return pause
					}
					if err := fail(g, e); err != nil {
						return err
					}
					continue
				}
				g.key.Key = newAPIKeyValue(data.Key)
			} else {
				g.key.Key = newAPIKeyValue(decoded.Keys[g.key.ID])
			}
			if g.key.Key == "" {
				if err := fail(g, errors.New("站点未返回该组完整测试密钥")); err != nil {
					return err
				}
				continue
			}
			if err := save(g); err != nil {
				return err
			}
		}
	}
	if createPause != nil {
		return createPause
	}
	return s.subFinishSyncKeys(ctx, account, job, failures)
}

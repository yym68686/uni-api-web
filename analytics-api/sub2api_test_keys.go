package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type subPanelCall func(string, string, any, any, string) error

type subTestKeyError struct {
	kind, message string
}

func (e *subTestKeyError) Error() string { return e.message }

func subTestKeyStateError(key subRemoteKey) error {
	if key.Status == "expired" || (key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now())) {
		return &subTestKeyError{"expired", "专用测试 key 已过期，请在上游调整有效期后同步"}
	}
	if key.Status == "quota_exhausted" || (key.Status == "active" && key.Quota != nil && key.QuotaUsed != nil && *key.Quota > 0 && *key.QuotaUsed >= *key.Quota) {
		detail := ""
		if key.Quota != nil && key.QuotaUsed != nil {
			detail = fmt.Sprintf("（已用 $%.6f / 限额 $%.6f）", *key.QuotaUsed, *key.Quota)
		}
		return &subTestKeyError{"quota_exhausted", "专用测试 key 额度已耗尽" + detail + "；请在上游调整该 key 的额度后同步"}
	}
	if key.Status == "inactive" || key.Status == "disabled" {
		return &subTestKeyError{"disabled", "专用测试 key 已停用，请在上游启用后同步"}
	}
	if key.Status != "active" {
		return &subTestKeyError{"unavailable", "专用测试 key 状态不可用，请在上游检查后同步"}
	}
	return nil
}

// Never reset spending, extend expiry, reactivate a disabled key, or alter a
// business key. Only the console's recorded test credential can lose the old
// built-in $1 cap; a custom quota is left under the upstream user's control.
func subPrepareTestKey(call subPanelCall, account string, group int64, key subRemoteKey, found bool, storedID int64, storedKey string) (subRemoteKey, error) {
	name := subKeyName(account, group)
	if !found {
		err := call("POST", "/api/v1/keys", map[string]any{"name": name, "group_id": group}, &key, "subcheck-"+account+"-"+strconv.FormatInt(group, 10))
		if err != nil {
			return key, &subTestKeyError{"create_failed", "创建专用测试 key 失败：" + err.Error()}
		}
	}
	if key.GroupID != group || key.Key == "" || key.ID <= 0 || key.Name != name {
		return key, &subTestKeyError{"unavailable", "站点返回的测试 key 与名称或分组不匹配"}
	}
	legacy := func(k subRemoteKey) bool {
		return k.ID == storedID && k.Key == storedKey && k.Name == name && k.GroupID == group &&
			k.Quota != nil && *k.Quota == 1 && (k.Status == "active" || k.Status == "quota_exhausted") &&
			(k.ExpiresAt == nil || k.ExpiresAt.After(time.Now()))
	}
	if found && legacy(key) {
		path := "/api/v1/keys/" + strconv.FormatInt(key.ID, 10)
		var current subRemoteKey
		if err := call("GET", path, nil, &current, ""); err != nil {
			return key, &subTestKeyError{"quota_update_failed", "读取旧测试 key 额度失败：" + err.Error()}
		}
		if current.ID != key.ID || current.Key != key.Key || current.Name != name || current.GroupID != group {
			return key, &subTestKeyError{"unavailable", "专用测试 key 已变更，请重新同步"}
		}
		key = current
		if legacy(current) {
			// An absolute quota update is safe to retry. Do not send reset_quota or
			// status: quota_exhausted reactivation belongs to the upstream service.
			if err := call("PUT", path, map[string]any{"quota": 0}, nil, ""); err != nil {
				return key, &subTestKeyError{"quota_update_failed", "取消旧测试 key 的 $1 限额失败：" + err.Error()}
			}
			var verified subRemoteKey
			if err := call("GET", path, nil, &verified, ""); err != nil {
				return key, &subTestKeyError{"quota_update_failed", "测试 key 额度更新后未能核验，请重新同步：" + err.Error()}
			}
			if verified.ID != current.ID || verified.Key != current.Key || verified.Name != name || verified.GroupID != group || verified.Quota == nil || *verified.Quota != 0 {
				return key, &subTestKeyError{"quota_update_failed", "上游未确认测试 key 已取消限额，请检查后重新同步"}
			}
			key = verified
		}
	}
	return key, subTestKeyStateError(key)
}

func subTestKeyFailureSummary(failures map[string]int) string {
	parts := []string{}
	for _, item := range []struct{ kind, label string }{
		{"create_failed", "创建测试 key 失败"},
		{"quota_update_failed", "测试 key 额度更新未完成"},
		{"quota_exhausted", "测试 key 额度耗尽"},
		{"expired", "测试 key 已过期"},
		{"disabled", "测试 key 已停用"},
		{"unavailable", "测试 key 不可用"},
	} {
		if n := failures[item.kind]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d 个分组%s", n, item.label))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "；") + "，详情见表格"
}

func subTestKeyFailureKind(err error) string {
	var failure *subTestKeyError
	if errors.As(err, &failure) {
		return failure.kind
	}
	return "unavailable"
}

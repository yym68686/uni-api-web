package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
)

const subGroupDisabledMessage = "站点已停用该分组，无法新增接入；请改用其他可用分组，或等待站点恢复后重新检查。"
const subGroupUnavailableMessage = "站点当前未开放该分组，无法新增接入；请在站点确认分组状态或账号权限后重新检查。"

type subGroupAccess struct {
	Available bool   `json:"available"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
}

// The current group directory is authoritative, not an old successful probe.
// Key details only explain a missing group; they never grant access themselves.
func readSubGroupAccess(groups []subRemoteGroup, group, key int64, read func(string, any) error) (subGroupAccess, error) {
	if groups == nil {
		return subGroupAccess{}, errors.New("站点分组列表无效，请稍后重新检查")
	}
	for _, g := range groups {
		if g.ID == group {
			if g.Status != "" && g.Status != "active" {
				return subGroupAccess{Status: "disabled", Message: subGroupDisabledMessage}, nil
			}
			return subGroupAccess{Available: true, Status: "available"}, nil
		}
	}
	if key > 0 {
		var detail struct {
			GroupID int64           `json:"group_id"`
			Group   *subRemoteGroup `json:"group"`
		}
		if read("/api/v1/keys/"+strconv.FormatInt(key, 10), &detail) == nil && detail.GroupID == group && detail.Group != nil && detail.Group.ID == group && detail.Group.Status != "" && detail.Group.Status != "active" {
			return subGroupAccess{Status: "disabled", Message: subGroupDisabledMessage}, nil
		}
	}
	return subGroupAccess{Status: "unavailable", Message: subGroupUnavailableMessage}, nil
}

// Update only discovery metadata, fenced against concurrent sync/re-login.
// Existing gateway bindings and historical check results remain intact.
func (s *Service) saveSubGroupAccess(ctx context.Context, account string, group int64, job string, synced int64, access subGroupAccess) error {
	_, err := s.control.db.ExecContext(ctx, `UPDATE console_sub_targets SET active=$3,message=CASE WHEN NOT $3 THEN $4 WHEN message IN ($7,$8) THEN '' ELSE message END WHERE account_id=$1 AND group_id=$2 AND EXISTS(SELECT 1 FROM console_sub_accounts WHERE id=$1 AND job_id=$5 AND synced_at=$6)`, account, group, access.Available, access.Message, job, synced, subGroupDisabledMessage, subGroupUnavailableMessage)
	return err
}

func (s *Service) subGroupAccessStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, _ := s.controlUser(r)
	account, group := r.PathValue("id"), int64Param(r.PathValue("group"))
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var base, job string
	var key, synced int64
	if err := s.control.db.QueryRowContext(ctx, `SELECT a.base,a.job_id,a.synced_at,t.remote_key_id FROM console_sub_accounts a JOIN console_sub_targets t ON t.account_id=a.id WHERE a.id=$1 AND a.owner=$2 AND t.group_id=$3`, account, owner, group).Scan(&base, &job, &synced, &key); err != nil {
		http.Error(w, "分组不存在", 404)
		return
	}
	read := func(path string, out any) error { return s.subUsageGET(ctx, account, base, path, out) }
	var groups []subRemoteGroup
	var err error
	if s.isNewAPI(ctx, account) {
		groups, err = s.newAPIGroups(ctx, account, base)
	} else {
		err = read("/api/v1/groups/available", &groups)
	}
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	access, err := readSubGroupAccess(groups, group, key, read)
	if err == nil {
		err = s.saveSubGroupAccess(ctx, account, group, job, synced, access)
	}
	if err != nil {
		http.Error(w, "无法确认最新分组状态，请稍后重新检查", 502)
		return
	}
	writeJSON(w, 200, access)
}

package main

import (
	"context"
	"net/http"
	"strings"
)

type detectionTarget struct {
	Source   string `json:"source_id,omitempty"`
	Provider string `json:"provider,omitempty"`
	Account  string `json:"account_id,omitempty"`
	Group    int64  `json:"group_id,omitempty"`
}

func (t detectionTarget) scope() string {
	return mustJSON([]any{t.Source, t.Provider, t.Account, t.Group})
}
func (t detectionTarget) valid() bool {
	return (t.Source != "" && t.Provider != "" && len(t.Provider) <= 256 && !strings.ContainsAny(t.Provider, "\r\n/") && t.Account == "" && t.Group == 0) ||
		(t.Source == "" && t.Provider == "" && t.Account != "" && t.Group > 0)
}

func readDisabledDetection(ctx context.Context, db controlQueryDB, owner string) (map[string]bool, []detectionTarget, error) {
	rows, err := db.QueryContext(ctx, `SELECT source_id,provider,account_id,group_id FROM console_channel_detection_settings WHERE owner=$1 ORDER BY scope`, owner)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	disabled := map[string]bool{}
	data := []detectionTarget{}
	for rows.Next() {
		var t detectionTarget
		if err = rows.Scan(&t.Source, &t.Provider, &t.Account, &t.Group); err != nil {
			return nil, nil, err
		}
		disabled[t.scope()] = true
		data = append(data, t)
	}
	return disabled, data, rows.Err()
}

// Settings belong to the console user and exact account/group or source/provider,
// never a display name. They neither edit gateway routing nor cancel paid work.
func (s *Service) channelDetectionSettings(w http.ResponseWriter, r *http.Request) {
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method == "GET" {
		_, data, err := readDisabledDetection(r.Context(), s.control.db, owner)
		if err != nil {
			http.Error(w, "检测开关读取失败", 503)
			return
		}
		writeJSON(w, 200, map[string]any{"disabled": data})
		return
	}
	var in struct {
		Enabled *bool             `json:"enabled"`
		Targets []detectionTarget `json:"targets"`
	}
	if !decodeConfiguration(w, r, &in) {
		return
	}
	if in.Enabled == nil || len(in.Targets) == 0 || len(in.Targets) > 512 {
		http.Error(w, "请选择渠道并指定检测开关", 400)
		return
	}
	tx, err := s.control.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "检测开关保存失败", 503)
		return
	}
	defer tx.Rollback()
	for _, t := range in.Targets {
		if !t.valid() {
			http.Error(w, "渠道标识无效", 400)
			return
		}
		var allowed bool
		if t.Account != "" {
			err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id WHERE a.owner=$1 AND a.id=$2 AND t.group_id=$3)`, owner, t.Account, t.Group).Scan(&allowed)
		} else {
			err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM console_channel_management_snapshots m JOIN console_sources s ON s.id=m.source_id WHERE s.enabled AND m.source_id=$1 AND m.catalog->'providers' ? $2)`, t.Source, t.Provider).Scan(&allowed)
		}
		if err != nil {
			http.Error(w, "渠道状态读取失败", 503)
			return
		}
		if !allowed {
			http.Error(w, "渠道不存在或无权访问，请刷新核对", 404)
			return
		}
		if *in.Enabled {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM console_channel_detection_settings WHERE owner=$1 AND scope=$2`, owner, t.scope())
		} else {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO console_channel_detection_settings(owner,scope,source_id,provider,account_id,group_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(owner,scope) DO NOTHING`, owner, t.scope(), t.Source, t.Provider, t.Account, t.Group)
		}
		if err != nil {
			http.Error(w, "检测开关保存失败", 503)
			return
		}
	}
	if tx.Commit() != nil {
		http.Error(w, "检测开关保存失败", 503)
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}

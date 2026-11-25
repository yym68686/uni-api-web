package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

type subSyncProgress struct {
	Total         int                `json:"total"`
	Completed     int                `json:"completed"`
	Queued        int                `json:"queued"`
	Running       int                `json:"running"`
	Interrupted   int                `json:"interrupted"`
	Rejected      int                `json:"rejected,omitempty"`
	Skipped       int                `json:"skipped,omitempty"`
	KeysReady     int                `json:"keys_ready"`
	KeysTotal     int                `json:"keys_total"`
	KeyErrors     int                `json:"key_errors"`
	RunningModels []string           `json:"running_models"`
	Groups        []subGroupProgress `json:"groups"`
}
type subGroupProgress struct {
	State       string `json:"state"`
	Compaction  string `json:"compaction_state"`
	ToolUse     string `json:"tool_use_state"`
	TierVersion int64  `json:"tier_version,omitempty"`
	ToolVersion int64  `json:"tool_version,omitempty"`
	GroupID     int64  `json:"group_id"`
	Total       int    `json:"total"`
	Completed   int    `json:"completed"`
	Queued      int    `json:"queued"`
	Running     int    `json:"running"`
	Interrupted int    `json:"interrupted"`
	Rejected    int    `json:"rejected,omitempty"`
	Skipped     int    `json:"skipped,omitempty"`
}

// Poll only task state/identity. No result JSON, receipts, gateway directory or
// management page recomputation is needed to report a running job's progress.
func (s *Service) subAccountsProgress(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	var watched []string
	if raw := r.URL.Query().Get("accounts"); raw != "" {
		if json.Unmarshal([]byte(raw), &watched) != nil || len(watched) > 256 {
			http.Error(w, "任务进度范围无效", 400)
			return
		}
	}
	tx, err := s.control.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		http.Error(w, "任务进度暂不可用", 503)
		return
	}
	defer tx.Rollback()
	accounts, err := readSubAccountHeaders(r.Context(), tx, owner, "")
	if err == nil {
		err = readSubSyncProgress(r, tx, owner, accounts, watched)
	}
	if err != nil {
		log.Printf("sub_progress_read_failed class=%s request_id=%s", databaseErrorClass(err), consoleRequestID(r.Context()))
		http.Error(w, "任务进度读取失败，请重试", 503)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"data": accounts, "read_at_ms": time.Now().UnixMilli()})
}

func readSubSyncProgress(r *http.Request, tx *sql.Tx, owner string, accounts []subAccount, watched []string) error {
	// The durable submitted selection wins over subsequent browser checkbox
	// changes. Only state and model identity are read, never historical results.
	rows, err := tx.QueryContext(r.Context(), `SELECT t.account_id,t.group_id,t.state,t.compaction_state,t.tool_use_state,t.tool_read_version,
 CASE WHEN a.sync_run_id<>'' AND t.sync_run_id=a.sync_run_id THEN t.sync_key_state ELSE '' END,count(m.model),
 count(*) FILTER(WHERE m.state IN ('done','error')),
 count(*) FILTER(WHERE m.state='queued'),count(*) FILTER(WHERE m.state='running'),
 count(*) FILTER(WHERE m.state='interrupted' AND m.message NOT IN `+legacySiteRejectionSQL+`),
 count(*) FILTER(WHERE m.state='rejected' OR (m.state='interrupted' AND m.message IN `+legacySiteRejectionSQL+`)),
 count(*) FILTER(WHERE m.state='skipped'),
 COALESCE(jsonb_agg(m.model ORDER BY m.model) FILTER(WHERE m.state='running'),'[]'::jsonb),COALESCE(sum(m.tier_read_version),0)::bigint
 FROM console_sub_accounts a JOIN console_sub_targets t ON t.account_id=a.id AND t.active
 LEFT JOIN console_sub_models m ON m.account_id=t.account_id AND m.group_id=t.group_id
 AND (a.sync_run_id<>'' AND a.job_kind IN ('','sync') AND m.sync_run_id=a.sync_run_id
 OR (a.sync_run_id='' OR a.job_kind NOT IN ('','sync')) AND (a.job_kind NOT IN ('','sync') OR a.sync_models IS NULL OR a.sync_models ? m.model))
 WHERE a.owner=$1 AND (a.state IN ('queued','running') OR a.id=ANY($2::text[]))
 AND NOT (a.state='queued' AND a.job_kind='sync')
 AND (a.sync_run_id='' OR a.job_kind NOT IN ('','sync') OR t.sync_run_id=a.sync_run_id)
 GROUP BY t.account_id,t.group_id,t.state,t.compaction_state,t.tool_use_state,t.tool_read_version,t.sync_key_state,a.sync_run_id,t.sync_run_id ORDER BY t.account_id,t.group_id`, owner, watched)
	if err != nil {
		return err
	}
	defer rows.Close()
	index := map[string]*subAccount{}
	synced := map[string]int{}
	wanted := map[string]bool{}
	for _, id := range watched {
		wanted[id] = true
	}

	for i := range accounts {
		a := &accounts[i]
		index[a.ID] = a
		// Completed, unwatched accounts need only fresh header state.
		a.Progress = nil
		if wanted[a.ID] || a.State == "queued" || a.State == "running" {
			a.Progress = &subSyncProgress{RunningModels: []string{}, Groups: []subGroupProgress{}}
		}

	}
	for rows.Next() {
		var id string
		var keyState string
		var g subGroupProgress
		var raw []byte
		if err = rows.Scan(&id, &g.GroupID, &g.State, &g.Compaction, &g.ToolUse, &g.ToolVersion, &keyState, &g.Total, &g.Completed, &g.Queued, &g.Running, &g.Interrupted, &g.Rejected, &g.Skipped, &raw, &g.TierVersion); err != nil {
			return err
		}
		a := index[id]
		if a == nil {
			continue
		}
		if g.Total > 0 && g.Completed+g.Rejected+g.Skipped == g.Total && keyState != "preparing" && keyState != "error" || g.Total == 0 && g.State == "done" {
			synced[id]++
		}
		if a.Progress == nil {
			a.Progress = &subSyncProgress{RunningModels: []string{}, Groups: []subGroupProgress{}}
		}
		p := a.Progress
		if keyState != "" {
			p.KeysTotal++
		}
		if keyState == "ready" {
			p.KeysReady++
		}
		if keyState == "error" {
			p.KeyErrors++
		}
		p.Groups = append(p.Groups, g)
		p.Total += g.Total
		p.Completed += g.Completed
		p.Queued += g.Queued
		p.Running += g.Running
		p.Interrupted += g.Interrupted
		p.Rejected += g.Rejected
		p.Skipped += g.Skipped
		var names []string
		if err = json.Unmarshal(raw, &names); err != nil {
			return err
		}
		for _, name := range names {
			if len(p.RunningModels) < 4 {
				p.RunningModels = append(p.RunningModels, name)
			}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range accounts {
		a := &accounts[i]
		if a.Progress == nil {
			continue
		}
		active, complete := len(a.Progress.Groups), synced[a.ID]
		a.ActiveGroups, a.CompletedGroups = &active, &complete
	}
	return nil
}

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
	RunningModels []string           `json:"running_models"`
	Groups        []subGroupProgress `json:"groups"`
}
type subGroupProgress struct {
	GroupID     int64 `json:"group_id"`
	Total       int   `json:"total"`
	Completed   int   `json:"completed"`
	Queued      int   `json:"queued"`
	Running     int   `json:"running"`
	Interrupted int   `json:"interrupted"`
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
	rows, err := tx.QueryContext(r.Context(), `SELECT t.account_id,t.group_id,t.state,count(m.model),
 count(*) FILTER(WHERE m.state IN ('done','error')),
 count(*) FILTER(WHERE m.state='queued'),count(*) FILTER(WHERE m.state='running'),
 count(*) FILTER(WHERE m.state='interrupted'),
 COALESCE(jsonb_agg(m.model ORDER BY m.model) FILTER(WHERE m.state='running'),'[]'::jsonb)
 FROM console_sub_accounts a JOIN console_sub_targets t ON t.account_id=a.id AND t.active
 LEFT JOIN console_sub_models m ON m.account_id=t.account_id AND m.group_id=t.group_id
 AND (a.job_kind NOT IN ('','sync') OR a.sync_models IS NULL OR a.sync_models ? m.model)
 WHERE a.owner=$1 AND (a.state IN ('queued','running') OR a.id=ANY($2::text[])) GROUP BY t.account_id,t.group_id,t.state ORDER BY t.account_id,t.group_id`, owner, watched)
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
		var targetState string
		var g subGroupProgress
		var raw []byte
		if err = rows.Scan(&id, &g.GroupID, &targetState, &g.Total, &g.Completed, &g.Queued, &g.Running, &g.Interrupted, &raw); err != nil {
			return err
		}
		a := index[id]
		if a == nil {
			continue
		}
		if targetState == "done" {
			synced[id]++
		}
		if a.Progress == nil {
			a.Progress = &subSyncProgress{RunningModels: []string{}, Groups: []subGroupProgress{}}
		}
		p := a.Progress
		p.Groups = append(p.Groups, g)
		p.Total += g.Total
		p.Completed += g.Completed
		p.Queued += g.Queued
		p.Running += g.Running
		p.Interrupted += g.Interrupted
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
		active, complete := len(a.Progress.Groups), 0
		for _, g := range a.Progress.Groups {
			if g.Total > 0 && g.Completed == g.Total {
				complete++
			}
		}
		if a.SyncOnly {
			complete = synced[a.ID]
		}
		a.ActiveGroups, a.CompletedGroups = &active, &complete
	}
	return nil
}

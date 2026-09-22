package main

import (
	"context"
	"database/sql"
	"errors"
)

func subSelectionModels(target subSelection, kind string) []string {
	if kind == "compaction" || kind == "tool_use" {
		return []string{""}
	}
	if kind == "quality" {
		return []string{checkModel}
	}
	if len(target.Models) == 0 {
		return subModels
	}
	return target.Models
}

// The caller holds the account row lock, shared with stop and job promotion.
// Only duplicate work is rejected, never another group or detection kind.
func (s *Service) subEnqueueCheck(ctx context.Context, tx *sql.Tx, target subSelection, kind, model string) (int, error) {
	var active, duplicate bool
	err := tx.QueryRowContext(ctx, `SELECT active AND encrypted_key<>'',
	 CASE WHEN $3='compaction' THEN compaction_state IN ('queued','running')
	 WHEN $3='tool_use' THEN tool_use_state IN ('queued','running')
	 ELSE EXISTS(SELECT 1 FROM console_sub_models m WHERE m.account_id=t.account_id AND m.group_id=t.group_id AND m.model=$4 AND m.state IN ('queued','running')) END
	 OR EXISTS(SELECT 1 FROM console_sub_check_queue q WHERE q.account_id=t.account_id AND q.group_id=t.group_id AND
	 ((q.kind=$3 AND q.model=$4) OR ($3 IN ('quality','check') AND q.kind IN ('quality','check') AND q.model=$4)
	 OR ($3 IN ('compaction','tool_use') AND q.kind='check')))
	 FROM console_sub_targets t WHERE account_id=$1 AND group_id=$2`, target.AccountID, target.GroupID, kind, model).Scan(&active, &duplicate)
	if err == sql.ErrNoRows || (err == nil && !active) {
		return 400, errors.New("分组不可用或尚未创建 key，请先同步")
	}
	if err != nil {
		return 503, errors.New("检测状态读取失败")
	}
	if duplicate {
		return 409, errors.New("所选检测项已在排队或检测中")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES($1,$2,$3,$4)`, target.AccountID, target.GroupID, kind, model)
	if err != nil {
		return 503, errors.New("任务创建失败")
	}
	return 202, nil
}

// Promote one kind at a time into the existing account worker. Its two-worker
// limit, probe semantics, lease fencing and crash behavior remain unchanged.
func (s *Service) subPromoteAccountChecks(ctx context.Context, tx *sql.Tx, id string) error {
	var kind string
	err := tx.QueryRowContext(ctx, `SELECT kind FROM console_sub_check_queue WHERE account_id=$1 ORDER BY created_at,kind,group_id,model LIMIT 1`, id).Scan(&kind)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE console_sub_accounts SET state='queued',job_kind=$2,job_id='',message='',lease_until=NULL WHERE id=$1`, id, kind)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE console_sub_targets t SET state='queued',message='',
	 compaction_state=CASE WHEN $2 IN ('compaction','check') THEN 'queued' WHEN compaction_state IN ('queued','running') THEN 'interrupted' ELSE compaction_state END,
	 tool_use_state=CASE WHEN $2 IN ('tool_use','check') THEN 'queued' WHEN tool_use_state IN ('queued','running') THEN 'interrupted' ELSE tool_use_state END
	 WHERE account_id=$1 AND EXISTS(SELECT 1 FROM console_sub_check_queue q WHERE q.account_id=t.account_id AND q.group_id=t.group_id AND q.kind=$2)`, id, kind)
	if err != nil {
		return err
	}
	if kind == "check" || kind == "quality" {
		_, err = tx.ExecContext(ctx, `INSERT INTO console_sub_models(account_id,group_id,model,state)
		 SELECT account_id,group_id,model,'queued' FROM console_sub_check_queue WHERE account_id=$1 AND kind=$2
		 ON CONFLICT(account_id,group_id,model) DO UPDATE SET state='queued',message=''`, id, kind)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM console_sub_check_queue WHERE account_id=$1 AND kind=$2`, id, kind)
	return err
}

func (s *Service) subPromoteChecks(ctx context.Context) error {
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT a.id FROM console_sub_accounts a WHERE a.state NOT IN ('queued','running')
	 AND EXISTS(SELECT 1 FROM console_sub_check_queue q WHERE q.account_id=a.id)
	 ORDER BY a.id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if err = s.subPromoteAccountChecks(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Project durable waiting tasks into the same per-check UI states used by the
// active worker. Do not overwrite stored results or the active worker's states.
func (s *Service) subAttachQueuedChecks(ctx context.Context, owner string, accounts []subAccount) error {
	rows, err := s.control.db.QueryContext(ctx, `SELECT q.account_id,q.group_id,q.kind,q.model FROM console_sub_check_queue q JOIN console_sub_accounts a ON a.id=q.account_id WHERE a.owner=$1`, owner)
	if err != nil {
		return err
	}
	defer rows.Close()
	targets := map[string]map[int64]*subTarget{}
	accountIndex := map[string]*subAccount{}
	for i := range accounts {
		a := &accounts[i]
		accountIndex[a.ID] = a
		targets[a.ID] = map[int64]*subTarget{}
		for j := range a.Targets {
			targets[a.ID][a.Targets[j].GroupID] = &a.Targets[j]
		}
	}
	for rows.Next() {
		var id, kind, model string
		var group int64
		if err = rows.Scan(&id, &group, &kind, &model); err != nil {
			return err
		}
		t := targets[id][group]
		if t == nil {
			continue
		}
		a := accountIndex[id]
		if a.State != "running" && a.State != "queued" {
			a.State = "queued"
		}
		if kind == "compaction" {
			t.CompactionState = "queued"
			continue
		}
		if kind == "tool_use" {
			t.ToolUseState = "queued"
			continue
		}
		if kind == "check" {
			if t.CompactionState != "running" {
				t.CompactionState = "queued"
			}
			if t.ToolUseState != "running" {
				t.ToolUseState = "queued"
			}
		}
		found := false
		for i := range t.Models {
			if t.Models[i].Model == model {
				t.Models[i].State = "queued"
				found = true
				break
			}
		}
		if !found {
			t.Models = append(t.Models, subModelResult{Model: model, State: "queued"})
		}
	}
	return rows.Err()
}

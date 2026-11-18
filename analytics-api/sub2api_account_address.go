package main

import (
	"context"
	"net/http"
)

// The original site identity is captured by the owner-checked login handler,
// never by a downstream adapter or untrusted browser session.
type subLoginBaseKey struct{}

func subLoginBase(r *http.Request) string {
	base, _ := r.Context().Value(subLoginBaseKey{}).(string)
	return base
}

// Called only after verifying a fresh session at the destination and while
// holding the auth lease. Failed verification never changes the saved site.
func (s *Service) subSaveSiteAddress(w http.ResponseWriter, r *http.Request, ctx context.Context, previous, base, login, kind, encrypted, password string) {
	owner, _ := s.controlUser(r)
	id := r.PathValue("id")
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "站点地址保存失败", 503)
		return
	}
	defer tx.Rollback()
	var found string
	err = tx.QueryRowContext(ctx, `SELECT id FROM console_sub_accounts WHERE id=$1 AND owner=$2 AND base=$3 AND lower(email)=lower($4) AND provider_kind=$5 AND state NOT IN ('queued','running') FOR NO KEY UPDATE`, id, owner, previous, login, kind).Scan(&found)
	if err != nil {
		http.Error(w, "账号已变化或有任务进行中，请刷新后重试", 409)
		return
	}
	// Serialize with quote submission, revoke outstanding quotes, and never
	// move an account while a confirmed payment/redeem is still in progress.
	if _, err = tx.ExecContext(ctx, `UPDATE console_recharge_settings SET revision=$2 WHERE account_id=$1`, id, randomID()); err != nil {
		http.Error(w, "站点地址保存失败", 503)
		return
	}
	var blocked bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_recharge_jobs WHERE account_id=$1 AND state NOT IN ('done','closed','failed')) OR EXISTS(SELECT 1 FROM console_sub_accounts WHERE owner=$2 AND base=$3 AND lower(email)=lower($4) AND id<>$1)`, id, owner, base, login).Scan(&blocked)
	if err != nil || blocked {
		http.Error(w, "目标站点账号已存在或有充值任务未完成，暂不能更改地址", 409)
		return
	}
	// Fence late workers and invalidate only current observations/remote keys.
	// Keep historical usage/quality facts and all gateway configuration intact.
	_, err = tx.ExecContext(ctx, `UPDATE console_sub_accounts SET base=$2,encrypted_auth=$3,encrypted_login_password=$4,state='idle',message='',balance=NULL,synced_at=0,job_id=$5,job_kind='',sync_models=NULL,lease_until=NULL,usage_lease_token='',usage_lease_until=NULL WHERE id=$1`, id, base, encrypted, password, randomID())
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE console_sub_targets SET active=false,state='idle',message='',remote_key_id=0,encrypted_key='',routing_key_id=0,encrypted_routing_key='',result=NULL,billing=NULL,compaction=NULL,compaction_state='idle',tool_use=NULL,tool_use_state='idle' WHERE account_id=$1`, id)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE console_sub_models SET state='idle',message='',result=NULL WHERE account_id=$1`, id)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE console_model_fingerprints SET state='interrupted',message='站点地址已更改，请重新检测',result=NULL,sample_state=NULL,revision='',run_id='',deadline=NULL,site_key='',updated_at=now() WHERE account_id=$1`, id)
	}
	for _, table := range []string{"console_sub_check_queue", "console_sub_key_scans", "console_sub_key_index", "console_sub_key_index_stage", "console_sub_observed_rates"} {
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE account_id=$1`, id)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		http.Error(w, "站点地址保存失败，请刷新后重试", 409)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "base": base, "address_changed": true, "authenticated": true, "queued": false})
}

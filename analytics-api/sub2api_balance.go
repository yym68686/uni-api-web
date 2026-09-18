package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type subAccountBalance struct {
	Amount    *float64 `json:"amount"`
	CheckedAt int64    `json:"checked_at"`
	Status    string   `json:"status"`
}

// Serialize token rotation with sync/import jobs, across API replicas.
func (s *Service) subLockAuth(ctx context.Context, id string) (func(), error) {
	// Both callers bound auth work to 15 seconds. A short lease preserves the
	// same per-account exclusion without pinning a pool connection while a
	// remote site responds. Otherwise simultaneous sites could exhaust the pool
	// before their post-lock reads and heartbeat updates can run.
	token := randomID()
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("account authentication lock requires a deadline")
	}
	for {
		var claimed string
		err := s.control.db.QueryRowContext(ctx, `INSERT INTO console_sub_auth_locks(account_id,token,expires_at) VALUES($1,$2,now()+$3*interval '1 millisecond') ON CONFLICT(account_id) DO UPDATE SET token=excluded.token,expires_at=excluded.expires_at WHERE console_sub_auth_locks.expires_at<now() RETURNING token`, id, token, time.Until(deadline).Milliseconds()+5000).Scan(&claimed)
		if err == nil {
			return func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_, _ = s.control.db.ExecContext(cleanup, `DELETE FROM console_sub_auth_locks WHERE account_id=$1 AND token=$2`, id, token)
			}, nil
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Service) subAccountBalance(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	owner, _ := s.controlUser(r)
	id := r.PathValue("id")
	var base, email, encrypted string
	var raw []byte
	err := s.control.db.QueryRowContext(ctx, `SELECT base,email,encrypted_auth,balance FROM console_sub_accounts WHERE id=$1 AND owner=$2`, id, owner).Scan(&base, &email, &encrypted, &raw)
	if err != nil {
		http.Error(w, "账号不存在", 404)
		return
	}
	cached := subAccountBalance{Status: "missing"}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cached)
	}
	if cached.CheckedAt > time.Now().Add(-5*time.Minute).Unix() {
		writeJSON(w, 200, cached)
		return
	}
	unlock, err := s.subLockAuth(ctx, id)
	if err != nil {
		cached.Status = "error"
		writeJSON(w, 200, cached)
		return
	}
	defer unlock()
	// Re-read after obtaining the lock so concurrent requests reuse this result.
	err = s.control.db.QueryRowContext(ctx, `SELECT encrypted_auth,balance FROM console_sub_accounts WHERE id=$1 AND owner=$2`, id, owner).Scan(&encrypted, &raw)
	if err != nil {
		http.Error(w, "账号不存在", 404)
		return
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cached)
	}
	if cached.CheckedAt > time.Now().Add(-5*time.Minute).Unix() {
		writeJSON(w, 200, cached)
		return
	}
	var auth subAuth
	plain, err := s.control.decrypt(encrypted)
	if err == nil {
		err = json.Unmarshal([]byte(plain), &auth)
	}
	var profile struct {
		Email   string   `json:"email"`
		Balance *float64 `json:"balance"`
	}
	if err == nil {
		err = subJSON(ctx, subHTTP, base, "GET", "/api/v1/auth/me", auth.Access, nil, &profile, "")
		var remote *subRemoteError
		if errors.As(err, &remote) && remote.Status == 401 && auth.Refresh != "" {
			var next subAuth
			err = subJSON(ctx, subHTTP, base, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": auth.Refresh}, &next, "")
			if err == nil && (next.Access == "" || next.Refresh == "") {
				err = errors.New("invalid session")
			}
			if err == nil {
				next.ExpiresAt = time.Now().Add(time.Duration(next.ExpiresIn) * time.Second).Unix()
				encoded, _ := json.Marshal(next)
				var enc string
				enc, err = s.control.encrypt(string(encoded))
				if err == nil {
					var saved sql.Result
					saved, err = s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET encrypted_auth=$3 WHERE id=$1 AND encrypted_auth=$2`, id, encrypted, enc)
					if err == nil {
						if n, _ := saved.RowsAffected(); n != 1 {
							err = errors.New("session changed")
						} else {
							encrypted = enc
						}
					}
				}
				if err == nil {
					err = subJSON(ctx, subHTTP, base, "GET", "/api/v1/auth/me", next.Access, nil, &profile, "")
				}
			}
		}
	}
	if err != nil || !strings.EqualFold(profile.Email, email) {
		cached.Status = "error"
		writeJSON(w, 200, cached)
		return
	}
	fresh := subAccountBalance{Amount: profile.Balance, CheckedAt: time.Now().Unix(), Status: "ok"}
	if fresh.Amount == nil {
		fresh.Status = "missing"
	}
	encoded, _ := json.Marshal(fresh)
	_, err = s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET balance=$3 WHERE id=$1 AND encrypted_auth=$2`, id, encrypted, string(encoded))
	if err != nil {
		cached.Status = "error"
		writeJSON(w, 200, cached)
		return
	}
	writeJSON(w, 200, fresh)
}

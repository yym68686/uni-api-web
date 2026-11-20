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
	Amount          *float64 `json:"amount"`
	CheckedAt       int64    `json:"checked_at"`
	Status          string   `json:"status"`
	ProviderKind    string   `json:"provider_kind,omitempty"`
	ErrorReason     string   `json:"error_reason,omitempty"`
	RefreshRequired bool     `json:"refresh_required,omitempty"`
}

var errBalanceLoginRequired = errors.New("balance session must be renewed interactively")

func subBalanceErrorReason(err error) string {
	var remote *subRemoteError
	switch {
	case errors.Is(err, errBalanceLoginRequired):
		return "login_required"
	case errors.As(err, &remote):
		switch {
		case remote.requiresBrowser():
			return "verification_required"
		case remote.Status == http.StatusUnauthorized:
			return "login_required"
		case remote.Status == http.StatusForbidden:
			return "access_denied"
		case remote.Status == http.StatusTooManyRequests:
			return "rate_limited"
		}
	}
	return ""
}

func (b subAccountBalance) fresh() bool {
	return !b.RefreshRequired && b.CheckedAt > time.Now().Add(-5*time.Minute).Unix()
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
	var base, email, encrypted, encryptedPassword, providerKind string
	var raw []byte
	err := s.control.db.QueryRowContext(ctx, `SELECT base,email,encrypted_auth,encrypted_login_password,balance,provider_kind FROM console_sub_accounts WHERE id=$1 AND owner=$2`, id, owner).Scan(&base, &email, &encrypted, &encryptedPassword, &raw, &providerKind)
	if err != nil {
		http.Error(w, "账号不存在", 404)
		return
	}
	cached := subAccountBalance{Status: "missing"}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cached)
	}
	// Include the verified account's adapter even for older cached snapshots.
	// The observation page can then distinguish a missing New API usage route
	// from an unavailable wallet without guessing from a hostname or balance.
	cached.ProviderKind = providerKind
	fail := func(err error) {
		cached.Status = "error"
		cached.ErrorReason = subBalanceErrorReason(err)
		writeJSON(w, 200, cached)
	}
	if cached.fresh() {
		writeJSON(w, 200, cached)
		return
	}
	if providerKind == "newapi" {
		var profile struct {
			Email   string   `json:"email"`
			Balance *float64 `json:"balance"`
		}
		if err := s.newAPIAdapterCall(ctx, id, base, "GET", "/api/v1/auth/me", nil, &profile); err != nil || !strings.EqualFold(profile.Email, email) {
			fail(err)
			return
		}
		fresh := subAccountBalance{Amount: profile.Balance, CheckedAt: time.Now().Unix(), Status: "ok", ProviderKind: providerKind}
		if fresh.Amount == nil {
			fresh.Status = "missing"
		}
		encoded, _ := json.Marshal(fresh)
		if _, err := s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET balance=$2 WHERE id=$1 AND owner=$3 AND base=$4`, id, string(encoded), owner, base); err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, fresh)
		return
	}
	unlock, err := s.subLockAuth(ctx, id)
	if err != nil {
		fail(err)
		return
	}
	defer unlock()
	// Re-read after obtaining the lock so concurrent requests reuse this result.
	err = s.control.db.QueryRowContext(ctx, `SELECT encrypted_auth,encrypted_login_password,balance FROM console_sub_accounts WHERE id=$1 AND owner=$2 AND base=$3`, id, owner, base).Scan(&encrypted, &encryptedPassword, &raw)
	if err != nil {
		http.Error(w, "账号不存在", 404)
		return
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cached)
	}
	cached.ProviderKind = providerKind
	if cached.fresh() {
		writeJSON(w, 200, cached)
		return
	}
	var auth subAuth
	plain, err := s.control.decrypt(encrypted)
	if err == nil {
		err = json.Unmarshal([]byte(plain), &auth)
	}
	if err != nil {
		fail(errBalanceLoginRequired)
		return
	}
	var profile struct {
		Email   string   `json:"email"`
		Balance *float64 `json:"balance"`
	}
	readProfile := func(access string) error {
		return subJSON(ctx, subHTTP, base, "GET", "/api/v1/auth/me", access, nil, &profile, "")
	}
	if auth.Access == "" {
		err = errBalanceLoginRequired
	} else {
		err = readProfile(auth.Access)
	}
	if subBalanceErrorReason(err) == "login_required" {
		next, renewErr := renewSubBalanceSession(ctx, base, email, encryptedPassword, auth, s.control.decrypt)
		if renewErr != nil {
			fail(renewErr)
			return
		}
		if err = readProfile(next.Access); err != nil || !strings.EqualFold(profile.Email, email) {
			fail(err)
			return
		}
		next.ExpiresAt = time.Now().Add(time.Duration(next.ExpiresIn) * time.Second).Unix()
		encoded, _ := json.Marshal(next)
		enc, e := s.control.encrypt(string(encoded))
		if e != nil {
			fail(e)
			return
		}
		saved, e := s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET encrypted_auth=$3 WHERE id=$1 AND encrypted_auth=$2`, id, encrypted, enc)
		if e != nil {
			fail(e)
			return
		}
		if n, e := saved.RowsAffected(); e != nil || n != 1 {
			fail(e)
			return
		}
		encrypted = enc
	}
	if err != nil || !strings.EqualFold(profile.Email, email) {
		fail(err)
		return
	}
	fresh := subAccountBalance{Amount: profile.Balance, CheckedAt: time.Now().Unix(), Status: "ok", ProviderKind: providerKind}
	if fresh.Amount == nil {
		fresh.Status = "missing"
	}
	encoded, _ := json.Marshal(fresh)
	saved, err := s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET balance=$3 WHERE id=$1 AND encrypted_auth=$2`, id, encrypted, string(encoded))
	if err != nil {
		fail(err)
		return
	}
	if n, e := saved.RowsAffected(); e != nil || n != 1 {
		fail(e)
		return
	}
	writeJSON(w, 200, fresh)
}

func renewSubBalanceSession(ctx context.Context, base, email, encryptedPassword string, auth subAuth, decrypt func(string) (string, error)) (subAuth, error) {
	if auth.Refresh != "" {
		var next subAuth
		err := subJSON(ctx, subHTTP, base, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": auth.Refresh}, &next, "")
		if err == nil && next.Access != "" {
			// Some sub2api deployments rotate only the access token.
			if next.Refresh == "" {
				next.Refresh = auth.Refresh
			}
			return next, nil
		}
		if err != nil {
			var remote *subRemoteError
			// Do not turn a challenge, network error or rate limit on refresh
			// into another auth request. Only rejected credentials use login.
			if !errors.As(err, &remote) || remote.requiresBrowser() ||
				(remote.Status != http.StatusUnauthorized && remote.Status != http.StatusBadRequest) {
				return subAuth{}, err
			}
		}
	}
	if encryptedPassword == "" {
		return subAuth{}, errBalanceLoginRequired
	}
	password, err := decrypt(encryptedPassword)
	if err != nil || password == "" {
		return subAuth{}, errBalanceLoginRequired
	}
	var next subAuth
	if err := subJSON(ctx, subHTTP, base, "POST", "/api/v1/auth/login", "", map[string]string{"email": email, "password": password}, &next, ""); err != nil {
		return subAuth{}, err
	}
	if next.Requires2FA || next.Access == "" {
		return subAuth{}, errBalanceLoginRequired
	}
	return next, nil
}

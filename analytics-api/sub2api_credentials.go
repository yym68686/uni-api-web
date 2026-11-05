package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

// Read one account's password only when its owner opens the login form. Account
// lists and summaries continue to expose availability without credential values.
func (s *Service) subAccountCredentials(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var encrypted string
	err = s.control.db.QueryRowContext(ctx, `SELECT encrypted_login_password FROM console_sub_accounts WHERE id=$1 AND owner=$2`, r.PathValue("id"), owner).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "账号不存在", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "账号密码暂不可用，请稍后重试", http.StatusServiceUnavailable)
		return
	}
	password := ""
	if encrypted != "" {
		password, err = s.control.decrypt(encrypted)
		if err != nil {
			http.Error(w, "已保存密码无法读取，请重新输入密码", http.StatusServiceUnavailable)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"password": password})
}

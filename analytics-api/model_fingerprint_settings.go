package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
)

// Freeze the pre-settings catalog for users without a saved preference.
// Newly published profiles always require an explicit opt-in.
var defaultAutomaticFingerprintModels = []string{
	"deepseek-v4.1-flash-1017", "gemini-3.1-pro", "gemini-3.8-flash",
	"gemini-4-argon", "gemini-4-carbon", "gpt-5.4", "gpt-5.5",
	"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-6-astra",
	"gpt-6-sol", "gpt-6.1-sol", "gpt-6.2-astra", "gpt-6.2-sol",
}

type fingerprintPreferences struct {
	Models []string `json:"enabled_models"`
}

func (s *Service) fingerprintSettings(w http.ResponseWriter, r *http.Request) {
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "请先登录", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	catalog := currentFingerprintCatalog()
	var prefs fingerprintPreferences
	if r.Method == http.MethodPut {
		if !decodeConfiguration(w, r, &prefs) {
			return
		}
		if prefs.Models == nil || len(prefs.Models) > len(catalog.names) {
			http.Error(w, "请选择当前目录支持的模型", http.StatusBadRequest)
			return
		}
		slices.Sort(prefs.Models)
		prefs.Models = slices.Compact(prefs.Models)
		for _, model := range prefs.Models {
			if _, supported := catalog.models[model]; !supported {
				http.Error(w, "检测目录已变化，请刷新后重新保存", http.StatusBadRequest)
				return
			}
		}
		_, err = s.control.db.ExecContext(ctx, `INSERT INTO console_fingerprint_preferences(owner,models) VALUES($1,$2)
 ON CONFLICT(owner) DO UPDATE SET models=excluded.models`, owner, mustJSON(prefs.Models))
	} else {
		var raw []byte
		err = s.control.db.QueryRowContext(ctx, `SELECT models FROM console_fingerprint_preferences WHERE owner=$1`, owner).Scan(&raw)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			prefs.Models = slices.Clone(defaultAutomaticFingerprintModels)
			err = nil
		case err == nil:
			err = json.Unmarshal(raw, &prefs.Models)
		}
	}
	if err != nil {
		http.Error(w, "指纹检测设置暂不可用", http.StatusServiceUnavailable)
		return
	}
	prefs.Models = slices.DeleteFunc(prefs.Models, func(model string) bool { _, ok := catalog.models[model]; return !ok })
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		fingerprintPreferences
		Supported []string `json:"supported_models"`
		Commit    string   `json:"reference_commit"`
	}{prefs, catalog.names, catalog.commit})
}

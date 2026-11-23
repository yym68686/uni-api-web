package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type configuredCheckScope struct {
	Source   string `json:"source_id"`
	Provider string `json:"provider"`
}

type configuredActiveCheck struct {
	configuredCheckScope
	Kind        string `json:"kind"`
	Model       string `json:"model"`
	Fingerprint string `json:"fingerprint"`
}

// Read only active check identities for the visible channels. Historical
// results, queue messages and upstream requests are not needed to detect completion.
func (s *Service) configuredCheckProgress(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("targets")
	var scopes []configuredCheckScope
	if len(raw) > 6000 || json.Unmarshal([]byte(raw), &scopes) != nil || len(scopes) == 0 || len(scopes) > 128 {
		http.Error(w, "检测进度范围无效", http.StatusBadRequest)
		return
	}
	for _, scope := range scopes {
		if scope.Source == "" || scope.Provider == "" {
			http.Error(w, "检测进度范围无效", http.StatusBadRequest)
			return
		}
	}
	rows, err := s.control.db.QueryContext(r.Context(), `SELECT c.source_id,c.provider,c.kind,c.model,c.fingerprint
 FROM (SELECT DISTINCT * FROM jsonb_to_recordset($1::jsonb) AS w(source_id text,provider text)) w
 JOIN console_sources s ON s.id=w.source_id AND s.enabled
 JOIN console_configured_checks c ON c.source_id=w.source_id AND c.provider=w.provider
 WHERE c.state='queued' OR (c.state='running' AND (c.deadline IS NULL OR c.deadline>=now()))`, raw)
	if err != nil {
		log.Printf("configured_check_progress read failed class=%s", databaseErrorClass(err))
		http.Error(w, "检测进度读取失败", http.StatusServiceUnavailable)
		return
	}
	defer rows.Close()
	active := []configuredActiveCheck{}
	for rows.Next() {
		var c configuredActiveCheck
		if err = rows.Scan(&c.Source, &c.Provider, &c.Kind, &c.Model, &c.Fingerprint); err != nil {
			break
		}
		active = append(active, c)
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		log.Printf("configured_check_progress read failed class=%s", databaseErrorClass(err))
		http.Error(w, "检测进度读取失败", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"data": active})
}

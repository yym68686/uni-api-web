package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Only hashes are retained. Bodies remain request-local and authorization is
// checked by the normal handler before every conditional/delta response.
type summaryHistory struct {
	mu       sync.Mutex
	versions map[string]summaryVersion
}
type summaryVersion struct {
	created time.Time
	hashes  map[string]string
}
type summaryRow struct {
	ID    string `json:"id"`
	Value any    `json:"value"`
}

func summaryView(r *http.Request) string {
	if view := r.Header.Get("X-Console-View"); view != "" {
		return view
	}
	return r.URL.Query().Get("view")
}

func (s *Service) writeSummary(w http.ResponseWriter, r *http.Request, rows []summaryRow) {
	owner, err := s.controlUser(r)
	if err != nil {
		http.Error(w, "请重新登录", http.StatusUnauthorized)
		return
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	hashes := make(map[string]string, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		hashes[row.ID] = tokenHash(mustJSON(row.Value))
		order = append(order, row.ID)
	}
	version := tokenHash(mustJSON(hashes))
	scope := owner + "\n" + r.URL.Path + "\n" + summaryView(r) + "\n" + r.URL.RawQuery + "\n"
	previous := r.Header.Get("X-Console-Since")
	h := &s.summaries
	h.mu.Lock()
	if h.versions == nil {
		h.versions = map[string]summaryVersion{}
	}
	old, exists := h.versions[scope+previous]
	if time.Since(old.created) > 10*time.Minute {
		exists = false
	}
	// Bounded history; a restart or evicted cursor sends a complete summary.
	for key, v := range h.versions {
		if time.Since(v.created) > 10*time.Minute {
			delete(h.versions, key)
		}
	}
	if len(h.versions) >= 64 {
		oldestKey := ""
		var oldest time.Time
		for key, v := range h.versions {
			if oldestKey == "" || v.created.Before(oldest) {
				oldestKey = key
				oldest = v.created
			}
		}
		delete(h.versions, oldestKey)
	}
	if len(rows) <= 20000 {
		h.versions[scope+version] = summaryVersion{created: time.Now(), hashes: hashes}
	}
	h.mu.Unlock()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Vary", "X-Console-Since, X-Console-View")
	w.Header().Set("ETag", `"`+version+`"`)
	if previous == version {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	changed := rows
	removed := []string{}
	base := ""
	if exists {
		base = previous
		changed = []summaryRow{}
		for _, row := range rows {
			if hashes[row.ID] != old.hashes[row.ID] {
				changed = append(changed, row)
			}
		}
		for id := range old.hashes {
			if _, ok := hashes[id]; !ok {
				removed = append(removed, id)
			}
		}
		sort.Strings(removed)
	}
	writeJSON(w, 200, map[string]any{"format": "summary-v1", "version": version, "base_version": base, "data": changed, "removed": removed, "order": order})
}

// Keep every scalar needed by list filtering, prices, eligibility and timing.
// Probe replies and verbose receipts are fetched from the original scoped
// detail handler. Clone through JSON so shared quality-cache objects stay intact.
func compactSummary(value any) any {
	var data any
	_ = json.Unmarshal([]byte(mustJSON(value)), &data)
	var visit func(any)
	visit = func(v any) {
		switch item := v.(type) {
		case []any:
			for _, child := range item {
				visit(child)
			}
		case map[string]any:
			for key, child := range item {
				switch key {
				case "curl_token", "text", "id", "started_at", "request_ids", "request_id", "log_id", "created_at":
					delete(item, key)
				case "attempts":
					item[key] = []any{}
				case "message":
					if text, ok := child.(string); ok {
						r := []rune(text)
						if len(r) > 160 {
							item[key] = string(r[:160]) + "…"
						}
					}
				case "usage":
					if usage, ok := child.(map[string]any); ok {
						for field := range usage {
							if !strings.Contains("|status|input_price|output_price|input_tokens|output_tokens|", "|"+field+"|") {
								delete(usage, field)
							}
						}
					}
				default:
					visit(child)
				}
			}
		}
	}
	visit(data)
	return data
}

func (s *Service) writeAccountSummary(w http.ResponseWriter, r *http.Request, accounts []subAccount) {
	rows := []summaryRow{}
	for i, a := range accounts {
		targets := a.Targets
		a.Targets = []subTarget{}
		rows = append(rows, summaryRow{ID: a.ID, Value: map[string]any{"account": a, "position": i}})
		for j, t := range targets {
			rows = append(rows, summaryRow{ID: qualityGroupID(a.ID, t.GroupID), Value: map[string]any{"account_id": a.ID, "target": compactSummary(t), "position": j}})
		}
	}
	s.writeSummary(w, r, rows)
}

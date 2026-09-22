package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"time"
)

type routeMove struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Position int    `json:"position"`
}

// Move only selected providers while retaining the relative order of every
// other provider and the exact disabled policy. Apply all models atomically.
func applyRouteMoves(snapshot *retainedSnapshot, key string, catalog []routeMove, moves []routeMove) error {
	orders := map[string][]string{}
	for _, row := range catalog {
		seen := false
		for _, provider := range orders[row.Model] {
			if provider == row.Provider {
				seen = true
			}
		}
		if !seen {
			orders[row.Model] = append(orders[row.Model], row.Provider)
		}
	}
	byModel := map[string][]routeMove{}
	seen := map[string]bool{}
	for _, move := range moves {
		id := move.Provider + "\n" + move.Model
		found := false
		for _, p := range orders[move.Model] {
			if p == move.Provider {
				found = true
			}
		}
		if seen[id] || !found || move.Position < 1 || move.Position > len(orders[move.Model]) {
			return errors.New("模型或位置已变化，请重新读取路由后重试")
		}
		seen[id] = true
		byModel[move.Model] = append(byModel[move.Model], move)
	}
	for _, rows := range byModel {
		sort.Slice(rows, func(i, j int) bool { return rows[i].Position < rows[j].Position })
		for i := 1; i < len(rows); i++ {
			if rows[i].Position == rows[i-1].Position {
				return errors.New("同一模型的渠道位置不能重复")
			}
		}
	}
	for model, rows := range byModel {
		order := []string{}
		for _, p := range orders[model] {
			if !seen[p+"\n"+model] {
				order = append(order, p)
			}
		}
		final := make([]string, len(orders[model]))
		for _, move := range rows {
			final[move.Position-1] = move.Provider
		}
		n := 0
		for i := range final {
			if final[i] == "" {
				final[i] = order[n]
				n++
			}
		}
		found := false
		for i := range snapshot.Rules {
			rule := &snapshot.Rules[i]
			if rule.KeyID == key && rule.Model == model {
				rule.Order = final
				found = true
				break
			}
		}
		if !found {
			snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: key, Model: model, Order: final, Disabled: []string{}})
		}
	}
	return nil
}

func (s *Service) editChannelRoutes(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key      string      `json:"api_key_id"`
		Revision string      `json:"revision"`
		Moves    []routeMove `json:"moves"`
	}
	if !decodeControlLimit(w, r, &in, 512<<10) {
		return
	}
	if len(in.Moves) == 0 || len(in.Moves) > 1024 {
		http.Error(w, "请选择需要调整的模型", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	src, err := s.control.source(ctx, r.PathValue("id"))
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	key, err := subRawKey(src.ID, in.Key)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	unlock, err := s.control.lockControls(ctx, src.ID)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	defer unlock()
	state, err := s.reconcileControls(ctx, src)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	snapshot, code, err := s.importSnapshot(ctx, src, state, in.Revision)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	raw, _, err := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
	var catalog []routeMove
	if err != nil || decodeMap(raw["data"], &catalog) != nil {
		http.Error(w, "路由读取失败", 503)
		return
	}
	if err = applyRouteMoves(&snapshot, key, catalog, in.Moves); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	admin := src
	if src.ConfigKey != "" {
		admin.Key = src.ConfigKey
	}
	applied, code, err := subGateway(ctx, admin, "POST", "/v1/channel-controls/restore", map[string]any{"revision": in.Revision, "snapshot": snapshot})
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	if err = s.saveLiveControls(ctx, src, applied); err != nil {
		retentionFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"message": "各模型路由位置已保存"})
}

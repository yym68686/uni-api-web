package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maxChannelCatalogModels = 10000

type catalogModelState struct {
	Manual   bool `json:"manual,omitempty"`
	Observed bool `json:"observed,omitempty"`
}

type channelModelCatalogEntry struct {
	Model string `json:"model"`
	catalogModelState
}

type channelModelCatalogView struct {
	Models      []channelModelCatalogEntry `json:"models"`
	RefreshedAt *time.Time                 `json:"refreshed_at"`
}

func readChannelCatalog(row *sql.Row) (map[string]catalogModelState, *time.Time, error) {
	models := map[string]catalogModelState{}
	var raw []byte
	var refreshed *time.Time
	if err := row.Scan(&raw, &refreshed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models, nil, nil
		}
		return nil, nil, err
	}
	if err := json.Unmarshal(raw, &models); err != nil {
		return nil, nil, err
	}
	return models, refreshed, nil
}

func catalogModelValid(model string) bool {
	return validPublicModel(model) && validProbeModel(model)
}

// Refresh is a union, not a replace: previously observed and manually added
// IDs stay visible when a later discovery omits them. This data never feeds
// the gateway model mapping or the availability gate.
func mergeChannelCatalog(models map[string]catalogModelState, observed []string, manual string) {
	if manual != "" {
		state := models[manual]
		state.Manual = true
		models[manual] = state
		return
	}
	for model, state := range models {
		state.Observed = false
		models[model] = state
	}
	for _, model := range observed {
		if catalogModelValid(model) {
			state := models[model]
			state.Observed = true
			models[model] = state
		}
	}
}

func writeChannelCatalog(w http.ResponseWriter, models map[string]catalogModelState, refreshed *time.Time) {
	result := channelModelCatalogView{Models: make([]channelModelCatalogEntry, 0, len(models)), RefreshedAt: refreshed}
	for model, state := range models {
		result.Models = append(result.Models, channelModelCatalogEntry{Model: model, catalogModelState: state})
	}
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].Model < result.Models[j].Model })
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) channelModelCatalog(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider == "" || len(provider) > 256 || strings.ContainsAny(provider, "\r\n/\x00") || provider == "__uni_console_global_preferences__" {
		http.Error(w, "无效渠道", http.StatusBadRequest)
		return
	}
	src, err := s.settingsSource(r)
	if err != nil {
		http.Error(w, "来源不存在", http.StatusNotFound)
		return
	}
	var in struct {
		Revision string `json:"revision"`
		Action   string `json:"action"`
		Model    string `json:"model"`
	}
	if r.Method == http.MethodPost {
		if !decodeConfiguration(w, r, &in) {
			return
		}
		if in.Revision == "" || (in.Action != "refresh" && (in.Action != "add" || !catalogModelValid(in.Model))) ||
			(in.Action == "refresh" && in.Model != "") {
			http.Error(w, "无效模型目录操作", http.StatusBadRequest)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	view, status, err := s.settingsGateway(ctx, src, http.MethodGet, "/v1/channel-settings?"+url.Values{"provider": {provider}}.Encode(), nil)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	effective, _ := view["effective"].(map[string]any)
	base, _ := effective["base_url"].(string)
	if base == "" || view["provider"] != provider {
		http.Error(w, "无法确认当前渠道的上游地址", http.StatusBadGateway)
		return
	}
	// Repointing a source or provider must not display the previous upstream's
	// catalog. Key/order/model edits do not change this identity.
	identity := tokenHash(mustJSON([]any{src.Base, base, view["api_key_id"]}))
	const selectCatalog = `SELECT models,refreshed_at FROM console_channel_model_catalogs WHERE source_id=$1 AND provider=$2 AND upstream_identity=$3`
	args := []any{src.ID, provider, identity}
	if r.Method == http.MethodGet {
		models, refreshed, err := readChannelCatalog(s.control.db.QueryRowContext(ctx, selectCatalog, args...))
		if err != nil {
			http.Error(w, "模型目录读取失败", http.StatusServiceUnavailable)
			return
		}
		writeChannelCatalog(w, models, refreshed)
		return
	}
	if view["revision"] != in.Revision {
		http.Error(w, "渠道配置已变化，请重新读取后再操作", http.StatusConflict)
		return
	}
	var observed []string
	var started time.Time
	if in.Action == "refresh" {
		if err := s.control.db.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&started); err != nil {
			http.Error(w, "模型目录暂不可用", http.StatusServiceUnavailable)
			return
		}
		result, status, err := s.settingsGateway(ctx, src, http.MethodPost, "/v1/channel-settings/discover", channelSettingMutation{
			Revision:  in.Revision,
			Operation: "catalog-" + randomID(),
			Changes:   []channelSettingChange{{Provider: provider}},
		})
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		if decodeMap(result["models"], &observed) != nil || observed == nil {
			http.Error(w, "上游模型目录响应无效", http.StatusBadGateway)
			return
		}
	}
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "模型目录保存失败", http.StatusServiceUnavailable)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO console_channel_model_catalogs(source_id,provider,upstream_identity) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, args...); err != nil {
		http.Error(w, "模型目录保存失败", http.StatusServiceUnavailable)
		return
	}
	models, refreshed, err := readChannelCatalog(tx.QueryRowContext(ctx, selectCatalog+" FOR UPDATE", args...))
	if err != nil {
		http.Error(w, "模型目录保存失败", http.StatusServiceUnavailable)
		return
	}
	// Don't let a slower, older refresh supersede a newer one. Manual edits
	// always merge under the same row lock.
	if in.Action == "add" || refreshed == nil || started.After(*refreshed) {
		mergeChannelCatalog(models, observed, in.Model)
		if len(models) > maxChannelCatalogModels {
			http.Error(w, "模型目录超过容量上限，原目录未变更", http.StatusBadRequest)
			return
		}
		if in.Action == "refresh" {
			refreshed = &started
		}
		_, err = tx.ExecContext(ctx, `UPDATE console_channel_model_catalogs SET models=$4,refreshed_at=$5 WHERE source_id=$1 AND provider=$2 AND upstream_identity=$3`, src.ID, provider, identity, mustJSON(models), refreshed)
	}
	if err != nil || tx.Commit() != nil {
		http.Error(w, "模型目录保存失败", http.StatusServiceUnavailable)
		return
	}
	writeChannelCatalog(w, models, refreshed)
}

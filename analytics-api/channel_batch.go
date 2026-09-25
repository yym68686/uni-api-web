package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"time"
)

type channelBatchTarget struct {
	Key       string            `json:"api_key_id"`
	Provider  string            `json:"provider"`
	Origin    string            `json:"origin_provider"`
	Account   string            `json:"account_id"`
	Group     int64             `json:"group_id"`
	Current   map[string]string `json:"current"`
	Models    map[string]string `json:"models"`
	Positions map[string]int    `json:"positions"`
}
type channelBatchInput struct {
	Revision        string               `json:"revision"`
	Part            string               `json:"part"`
	AllowUnverified bool                 `json:"allow_unverified_models"`
	Targets         []channelBatchTarget `json:"targets"`
}
type batchCatalogRow struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Upstream string `json:"upstream_model"`
}

// Independent sources commit in parallel. All keys of one source share one
// lock, one revision, and one atomic restore; never race full-snapshot writes.
func (s *Service) applyChannelBatch(w http.ResponseWriter, r *http.Request) {
	// Override the server's short default only for this bounded batch operation.
	// A dropped response after restore is ambiguous, so allow time for retention.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(115 * time.Second))
	var in channelBatchInput
	if !decodeControlLimit(w, r, &in, 2<<20) {
		return
	}
	if in.Revision == "" || len(in.Targets) == 0 || len(in.Targets) > 1024 {
		http.Error(w, "请选择接入和有效配置版本", 400)
		return
	}
	switch in.Part {
	case "all", "models", "aliases", "positions", "delete":
	default:
		http.Error(w, "无效的批量操作", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 110*time.Second)
	defer cancel()
	src, err := s.control.source(ctx, r.PathValue("id"))
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	owner, _ := s.controlUser(r)
	seen := map[string]bool{}
	keys := map[string]bool{}
	for i := range in.Targets {
		t := &in.Targets[i]
		t.Key, err = subRawKey(src.ID, t.Key)
		if err != nil || t.Provider == "" || seen[t.Key+"\n"+t.Provider] {
			http.Error(w, "接入无效或重复", 400)
			return
		}
		seen[t.Key+"\n"+t.Provider] = true
		keys[t.Key] = true
		if t.Account != "" {
			var owned bool
			err = s.control.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_sub_accounts a JOIN console_sub_targets t ON t.account_id=a.id WHERE a.id=$1 AND t.group_id=$2 AND a.owner=$3)`, t.Account, t.Group, owner).Scan(&owned)
			if err != nil || !owned || t.Provider != subProviderName(t.Account, t.Group, t.Key) {
				http.Error(w, "分组不存在或接入不匹配", 404)
				return
			}
		} else if t.Origin == "" || (t.Provider != t.Origin && t.Provider != configuredImportName(t.Origin, t.Key)) {
			http.Error(w, "接入不属于所选渠道", 400)
			return
		}
		if in.Part != "delete" {
			public, e := importPublicModels(nil, t.Models)
			if e != nil || validateModelPositions(public, t.Positions) != nil ||
				len(t.Positions) == 0 || (in.Part != "positions" && len(t.Positions) != len(public)) {
				http.Error(w, "模型或位置无效", 400)
				return
			}
		}
	}
	lockCtx, stopWaiting := context.WithTimeout(ctx, 15*time.Second)
	unlock, err := s.control.waitControls(lockCtx, src.ID)
	stopWaiting()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "渠道配置仍在同步，等待超时；尚未提交修改，请稍后重试", 409)
		} else {
			http.Error(w, "无法取得渠道配置锁；尚未提交修改，请稍后重试", 503)
		}
		return
	}
	defer unlock()
	state, err := s.reconcileControls(ctx, src)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if state["revision"] != in.Revision {
		http.Error(w, "配置已变化，请核对后继续", 409)
		return
	}
	snapshot, code, err := s.importSnapshot(ctx, src, state, in.Revision)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}

	// Read one effective catalog per key, with bounded parallel I/O.
	catalogs := map[string][]batchCatalogRow{}
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for key := range keys {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				firstErr = ctx.Err()
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			raw, _, e := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
			var rows []batchCatalogRow
			if e == nil {
				e = decodeMap(raw["data"], &rows)
			}
			excluded := configuredExcluded(state, key)
			visible := []batchCatalogRow{}
			for _, row := range rows {
				if !excluded[row.Provider] {
					visible = append(visible, row)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				firstErr = e
			} else {
				catalogs[key] = visible
			}
		}(key)
	}
	wg.Wait()
	if firstErr != nil {
		http.Error(w, "来源路由读取失败，未提交修改", 503)
		return
	}

	// Read shared definitions only once. Existing copies come from the exported
	// snapshot, retaining every header, request policy, credential and setting.
	baseDocuments := map[string]map[string]any{}
	needBase := false
	for _, t := range in.Targets {
		if in.Part != "positions" && in.Part != "delete" && t.Account == "" {
			needBase = true
		}
	}
	if needBase {
		admin := src
		if src.ConfigKey != "" {
			admin.Key = src.ConfigKey
		}
		raw, status, e := subGateway(ctx, admin, "GET", "/v1/api_config", nil)
		if e != nil {
			http.Error(w, e.Error(), status)
			return
		}
		var cfg struct {
			Providers []map[string]any `json:"providers"`
		}
		if decodeMap(raw["api_config"], &cfg) != nil {
			http.Error(w, "来源配置无效", 502)
			return
		}
		for _, doc := range cfg.Providers {
			if p, ok := doc["provider"].(string); ok {
				baseDocuments[p] = doc
			}
		}
	}
	documents := map[string]map[string]any{}
	type validationScope struct {
		account  string
		group    int64
		provider string
	}
	neededByScope := map[validationScope]map[string]string{}
	for _, t := range in.Targets {
		if in.Part == "positions" || in.Part == "delete" {
			continue
		}
		doc, status, e := s.importProviderDocument(ctx, src, snapshot, t.Provider, in.Revision, baseDocuments)
		if e != nil {
			http.Error(w, e.Error(), status)
			return
		}
		documents[t.Key+"\n"+t.Provider] = doc
		scope := validationScope{t.Account, t.Group, t.Origin}
		for model := range newChannelModels(t.Current, t.Models) {
			if neededByScope[scope] == nil {
				neededByScope[scope] = map[string]string{}
			}
			neededByScope[scope][model] = model
		}
	}
	// Validate each provider/group once, not once per caller key. This retains
	// the batched write path's bounded I/O when a channel has many bindings.
	for scope, models := range neededByScope {
		if scope.account != "" {
			err = s.validateSiteModelChanges(ctx, scope.account, scope.group, nil, models)
		} else {
			err = s.validateConfiguredModelChanges(ctx, src, scope.provider, owner, nil, models)
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	changed, err := buildBatchSnapshot(&snapshot, in, catalogs, documents)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	applied := state
	if changed {
		admin := src
		if src.ConfigKey != "" {
			admin.Key = src.ConfigKey
		}
		applied, code, err = subGateway(ctx, admin, "POST", "/v1/channel-controls/restore", map[string]any{"revision": in.Revision, "snapshot": snapshot})
		if err != nil {
			http.Error(w, err.Error(), code)
			return
		}
	}
	if err = s.saveLiveControls(ctx, src, applied); err != nil {
		retentionFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"revision": applied["revision"], "changed": changed})
}

// Models are canonical upstream names, not names to resolve through another
// source's alias table. Validate all bindings before changing the snapshot.
func buildBatchSnapshot(snapshot *retainedSnapshot, in channelBatchInput, catalogs map[string][]batchCatalogRow, documents map[string]map[string]any) (bool, error) {
	noop := map[string]bool{}
	for _, t := range in.Targets {
		actual := map[string]string{}
		positions := map[string]int{}
		counts := map[string]int{}
		seen := map[string]bool{}
		for _, row := range catalogs[t.Key] {
			id := row.Provider + "\n" + row.Model
			if seen[id] {
				continue
			}
			seen[id] = true
			counts[row.Model]++
			if row.Provider != t.Provider {
				continue
			}
			up := row.Upstream
			if up == "" {
				up = row.Model
			}
			actual[row.Model] = up
			positions[row.Model] = counts[row.Model]
		}
		if len(actual) == 0 || !reflect.DeepEqual(actual, t.Current) {
			return false, errors.New("渠道模型已变化，请重新核对")
		}
		if in.Part == "positions" && !reflect.DeepEqual(actual, t.Models) {
			return false, errors.New("调整位置不能修改模型")
		}
		same := in.Part != "delete" && reflect.DeepEqual(actual, t.Models)
		for m, pos := range t.Positions {
			same = same && positions[m] == pos
		}
		noop[t.Key+"\n"+t.Provider] = same
	}
	moves := map[string][]routeMove{}
	changedKeys := map[string]bool{}
	changed := false
	for _, t := range in.Targets {
		id := t.Key + "\n" + t.Provider
		if noop[id] {
			// Matching positions are anchors when another provider of the same
			// model moves around them. Preserve them without replacing definitions.
			for model, pos := range t.Positions {
				moves[t.Key] = append(moves[t.Key], routeMove{Provider: t.Provider, Model: model, Position: pos})
			}
			continue
		}
		changed = true
		changedKeys[t.Key] = true
		provider := t.Provider
		if in.Part == "delete" {
			if err := removeConfiguredFromSnapshot(snapshot, t.Key, provider, t.Account == ""); err != nil {
				return false, err
			}
		} else if in.Part != "positions" {
			doc := documents[id]
			if doc == nil {
				return false, errors.New("渠道定义不完整")
			}
			if t.Account == "" && t.Provider == t.Origin {
				provider = configuredImportName(t.Origin, t.Key)
				for _, c := range snapshot.Channels {
					if c.Provider == provider {
						return false, errors.New("渠道独立配置已存在，请刷新")
					}
				}
				if err := removeConfiguredFromSnapshot(snapshot, t.Key, t.Provider, true); err != nil {
					return false, err
				}
			}
			public, err := importPublicModels(nil, t.Models)
			if err != nil {
				return false, err
			}
			doc["provider"] = provider
			doc["model"] = importModelDefinition(nil, t.Models)
			raw, _ := json.Marshal(doc)
			base, _ := doc["base_url"].(string)
			secret := "__full_definition__"
			if ks := providerKeys(doc["api"]); len(ks) > 0 {
				secret = ks[0]
			}
			for i := range snapshot.Rules {
				r := &snapshot.Rules[i]
				if r.Model != "" {
					if _, kept := t.Models[r.Model]; !kept {
						r.Order = withoutProvider(r.Order, provider)
						r.Disabled = withoutProvider(r.Disabled, provider)
					}
				}
			}
			next := []retainedChannel{}
			for _, old := range snapshot.Channels {
				if old.Provider == provider && old.KeyID != t.Key {
					return false, errors.New("渠道不属于当前 API key")
				}
				if old.Provider != provider {
					next = append(next, old)
				}
			}
			snapshot.Channels = append(next, retainedChannel{Provider: provider, KeyID: t.Key, Base: base, Key: secret, Models: public, Definition: raw})
			delete(snapshot.Settings, provider)
		}
		if in.Part != "positions" {
			rows := []batchCatalogRow{}
			for _, row := range catalogs[t.Key] {
				if row.Provider != t.Provider {
					rows = append(rows, row)
				}
			}
			if in.Part != "delete" {
				for model, up := range t.Models {
					rows = append(rows, batchCatalogRow{Provider: provider, Model: model, Upstream: up})
				}
			}
			catalogs[t.Key] = rows
		}
		if in.Part != "delete" {
			for model, pos := range t.Positions {
				moves[t.Key] = append(moves[t.Key], routeMove{Provider: provider, Model: model, Position: pos})
			}
		}
	}
	for key, ms := range moves {
		if !changedKeys[key] {
			continue
		}
		rows := []routeMove{}
		for _, row := range catalogs[key] {
			rows = append(rows, routeMove{Provider: row.Provider, Model: row.Model})
		}
		if err := applyRouteMoves(snapshot, key, rows, ms); err != nil {
			return false, err
		}
	}
	return changed, nil
}

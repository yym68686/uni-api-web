package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

type managementChannel struct {
	subInstalledChannel
	Engine           string   `json:"engine"`
	AccountIDs       []string `json:"account_ids"`
	ProbeFingerprint string   `json:"probe_fingerprint"`
}

type managementModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Upstream string `json:"upstream_model"`
	Engine   string `json:"engine"`
}
type managementProvider struct {
	Base             string `json:"base"`
	Fingerprint      string `json:"fingerprint"`
	ProbeFingerprint string `json:"probe_fingerprint"`
}
type managementSnapshot struct {
	Rows      []managementModel             `json:"rows"`
	Providers map[string]managementProvider `json:"providers"`
	Copies    map[string]bool               `json:"copies"`
	CheckedAt int64                         `json:"checked_at"`
	Identity  string                        `json:"identity"`
}

func loadManagementSnapshot(ctx context.Context, src controlSource) (managementSnapshot, error) {
	var catalog map[string]any
	var providers []configuredProvider
	var controls map[string]any
	var catalogErr error
	var readers sync.WaitGroup
	readers.Add(3)
	go func() {
		defer readers.Done()
		catalog, _, catalogErr = fetchSource(ctx, src, "/v1/model-channels", url.Values{"endpoint": {"all"}, "stream": {"all"}})
	}()
	go func() { defer readers.Done(); providers, _ = configuredProviders(ctx, src) }()
	go func() {
		defer readers.Done()
		controls, _, _ = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	}()
	readers.Wait()
	result := managementSnapshot{Rows: []managementModel{}, Providers: map[string]managementProvider{}, Copies: map[string]bool{}, CheckedAt: time.Now().Unix(), Identity: controlTarget(src)}
	if catalogErr != nil {
		return result, catalogErr
	}
	if err := decodeMap(catalog["data"], &result.Rows); err != nil {
		return result, err
	}
	for _, p := range providers {
		hashes, _ := json.Marshal(configuredKeyHashes(p))
		result.Providers[p.Provider] = managementProvider{
			Base:             subBindingSite(p.Base),
			Fingerprint:      tokenHash(controlTarget(src) + "\n" + p.Provider + "\n" + subBindingSite(p.Base) + "\n" + string(hashes)),
			ProbeFingerprint: configuredProbeFingerprint(src, p),
		}
	}
	var live retainedLive
	_ = decodeMap(controls, &live)
	for _, c := range live.Channels {
		for _, p := range providers {
			if c.Provider == configuredImportName(p.Provider, c.KeyID) {
				result.Copies[c.Provider] = true
				break
			}
		}
	}
	return result, nil
}

func (s *Service) saveManagementSnapshot(ctx context.Context, source string, snapshot managementSnapshot) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = s.control.db.ExecContext(ctx, `INSERT INTO console_channel_management_snapshots(source_id,catalog,checked_at) VALUES($1,$2,$3)
 ON CONFLICT(source_id) DO UPDATE SET catalog=excluded.catalog,checked_at=excluded.checked_at`, source, string(raw), snapshot.CheckedAt)
	return err
}

func (s *Service) managementSnapshots(ctx context.Context) (map[string]managementSnapshot, error) {
	rows, err := s.control.db.QueryContext(ctx, `SELECT source_id,catalog FROM console_channel_management_snapshots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]managementSnapshot{}
	for rows.Next() {
		var source string
		var raw []byte
		if err = rows.Scan(&source, &raw); err != nil {
			return nil, err
		}
		var snapshot managementSnapshot
		if err = json.Unmarshal(raw, &snapshot); err != nil {
			return nil, err
		}
		out[source] = snapshot
	}
	return out, rows.Err()
}

// Read the gateway catalog, not historical traffic: unused configured channels
// must be manageable too. Account enrichment uses the durable key bindings.
func (s *Service) channelManagement(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	bindings, err := s.configuredBindings(r.Context(), owner)
	if err != nil {
		http.Error(w, "渠道绑定读取失败", 503)
		return
	}
	accounts, err := s.bindingAccounts(r.Context())
	if err != nil {
		http.Error(w, "账号读取失败", 503)
		return
	}
	sources, err := s.control.listSources(r.Context())
	if err != nil {
		http.Error(w, "来源读取失败", 503)
		return
	}
	snapshots, err := s.managementSnapshots(r.Context())
	if err != nil {
		http.Error(w, "渠道目录读取失败", 503)
		return
	}
	lookup := map[string]subInstalledChannel{}
	for _, binding := range bindings {
		lookup[binding.SourceID+"\n"+binding.Provider] = binding
	}
	data := []managementChannel{}
	unavailable := []string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, source := range sources {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-r.Context().Done():
				return
			}
			defer func() { <-slots }()
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			src, e := s.control.source(ctx, id)
			snapshot, cached := snapshots[id]
			cached = cached && snapshot.Identity == controlTarget(src)
			if e == nil && !cached {
				snapshot, e = loadManagementSnapshot(ctx, src)
			}
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				unavailable = append(unavailable, source.Name)
				return
			}
			if cached && time.Since(time.Unix(snapshot.CheckedAt, 0)) > 5*time.Minute {
				unavailable = append(unavailable, source.Name)
			}
			grouped := map[string]*managementChannel{}
			for _, row := range snapshot.Rows {
				if snapshot.Copies[row.Provider] {
					continue
				}
				item := grouped[row.Provider]
				if item == nil {
					binding, found := lookup[id+"\n"+row.Provider]
					if !found {
						binding = subInstalledChannel{Kind: "configured", SourceID: id, SourceName: src.Name, Provider: row.Provider, Name: row.Provider, Models: []string{}, Positions: map[string]int{}}
					}
					if live, ok := snapshot.Providers[row.Provider]; ok {
						// An edited credential must not inherit the previous account's
						// results while the durable binding worker catches up.
						if found && live.Fingerprint != binding.Fingerprint {
							binding.AccountID = ""
							binding.GroupID = 0
							binding.BoundKeys = nil
						}
						binding.Base = live.Base
					}
					item = &managementChannel{subInstalledChannel: binding, Engine: row.Engine, AccountIDs: []string{}}
					if live, ok := snapshot.Providers[row.Provider]; ok {
						item.ProbeFingerprint = live.ProbeFingerprint
					}
					ids := map[string]bool{}
					for _, key := range binding.BoundKeys {
						ids[key.AccountID] = true
					}
					// A site match enables navigation/filtering only. Probe results and
					// account-only data still require a verified key/group binding.
					if len(ids) == 0 && binding.Base != "" {
						for _, a := range accounts {
							if a.Owner == owner && subBindingSite(a.Base) == subBindingSite(binding.Base) {
								ids[a.ID] = true
							}
						}
					}
					for id := range ids {
						item.AccountIDs = append(item.AccountIDs, id)
					}
					sort.Strings(item.AccountIDs)
					grouped[row.Provider] = item
				}
				found := false
				for _, model := range item.Models {
					if model == row.Model {
						found = true
						break
					}
				}
				if !found {
					item.Models = append(item.Models, row.Model)
				}
				if row.Upstream != "" && row.Upstream != row.Model {
					if item.ModelMappings == nil {
						item.ModelMappings = map[string]string{}
					}
					item.ModelMappings[row.Model] = row.Upstream
				}
			}
			for _, item := range grouped {
				sort.Strings(item.Models)
				data = append(data, *item)
			}
		}(source.ID)
	}
	wg.Wait()
	sort.Slice(data, func(i, j int) bool {
		return data[i].SourceID+"\n"+data[i].Provider < data[j].SourceID+"\n"+data[j].Provider
	})
	writeJSON(w, 200, map[string]any{"data": data, "unavailable_sources": unavailable})
}

type channelRoute struct {
	OriginProvider string `json:"origin_provider,omitempty"`
	Upstream       string `json:"upstream_model"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	KeyID          string `json:"api_key_id"`
	KeyPrefix      string `json:"key_prefix"`
	KeyPosition    int    `json:"key_position"`
	Position       int    `json:"position"`
}

// The catalog already expands wildcards/nested keys and applies temporary
// order rules. Count positions per model from that effective ordered catalog.
func (s *Service) channelRoutes(w http.ResponseWriter, r *http.Request) {
	src, err := s.control.source(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	raw, _, err := fetchSource(ctx, src, "/v1/api-keys", nil)
	var keys []struct {
		ID       string `json:"key_id"`
		Prefix   string `json:"prefix"`
		Position int    `json:"position"`
	}
	if err != nil || decodeMap(raw["data"], &keys) != nil {
		http.Error(w, "API key 列表读取失败", 503)
		return
	}
	data := []channelRoute{}
	state, _, stateErr := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if stateErr != nil {
		http.Error(w, "渠道配置读取失败", 503)
		return
	}
	unavailable := []string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				unavailable = append(unavailable, key.ID)
				mu.Unlock()
				return
			}
			defer func() { <-slots }()
			catalog, _, e := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key.ID}, "endpoint": {"all"}, "stream": {"all"}})
			var rows []struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Upstream string `json:"upstream_model"`
			}
			if e == nil {
				e = decodeMap(catalog["data"], &rows)
			}
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				unavailable = append(unavailable, key.ID)
				return
			}
			positions := map[string]int{}
			seen := map[string]bool{}
			replaced := configuredExcluded(state, key.ID)
			for _, row := range rows {
				if replaced[row.Provider] {
					continue
				}
				unique := row.Provider + "\n" + row.Model
				if seen[unique] {
					continue
				}
				seen[unique] = true
				positions[row.Model]++
				data = append(data, channelRoute{Provider: row.Provider, Model: row.Model, Upstream: row.Upstream, KeyID: key.ID, KeyPrefix: key.Prefix, KeyPosition: key.Position, Position: positions[row.Model]})
			}
		}()
	}
	wg.Wait()
	providers := map[string]bool{}
	for _, route := range data {
		providers[route.Provider] = true
	}
	// A source channel can currently belong to no caller key while its imported
	// copy does. Include the unfiltered catalog when resolving copy origins.
	if catalog, _, err := fetchSource(ctx, src, "/v1/model-channels", url.Values{"endpoint": {"all"}, "stream": {"all"}}); err == nil {
		var channels []struct {
			Provider string `json:"provider"`
		}
		if decodeMap(catalog["data"], &channels) == nil {
			for _, channel := range channels {
				providers[channel.Provider] = true
			}
		}
	}
	origins := map[string]string{}
	for provider := range providers {
		for _, key := range keys {
			origins[configuredImportName(provider, key.ID)] = provider
		}
	}
	for i := range data {
		data[i].OriginProvider = origins[data[i].Provider]
	}
	sort.Slice(data, func(i, j int) bool {
		a, b := data[i], data[j]
		if a.KeyPosition != b.KeyPosition {
			return a.KeyPosition < b.KeyPosition
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Position < b.Position
	})
	// A preview can reuse this full per-source catalog instead of fetching
	// channel-options separately for every caller key. Fence the parallel reads
	// with the same live revision; an inconsistent read is display-only.
	endState, _, endErr := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	revision, _ := state["revision"].(string)
	consistent := endErr == nil && revision != "" && endState["revision"] == revision
	writeJSON(w, 200, map[string]any{
		"data": data, "unavailable_keys": unavailable,
		"revision": revision, "snapshot_consistent": consistent,
		"manageable":      state["temporary_channel_management"] == true,
		"batch_revisions": true, "atomic_batch": true, "checked_at": time.Now().UnixMilli(),
	})
}

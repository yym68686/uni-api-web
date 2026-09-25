package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

const keyDirectoryFresh = 30 * time.Second
const keyDirectoryMaxAge = 5 * time.Minute
const keyDirectoryRetry = 5 * time.Second
const keyDirectoryTimeout = 8 * time.Second

// This directory is UI discovery only. It never authorizes mutations: saving
// still reads live key membership and the current control revision. Store only
// opaque IDs and masked labels, scoped by source URL and credential identity.
type importKey struct {
	ID       string `json:"key_id"`
	Prefix   string `json:"prefix"`
	Position int    `json:"position"`
	Current  bool   `json:"is_current,omitempty"`
}
type importKeyDirectory struct {
	Keys          []importKey `json:"keys"`
	Stale         bool        `json:"stale,omitempty"`
	RefreshFailed bool        `json:"refresh_failed,omitempty"`
}
type keyDirectoryEntry struct {
	identity string
	keys     []importKey
	checked  time.Time
	attempt  time.Time
	status   int
	err      error
	inflight chan struct{}
}
type keyDirectoryCache struct {
	mu      sync.Mutex
	entries map[string]*keyDirectoryEntry
}

func (s *Service) savedImportKeys(ctx context.Context, src controlSource) ([]importKey, time.Time) {
	if s.control == nil || s.control.db == nil {
		return nil, time.Time{}
	}
	var raw []byte
	var checked int64
	err := s.control.db.QueryRowContext(ctx, `SELECT directory,checked_at FROM console_channel_import_key_snapshots WHERE source_id=$1 AND identity=$2`, src.ID, controlTarget(src)).Scan(&raw, &checked)
	if err != nil {
		return nil, time.Time{}
	}
	var keys []importKey
	if json.Unmarshal(raw, &keys) != nil || keys == nil || time.Since(time.Unix(checked, 0)) >= keyDirectoryMaxAge {
		return nil, time.Time{}
	}
	return keys, time.Unix(checked, 0)
}

func (s *Service) saveImportKeys(ctx context.Context, src controlSource, keys []importKey) {
	if s.control == nil || s.control.db == nil {
		return
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return
	}
	_, _ = s.control.db.ExecContext(ctx, `INSERT INTO console_channel_import_key_snapshots(source_id,identity,directory,checked_at) VALUES($1,$2,$3,$4)
 ON CONFLICT(source_id) DO UPDATE SET identity=excluded.identity,directory=excluded.directory,checked_at=excluded.checked_at`, src.ID, controlTarget(src), raw, time.Now().Unix())
}

func (s *Service) deleteImportKeys(ctx context.Context, src controlSource) {
	if s.control == nil || s.control.db == nil {
		return
	}
	_, _ = s.control.db.ExecContext(ctx, `DELETE FROM console_channel_import_key_snapshots WHERE source_id=$1 AND identity=$2`, src.ID, controlTarget(src))
}

func (s *Service) channelImportKeys(ctx context.Context, src controlSource) (importKeyDirectory, int, error) {
	c := &s.keyDirectories
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*keyDirectoryEntry{}
	}
	identity := controlTarget(src)
	entry := c.entries[src.ID]
	if entry == nil || entry.identity != identity {
		c.mu.Unlock()
		keys, checked := s.savedImportKeys(ctx, src)
		c.mu.Lock()
		entry = c.entries[src.ID]
		if entry == nil || entry.identity != identity {
			// Bound memory across deleted sources and credential rotations.
			if len(c.entries) >= 128 {
				for id, e := range c.entries {
					if e.inflight == nil {
						delete(c.entries, id)
						break
					}
				}
			}
			entry = &keyDirectoryEntry{identity: identity, keys: keys, checked: checked}
			c.entries[src.ID] = entry
		}
	}
	now := time.Now()
	hasKeys := entry.keys != nil && now.Sub(entry.checked) < keyDirectoryMaxAge
	fresh := hasKeys && now.Sub(entry.checked) < keyDirectoryFresh
	if !fresh && entry.inflight == nil && (entry.err == nil || now.Sub(entry.attempt) >= keyDirectoryRetry) {
		entry.inflight = make(chan struct{})
		entry.attempt = now
		// A departing browser must not cancel the shared read for other callers.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), keyDirectoryTimeout)
		go func() {
			defer cancel()
			keys, status, err := fetchImportKeys(readCtx, src)
			if err == nil {
				s.saveImportKeys(readCtx, src, keys)
			} else if status == 401 || status == 403 {
				s.deleteImportKeys(readCtx, src)
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			entry.status, entry.err = status, err
			if err == nil {
				entry.keys, entry.checked = keys, time.Now()
			} else if status == 401 || status == 403 {
				entry.keys = nil
				entry.checked = time.Time{}
			}
			close(entry.inflight)
			entry.inflight = nil
		}()
	}
	if hasKeys {
		data := importKeyDirectory{Keys: append([]importKey{}, entry.keys...), Stale: !fresh, RefreshFailed: entry.err != nil && entry.inflight == nil}
		c.mu.Unlock()
		return data, 200, nil
	}
	pending := entry.inflight
	if pending == nil {
		status, err := entry.status, entry.err
		c.mu.Unlock()
		return importKeyDirectory{}, status, err
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return importKeyDirectory{}, http.StatusGatewayTimeout, errors.New("API key 列表读取超时，请重试")
	case <-pending:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry.err != nil {
		return importKeyDirectory{}, entry.status, entry.err
	}
	return importKeyDirectory{Keys: append([]importKey{}, entry.keys...)}, 200, nil
}

func fetchImportKeys(ctx context.Context, src controlSource) ([]importKey, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", src.Base+"/v1/api-keys", nil)
	if err != nil {
		return nil, 502, errors.New("来源地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+src.Key)
	req.Header.Set("Accept", "application/json")
	response, err := sourceHTTP.Do(req)
	if err != nil {
		return nil, 502, errors.New("来源暂未响应，API key 列表读取失败，请重试")
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return nil, 403, errors.New("来源密钥无管理权限，请检查来源设置")
	}
	if response.StatusCode != 200 {
		return nil, 502, errors.New("来源暂时无法读取 API key，请稍后重试")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	var result struct {
		CanInspect bool        `json:"can_inspect_all"`
		Keys       []importKey `json:"data"`
	}
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &result) != nil || result.Keys == nil {
		return nil, 502, errors.New("来源返回了无效的 API key 列表")
	}
	if !result.CanInspect {
		return nil, 403, errors.New("来源密钥无管理权限，请检查来源设置")
	}
	seen := map[string]bool{}
	for _, k := range result.Keys {
		if k.ID == "" || k.Position < 1 || seen[k.ID] {
			return nil, 502, errors.New("来源返回了无效的 API key 列表")
		}
		seen[k.ID] = true
	}
	return result.Keys, 200, nil
}

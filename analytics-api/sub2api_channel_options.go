package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type subOptionsRead struct {
	data   map[string]any
	status int
	err    error
}

// At most two concurrent GETs per options request. The buffered result lets
// an earlier-priority failure return without waiting for unrelated slow reads;
// the caller cancels their HTTP contexts on every return path.
func startSubOptionsRead(ctx context.Context, stage string, read func() (map[string]any, int, error)) <-chan subOptionsRead {
	result := make(chan subOptionsRead, 1)
	go func() {
		end := managementReadSpan(ctx, stage)
		data, status, err := read()
		end()
		result <- subOptionsRead{data, status, err}
	}()
	return result
}

func subChannelOptionsData(ctx context.Context, src controlSource, query url.Values) (map[string]any, int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer managementReadSpan(ctx, "options_gateway")()
	controls := startSubOptionsRead(ctx, "options_controls", func() (map[string]any, int, error) {
		return subGateway(ctx, src, http.MethodGet, "/v1/channel-controls", nil)
	})
	keyList := startSubOptionsRead(ctx, "options_keys", func() (map[string]any, int, error) {
		return fetchSource(ctx, src, "/v1/api-keys", nil)
	})
	keyInput := query.Get("api_key_id")
	key, keyErr := subRawKey(src.ID, keyInput)
	// API-key labels do not depend on the gateway config revision and may be read
	// alongside it. Position catalogs do: keep them behind the revision read so
	// an older catalog can never be paired with a newer accepted revision.
	state := <-controls
	if state.err != nil {
		return nil, state.status, state.err
	}
	keys := <-keyList
	if keys.err != nil {
		return nil, keys.status, errors.New("无法读取来源 API key")
	}
	channels := []any{}
	if keyInput != "" {
		if keyErr != nil {
			return nil, http.StatusBadRequest, keyErr
		}
		catalog := <-startSubOptionsRead(ctx, "options_catalog", func() (map[string]any, int, error) {
			return fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
		})
		if catalog.err != nil {
			return nil, catalog.status, errors.New("无法读取当前渠道顺序")
		}
		if data, ok := catalog.data["data"].([]any); ok {
			replaced := configuredExcluded(state.data, key)
			for _, row := range data {
				item, _ := row.(map[string]any)
				provider, _ := item["provider"].(string)
				if !replaced[provider] {
					channels = append(channels, row)
				}
			}
		}
	}
	return map[string]any{"batch_revisions": true, "revision": state.data["revision"], "supported": state.data["temporary_channel_import"] == true, "manageable": state.data["temporary_channel_management"] == true, "keys": keys.data["data"], "channels": channels, "provider": subProviderName(query.Get("account_id"), int64Param(query.Get("group_id")), strings.TrimPrefix(keyInput, src.ID+"::"))}, http.StatusOK, nil
}

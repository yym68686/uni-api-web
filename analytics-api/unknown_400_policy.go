package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

const unknown400Scope = "__uni_console_global_preferences__"
const unknown400Path = "/preferences/unknown_upstream_400_retries"
const legacyUnknown400Max = 3
const unknown400Max = 99 // Existing gateway safety cap is 100 total attempts.

// Only source-global intent is inspected, never provider bodies or nested echoes.
func unknown400Mutation(in channelSettingMutation) (int, error) {
	requested := 0
	for _, change := range in.Changes {
		value, exists := change.Set[unknown400Path]
		if !exists {
			continue
		}
		if change.Provider != unknown400Scope {
			return 0, errors.New("所有上游400重试仅支持来源全局设置")
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return 0, fmt.Errorf("所有上游400换渠道次数必须是 0–%d 的整数", unknown400Max)
		}
		n, err := strconv.Atoi(string(raw))
		if err != nil || n < 0 || n > unknown400Max {
			return 0, fmt.Errorf("所有上游400换渠道次数必须是 0–%d 的整数", unknown400Max)
		}
		requested = max(requested, n)
	}
	return requested, nil
}

func (s *Service) checkUnknown400Gateway(ctx context.Context, src controlSource, in channelSettingMutation) (int, error) {
	requested, err := unknown400Mutation(in)
	if err != nil {
		return http.StatusBadRequest, err
	}
	if requested == 0 {
		return 200, nil
	}
	schema, status, err := s.settingsGateway(ctx, src, "GET", "/v1/channel-settings/schema", nil)
	if err != nil {
		return status, err
	}
	if schema["all_upstream_400_retries"] != true {
		return http.StatusConflict, errors.New("来源尚不支持所有上游400换渠道重试，请先更新uni-api；修改未发送")
	}
	limit := legacyUnknown400Max
	if n, ok := schema["all_upstream_400_retries_max"].(float64); ok && n >= 0 && n <= unknown400Max && n == float64(int(n)) {
		limit = int(n)
	}
	if requested > limit {
		return http.StatusConflict, fmt.Errorf("当前来源最多支持 %d 次上游400换渠道；修改未发送", limit)
	}
	return 200, nil
}

// Zero/missing intent is backwards compatible. Nonzero or malformed saved
// intent must not silently disappear during a code rollback. Keep the LKG.
func savedUnknown400Retry(saved retainedSnapshot) (limit int, requiresCapability bool) {
	var overlay struct {
		Set map[string]json.RawMessage `json:"set"`
	}
	raw, exists := saved.Settings[unknown400Scope]
	if !exists {
		return 0, false
	}
	if json.Unmarshal(raw, &overlay) != nil {
		return 0, true
	}
	value, exists := overlay.Set[unknown400Path]
	if !exists {
		return 0, false
	}
	var n *int
	if json.Unmarshal(value, &n) != nil || n == nil {
		return 0, true
	}
	return *n, *n != 0
}

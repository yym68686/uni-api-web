package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

const unknown400Scope = "__uni_console_global_preferences__"
const unknown400Path = "/preferences/unknown_upstream_400_retries"

// Only source-global intent is inspected, never provider bodies or nested echoes.
func unknown400Mutation(in channelSettingMutation) (bool, error) {
	required := false
	for _, change := range in.Changes {
		value, exists := change.Set[unknown400Path]
		if !exists {
			continue
		}
		if change.Provider != unknown400Scope {
			return false, errors.New("未知400重试仅支持来源全局设置")
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return false, errors.New("未知400换渠道次数必须是0至3的整数")
		}
		n, err := strconv.Atoi(string(raw))
		if err != nil || n < 0 || n > 3 {
			return false, errors.New("未知400换渠道次数必须是0至3的整数")
		}
		required = required || n > 0
	}
	return required, nil
}

func (s *Service) checkUnknown400Gateway(ctx context.Context, src controlSource, in channelSettingMutation) (int, error) {
	required, err := unknown400Mutation(in)
	if err != nil {
		return http.StatusBadRequest, err
	}
	if !required {
		return 200, nil
	}
	schema, status, err := s.settingsGateway(ctx, src, "GET", "/v1/channel-settings/schema", nil)
	if err != nil {
		return status, err
	}
	if schema["unknown_upstream_400_retries"] != true {
		return http.StatusConflict, errors.New("来源尚不支持未知400换渠道重试，请先更新uni-api；修改未发送")
	}
	return 200, nil
}

// Zero/missing intent is backwards compatible. Nonzero or malformed saved
// intent must not silently disappear during a code rollback. Keep the LKG.
func requiresUnknown400Retry(saved retainedSnapshot) bool {
	var overlay struct {
		Set map[string]json.RawMessage `json:"set"`
	}
	raw, exists := saved.Settings[unknown400Scope]
	if !exists {
		return false
	}
	if json.Unmarshal(raw, &overlay) != nil {
		return true
	}
	value, exists := overlay.Set[unknown400Path]
	if !exists {
		return false
	}
	var n *int
	return json.Unmarshal(value, &n) != nil || n == nil || *n != 0
}

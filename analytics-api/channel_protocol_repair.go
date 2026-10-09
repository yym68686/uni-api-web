package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type protocolRepairInput struct {
	Action    string   `json:"action"`
	Revision  string   `json:"revision"`
	PlanHash  string   `json:"plan_hash"`
	Operation string   `json:"operation_id"`
	Providers []string `json:"providers"`
}

type protocolRepairChange struct {
	Provider     string           `json:"provider"`
	Key          string           `json:"api_key_id"`
	BeforeEngine string           `json:"before_engine"`
	BeforeURL    string           `json:"before_url"`
	After        []map[string]any `json:"after"`
}

func protocolRepairCustomized(snapshot retainedSnapshot, provider string) bool {
	var settings struct {
		Set    map[string]any `json:"set"`
		Remove []string       `json:"remove"`
	}
	_ = json.Unmarshal(snapshot.Settings[provider], &settings)
	for path := range settings.Set {
		if path == "/engine" || path == "/base_url" {
			return true
		}
	}
	for _, path := range settings.Remove {
		if path == "/engine" || path == "/base_url" {
			return true
		}
	}
	return false
}

func (s *Service) protocolRepairPlan(ctx context.Context, src controlSource, snapshot retainedSnapshot, owner string, selected []string) (retainedSnapshot, []protocolRepairChange, []map[string]string, error) {
	refs, err := s.control.subChannelRefs(ctx, owner)
	if err != nil {
		return snapshot, nil, nil, err
	}
	wanted := map[string]bool{}
	for _, provider := range selected {
		wanted[provider] = true
	}
	changes := []protocolRepairChange{}
	skipped := []map[string]string{}
	originals := append([]retainedChannel{}, snapshot.Channels...)
	for _, old := range originals {
		if len(wanted) > 0 && !wanted[old.Provider] {
			continue
		}
		var ref *subChannelRef
		for index := range refs {
			if matchesSubProvider(old.Provider, refs[index].Account, refs[index].Group, old.KeyID) {
				ref = &refs[index]
				break
			}
		}
		if ref == nil {
			continue
		}
		skip := func(reason string) {
			skipped = append(skipped, map[string]string{"provider": old.Provider, "reason": reason})
		}
		if protocolRepairCustomized(snapshot, old.Provider) {
			skip("引擎或地址已有人工覆盖，保留原设置")
			continue
		}
		document, _, err := s.importProviderDocument(ctx, src, snapshot, old.Provider, "")
		if err != nil {
			return snapshot, nil, nil, err
		}
		engine, _ := document["engine"].(string)
		base, _ := document["base_url"].(string)
		if engine != "gpt" || !strings.HasSuffix(strings.TrimRight(base, "/"), "/v1/responses") {
			continue
		}
		if subBindingSite(base) != subBindingSite(ref.Base) {
			skip("上游地址与原站点不一致，保留原设置")
			continue
		}
		models, err := protocolDocumentModels(document)
		if err != nil {
			skip(err.Error())
			continue
		}
		upstreams := []string{}
		for _, model := range models {
			upstreams = append(upstreams, model)
		}
		protocols, err := s.importProtocols(ctx, ref.Account, ref.Group, upstreams, nil)
		if err != nil {
			skip(err.Error())
			continue
		}
		parts, err := partitionChannelModels(models, protocols)
		if err != nil {
			skip(err.Error())
			continue
		}
		channels, err := splitProtocolChannel(&snapshot, old, document, parts)
		if err != nil {
			return snapshot, nil, nil, err
		}
		change := protocolRepairChange{Provider: old.Provider, Key: old.KeyID, BeforeEngine: engine, BeforeURL: base, After: []map[string]any{}}
		for index, channel := range channels {
			change.After = append(change.After, map[string]any{"provider": channel.Provider, "engine": "自动（由 uni-api 按地址判断）", "base_url": channel.Base, "models": parts[index].Models})
		}
		changes = append(changes, change)
	}
	return snapshot, changes, skipped, nil
}

func protocolDocumentModels(document map[string]any) (map[string]string, error) {
	var items []any
	if decodeMap(document["model"], &items) != nil {
		return nil, errors.New("模型定义不可识别，保留原设置")
	}
	models := map[string]string{}
	for _, item := range items {
		switch value := item.(type) {
		case string:
			models[value] = value
		case map[string]any:
			for upstream, raw := range value {
				name, ok := raw.(string)
				if !ok {
					return nil, errors.New("模型映射不可识别")
				}
				models[name] = upstream
			}
		default:
			return nil, errors.New("模型定义不可识别")
		}
	}
	return models, nil
}

func (s *Service) repairChannelProtocols(w http.ResponseWriter, r *http.Request) {
	var in protocolRepairInput
	if !decodeConfiguration(w, r, &in) {
		return
	}
	if in.Action != "preview" && in.Action != "apply" && in.Action != "rollback" && in.Action != "status" {
		http.Error(w, "无效操作", 400)
		return
	}
	src, err := s.control.source(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "来源不存在", 404)
		return
	}
	if src.configKeyError != nil {
		http.Error(w, "配置密钥不可用", 503)
		return
	}
	owner, _ := s.controlUser(r)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(100 * time.Second))
	unlocked, err := s.control.lockControls(ctx, src.ID)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	defer unlocked()
	state, code, err := subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	revision, _ := state["revision"].(string)
	if (in.Action == "preview" || in.Action == "apply") && state["automatic_engine"] != true {
		http.Error(w, "来源尚不支持按地址自动识别引擎，请更新 uni-api", 400)
		return
	}
	if in.Revision != "" && in.Revision != revision {
		http.Error(w, "配置已变化，请重新预览", 409)
		return
	}
	if in.Action == "status" {
		var encrypted, operationStatus string
		var raw []byte
		err = s.control.db.QueryRowContext(ctx, `SELECT encrypted_intent,status,public_result FROM console_channel_protocol_operations WHERE id=$1 AND source_id=$2 AND owner=$3 AND target_hash=$4`, in.Operation, src.ID, owner, controlTarget(src)).Scan(&encrypted, &operationStatus, &raw)
		if err != nil {
			http.Error(w, "操作记录不存在", 404)
			return
		}
		var result map[string]any
		_ = json.Unmarshal(raw, &result)
		if operationStatus == "applied" {
			writeJSON(w, 200, result)
			return
		}
		plain, decryptErr := s.control.decrypt(encrypted)
		var intent retainedSnapshot
		if decryptErr != nil || json.Unmarshal([]byte(plain), &intent) != nil {
			http.Error(w, "操作备份不可用", 503)
			return
		}
		live, liveErr := decodeRetained(state)
		if liveErr != nil || !controlEquivalent(live, intent) {
			writeJSON(w, 200, map[string]any{"status": "pending", "message": "当前配置与目标不一致，请人工核对", "operation_id": in.Operation})
			return
		}
		result["status"], result["revision"], result["operation_id"] = "applied", revision, in.Operation
		if err = s.saveLiveControls(ctx, src, state); err != nil {
			http.Error(w, "运行时配置已生效，保留状态同步失败", 503)
			return
		}
		_, err = s.control.db.ExecContext(ctx, `UPDATE console_channel_protocol_operations SET status='applied',public_result=$2,updated_at=now() WHERE id=$1`, in.Operation, mustJSON(result))
		if err != nil {
			http.Error(w, "运行时已应用，记录待确认", 503)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	before, code, err := s.importSnapshot(ctx, src, state, revision)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	var next retainedSnapshot
	changes, skipped := []protocolRepairChange{}, []map[string]string{}
	if in.Action == "rollback" {
		var encrypted, status string
		var result []byte
		err = s.control.db.QueryRowContext(ctx, `SELECT encrypted_before,status,public_result FROM console_channel_protocol_operations WHERE id=$1 AND source_id=$2 AND owner=$3 AND target_hash=$4`, in.Operation, src.ID, owner, controlTarget(src)).Scan(&encrypted, &status, &result)
		var receipt struct {
			Revision string `json:"revision"`
		}
		_ = json.Unmarshal(result, &receipt)
		if err != nil || status != "applied" || receipt.Revision != revision {
			http.Error(w, "修复后配置已变化或记录未完成，禁止覆盖，请人工核对", 409)
			return
		}
		plain, err := s.control.decrypt(encrypted)
		if err != nil || json.Unmarshal([]byte(plain), &next) != nil {
			http.Error(w, "备份不可用", 503)
			return
		}
	} else {
		if err = json.Unmarshal([]byte(mustJSON(before)), &next); err != nil {
			http.Error(w, "快照不可用", 503)
			return
		}
		next, changes, skipped, err = s.protocolRepairPlan(ctx, src, next, owner, in.Providers)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
	}
	if in.Action == "preview" {
		writeJSON(w, 200, map[string]any{"revision": revision, "plan_hash": tokenHash(mustJSON(next)), "changes": changes, "skipped": skipped})
		return
	}
	if in.Revision == "" || in.Operation == "" || len(in.Operation) > 128 {
		http.Error(w, "需提供预览版本与操作编号", 400)
		return
	}
	if in.Action == "apply" && len(changes) == 0 {
		writeJSON(w, 200, map[string]any{"status": "unchanged", "revision": revision, "skipped": skipped})
		return
	}
	if in.Action == "apply" && (in.PlanHash == "" || in.PlanHash != tokenHash(mustJSON(next))) {
		http.Error(w, "协议证据或配置已变化，请重新预览", 409)
		return
	}
	record, err := s.control.retainedRecord(ctx, src.ID)
	if err != nil || !record.Enabled {
		http.Error(w, "请先启用保留临时配置", 409)
		return
	}
	op := in.Operation
	if in.Action == "rollback" {
		op += "-rollback"
	}
	encBefore, err := s.control.encrypt(mustJSON(before))
	if err != nil {
		http.Error(w, "备份失败，未应用", 503)
		return
	}
	encNext, err := s.control.encrypt(mustJSON(next))
	if err != nil {
		http.Error(w, "备份失败，未应用", 503)
		return
	}
	_, err = s.control.db.ExecContext(ctx, `INSERT INTO console_channel_protocol_operations(id,source_id,owner,request_hash,request_revision,status,encrypted_before,encrypted_intent,public_result,target_hash) VALUES($1,$2,$3,$4,$5,'pending',$6,$7,$8,$9)`, op, src.ID, owner, "protocol-repair:"+tokenHash(mustJSON(in)), revision, encBefore, encNext, mustJSON(map[string]any{"changes": changes, "skipped": skipped}), controlTarget(src))
	if err != nil {
		http.Error(w, "操作已存在或备份失败；未重复提交，请核对记录", 409)
		return
	}
	admin := src
	if src.ConfigKey != "" {
		admin.Key = src.ConfigKey
	}
	applied, code, err := subGateway(ctx, admin, "POST", "/v1/channel-controls/restore", map[string]any{"revision": revision, "snapshot": next})
	if err != nil {
		http.Error(w, "应用结果待确认，请重新读取渠道状态；勿盲目重试", code)
		return
	}
	if err = s.saveLiveControls(ctx, src, applied); err != nil {
		retentionFailure(w, err)
		return
	}
	_, _ = s.control.db.ExecContext(ctx, `DELETE FROM console_channel_management_snapshots WHERE source_id=$1`, src.ID)
	result := map[string]any{"status": "applied", "operation_id": op, "revision": applied["revision"], "changes": changes, "skipped": skipped}
	_, err = s.control.db.ExecContext(ctx, `UPDATE console_channel_protocol_operations SET status='applied',public_result=$2,updated_at=now() WHERE id=$1`, op, mustJSON(result))
	if err != nil {
		http.Error(w, "运行时已应用，记录待确认", 503)
		return
	}
	writeJSON(w, 200, result)
}

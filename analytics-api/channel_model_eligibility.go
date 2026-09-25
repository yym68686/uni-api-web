package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Retaining an exact saved public/upstream pair is not a new selection. Never
// delete serving routes simply because a retest failed; aliases that change
// their upstream or name still require successful evidence.
func newChannelModels(current, desired map[string]string) map[string]bool {
	needed := map[string]bool{}
	for name, up := range desired {
		if current[name] != up {
			needed[up] = true
		}
	}
	return needed
}
func publicChannelModels(models []string, mappings map[string]string) map[string]string {
	result := map[string]string{}
	for _, m := range models {
		result[m] = m
	}
	for name, up := range mappings {
		result[name] = up
	}
	return result
}
func (s *Service) validateSiteModelChanges(ctx context.Context, account string, group int64, current, desired map[string]string) error {
	unavailable := []string{}
	for model := range newChannelModels(current, desired) {
		var available bool
		err := s.control.db.QueryRowContext(ctx, `SELECT state='done' AND result->'availability'->>'status'='success' FROM console_sub_models WHERE account_id=$1 AND group_id=$2 AND model=$3`, account, group, model).Scan(&available)
		if err != nil || !available {
			unavailable = append(unavailable, model)
		}
	}
	if len(unavailable) > 0 {
		sort.Strings(unavailable)
		return fmt.Errorf("新增或重命名模型必须检测可用，请先完成检测；未通过：%s", strings.Join(unavailable, "、"))
	}
	return nil
}

type modelEvidence struct {
	State  string
	Result *subResult
}

func (e modelEvidence) available() bool {
	return e.State == "done" && e.Result != nil && e.Result.Availability.Status == "success"
}
func newestModelEvidence(a, b modelEvidence) modelEvidence {
	pending := func(v modelEvidence) bool { return v.State == "queued" || v.State == "running" }
	if pending(a) != pending(b) {
		if pending(b) {
			return b
		}
		return a
	}
	stamp := func(v modelEvidence) int64 {
		if v.Result == nil {
			return 0
		}
		return v.Result.CheckedAt
	}
	if stamp(b) > stamp(a) || (stamp(b) == stamp(a) && !b.available()) {
		return b
	}
	return a
}
func (s *Service) validateConfiguredModelChanges(ctx context.Context, src controlSource, provider, owner string, current, desired map[string]string) error {
	needed := newChannelModels(current, desired)
	if len(needed) == 0 {
		return nil
	}
	providers, err := configuredProviders(ctx, src)
	if err != nil {
		return errors.New("无法核对渠道的最新检测配置")
	}
	var selected configuredProvider
	found := false
	for _, p := range providers {
		if p.Provider == provider {
			selected = p
			found = true
			break
		}
	}
	if !found {
		return errors.New("渠道已不存在，请重新读取")
	}
	fingerprint := configuredProbeFingerprint(src, selected)
	// Resolve tested public names to actual upstream models before considering
	// a result. Never borrow another source/provider/credential's passing badge.
	catalog, _, err := fetchSource(ctx, src, "/v1/model-channels", url.Values{"endpoint": {"all"}, "stream": {"all"}})
	var catalogRows []batchCatalogRow
	if err != nil || decodeMap(catalog["data"], &catalogRows) != nil {
		return errors.New("渠道模型读取失败")
	}
	mapped := map[string]string{}
	for _, r := range catalogRows {
		if r.Provider == provider {
			up := r.Upstream
			if up == "" {
				up = r.Model
			}
			mapped[r.Model] = up
		}
	}
	evidence := map[string]modelEvidence{}
	rows, err := s.control.db.QueryContext(ctx, `SELECT model,state,result FROM console_configured_checks WHERE source_id=$1 AND provider=$2 AND source_target=$3 AND fingerprint=$4 AND kind IN ('model','availability')`, src.ID, provider, controlTarget(src), fingerprint)
	if err != nil {
		return err
	}
	for rows.Next() {
		var model, state string
		var raw []byte
		if err = rows.Scan(&model, &state, &raw); err != nil {
			break
		}
		var result *subResult
		_ = json.Unmarshal(raw, &result)
		up := mapped[model]
		if up == "" {
			up = model
		}
		v := modelEvidence{state, result}
		if old, ok := evidence[up]; ok {
			evidence[up] = newestModelEvidence(old, v)
		} else {
			evidence[up] = v
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	// Site evidence can be reused only for confirmed key/group bindings for ALL
	// keys of this provider; a hostname match alone is never availability proof.
	hashes := configuredKeyHashes(selected)
	type group struct {
		account string
		id      int64
	}
	groups := []group{}
	bound := len(hashes) > 0
	for _, hash := range hashes {
		rows, err = s.control.db.QueryContext(ctx, `SELECT a.id,i.group_id,a.base FROM console_sub_key_index i JOIN console_sub_accounts a ON a.id=i.account_id WHERE a.owner=$1 AND i.key_hash=$2`, owner, hash)
		if err != nil {
			return err
		}
		matches := []group{}
		for rows.Next() {
			var g group
			var base string
			if err = rows.Scan(&g.account, &g.id, &base); err != nil {
				break
			}
			if g.id > 0 && subBindingSite(base) == subBindingSite(selected.Base) {
				matches = append(matches, g)
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if len(matches) != 1 {
			bound = false
			break
		}
		groups = append(groups, matches[0])
	}
	if bound {
		for model := range needed {
			var all, failed modelEvidence
			allPass := true
			for i, g := range groups {
				var state string
				var raw []byte
				e := s.control.db.QueryRowContext(ctx, `SELECT state,result FROM console_sub_models WHERE account_id=$1 AND group_id=$2 AND model=$3`, g.account, g.id, model).Scan(&state, &raw)
				var result *subResult
				_ = json.Unmarshal(raw, &result)
				v := modelEvidence{state, result}
				if e != nil || !v.available() {
					if allPass {
						failed = v
					} else {
						failed = newestModelEvidence(failed, v)
					}
					allPass = false
				}
				if i == 0 {
					all = v
				} else {
					all = newestModelEvidence(all, v)
				}
			}
			if !allPass {
				all = failed
			}
			if old, ok := evidence[model]; ok {
				evidence[model] = newestModelEvidence(old, all)
			} else {
				evidence[model] = all
			}
		}
	}
	unavailable := []string{}
	for model := range needed {
		if !evidence[model].available() {
			unavailable = append(unavailable, model)
		}
	}
	if len(unavailable) > 0 {
		sort.Strings(unavailable)
		return fmt.Errorf("新增或重命名模型必须在当前渠道检测可用，请先完成检测；未通过：%s", strings.Join(unavailable, "、"))
	}
	return nil
}

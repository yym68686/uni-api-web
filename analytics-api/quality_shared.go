package main

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
)

type qualityBinding struct {
	Source   string `json:"source"`
	Provider string `json:"provider"`
	Account  string `json:"account"`
	Group    int64  `json:"group"`
}

func qualityGroupID(account string, group int64) string {
	return account + ":" + strconv.FormatInt(group, 10)
}
func qualityChannelID(source, provider string) string { return source + "\n" + provider }

// Use persisted, exact bindings only. No upstream scan or model request is made.
// Multi-group/ambiguous configured providers cannot identify the key a probe
// used, so their checks remain local instead of being copied into every group.
func (s *Service) qualityBindings(ctx context.Context, owner string) ([]qualityBinding, error) {
	configured, err := s.configuredBindings(ctx, owner)
	if err != nil {
		return nil, err
	}
	bindings := map[string]qualityBinding{}
	for _, c := range configured {
		if c.AccountID != "" && c.GroupID > 0 && c.BindingStatus == "matched" {
			bindings[qualityChannelID(c.SourceID, c.Provider)] = qualityBinding{c.SourceID, c.Provider, c.AccountID, c.GroupID}
		}
	}
	refs, err := s.control.subChannelRefs(ctx, owner)
	if err != nil {
		return nil, err
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT p.source_id,p.encrypted_snapshot FROM console_control_snapshots p JOIN console_sources s ON s.id=p.source_id WHERE s.enabled AND p.encrypted_snapshot<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var source, encrypted string
		if err = rows.Scan(&source, &encrypted); err != nil {
			return nil, err
		}
		snapshot, e := s.control.retainedSnapshot(retainedRecord{Encrypted: encrypted})
		if e != nil {
			return nil, e
		}
		for _, c := range snapshot.Channels {
			var setting struct {
				Set    map[string]any `json:"set"`
				Remove []string       `json:"remove"`
			}
			_ = json.Unmarshal(snapshot.Settings[c.Provider], &setting)
			changed := false
			for path := range setting.Set {
				if path == "/api" || path == "/base_url" {
					changed = true
				}
			}
			for _, path := range setting.Remove {
				if path == "/api" || path == "/base_url" {
					changed = true
				}
			}
			if changed {
				continue
			}
			for _, ref := range refs {
				if c.Provider == subProviderName(ref.Account, ref.Group, c.KeyID) && subBindingSite(c.Base) == subBindingSite(ref.Base) {
					bindings[qualityChannelID(source, c.Provider)] = qualityBinding{source, c.Provider, ref.Account, ref.Group}
				}
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]qualityBinding, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		return qualityChannelID(out[i].Source, out[i].Provider) < qualityChannelID(out[j].Source, out[j].Provider)
	})
	return out, nil
}

type sharedQualityProjection struct {
	History qualitySummary
	Check   *ChannelCheck
}
type sharedQuality struct {
	Bindings []qualityBinding
	Groups   map[string]sharedQualityProjection
}

// Associate each original history row at most once. Original results/IDs are
// retained; concurrent reads, multiple installed caller keys and restarts do
// not duplicate attempts or move previously linked history to a new group.
func (s *Service) sharedQuality(ctx context.Context, owner string) (sharedQuality, error) {
	out := sharedQuality{Groups: map[string]sharedQualityProjection{}}
	var err error
	out.Bindings, err = s.qualityBindings(ctx, owner)
	if err != nil {
		return out, err
	}
	if len(out.Bindings) > 0 {
		raw, _ := json.Marshal(out.Bindings)
		_, err = s.control.db.ExecContext(ctx, `INSERT INTO console_quality_group_links(check_id,account_id,group_id)
 SELECT h.check_id,b.account,b."group" FROM jsonb_to_recordset($1::jsonb) AS b(source text,provider text,account text,"group" bigint)
 JOIN console_quality_history h ON h.source_id=b.source AND h.provider=b.provider
 JOIN console_sub_targets t ON t.account_id=b.account AND t.group_id=b."group"
 JOIN console_sub_accounts a ON a.id=t.account_id AND a.owner=$2
 WHERE h.account_id IS NULL AND NOT EXISTS(SELECT 1 FROM console_quality_group_links l WHERE l.check_id=h.check_id)
 ORDER BY h.check_id ON CONFLICT(check_id) DO NOTHING`, string(raw), owner)
		if err != nil {
			return out, err
		}
	}
	rows, err := s.control.db.QueryContext(ctx, `WITH owned AS MATERIALIZED (
 SELECT h.id,h.account_id,h.group_id,h.successful,h.verdict,h.checked_at
 FROM console_quality_group_history h JOIN console_sub_accounts a ON a.id=h.account_id WHERE a.owner=$1
 ), totals AS (SELECT account_id,group_id,count(*) AS total,count(*) FILTER(WHERE successful) AS successful,count(*) FILTER(WHERE successful AND verdict='pass') AS passed FROM owned GROUP BY account_id,group_id),
 latest AS (SELECT DISTINCT ON(account_id,group_id) account_id,group_id,id FROM owned WHERE verdict<>'running' ORDER BY account_id,group_id,checked_at DESC,id DESC)
 SELECT t.account_id,t.group_id,t.total,t.successful,t.passed,h.result,COALESCE(h.source_id,''),COALESCE(h.verdict,''),COALESCE(h.checked_at,0)
 FROM totals t LEFT JOIN latest l USING(account_id,group_id) LEFT JOIN console_quality_history h ON h.id=l.id`, owner)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var account, source, verdict string
		var group, at int64
		var raw []byte
		var p sharedQualityProjection
		if err = rows.Scan(&account, &group, &p.History.Total, &p.History.Successful, &p.History.Passed, &raw, &source, &verdict, &at); err != nil {
			return out, err
		}
		if len(raw) > 0 {
			c := ChannelCheck{Model: checkModel, CheckedAt: at, Verdict: verdict}
			if source != "" {
				if err = json.Unmarshal(raw, &c); err != nil {
					return out, err
				}
			} else {
				var result subResult
				if err = json.Unmarshal(raw, &result); err != nil {
					return out, err
				}
				c.Text, c.Message, c.DurationMS = result.Quality.Text, result.Quality.Message, result.Quality.Duration
				c.QualityProbe = &result.Quality
			}
			if c.Verdict != "pass" && c.Verdict != "fail" && c.Verdict != "inconclusive" {
				c.Verdict = "error"
			}
			c.History = &p.History
			c.HistoryScope = "account_group"
			c.Origin = "共享降智记录"
			p.Check = &c
		}
		out.Groups[qualityGroupID(account, group)] = p
	}
	return out, rows.Err()
}

func (q sharedQuality) channel(source, provider string) *ChannelCheck {
	for _, b := range q.Bindings {
		if b.Source == source && b.Provider == provider {
			p := q.Groups[qualityGroupID(b.Account, b.Group)]
			if p.Check == nil {
				return nil
			}
			c := *p.Check
			c.SourceID, c.Provider = source, provider
			return &c
		}
	}
	return nil
}

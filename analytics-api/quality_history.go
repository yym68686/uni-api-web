package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// History is independent of the latest-result projections. A durable pending
// row is created before each probe, so a stopped worker does not erase a check.
const qualityHistorySchema = `
CREATE TABLE IF NOT EXISTS console_quality_history(
 id BIGSERIAL PRIMARY KEY, check_id TEXT NOT NULL UNIQUE,
 source_id TEXT REFERENCES console_sources(id) ON DELETE CASCADE, provider TEXT,
 account_id TEXT, group_id BIGINT,
 model TEXT NOT NULL DEFAULT 'gpt-6-astra', rule TEXT NOT NULL DEFAULT 'candy-21',
 checked_at BIGINT NOT NULL, deadline TIMESTAMPTZ NOT NULL DEFAULT now(),
 verdict TEXT NOT NULL DEFAULT 'running', successful BOOLEAN NOT NULL DEFAULT false,
 result JSONB NOT NULL DEFAULT '{}',
 FOREIGN KEY(account_id,group_id) REFERENCES console_sub_targets(account_id,group_id) ON DELETE CASCADE,
 CHECK((source_id IS NOT NULL AND provider IS NOT NULL AND account_id IS NULL AND group_id IS NULL)
    OR (source_id IS NULL AND provider IS NULL AND account_id IS NOT NULL AND group_id IS NOT NULL)));
CREATE INDEX IF NOT EXISTS console_quality_channel_history ON console_quality_history(source_id,provider,id DESC);
CREATE INDEX IF NOT EXISTS console_quality_sub_history ON console_quality_history(account_id,group_id,id DESC);
CREATE TABLE IF NOT EXISTS console_quality_migrations(name TEXT PRIMARY KEY);
WITH first_run AS (
 INSERT INTO console_quality_migrations VALUES('latest-to-history-v1') ON CONFLICT DO NOTHING RETURNING name
)
INSERT INTO console_quality_history(check_id,source_id,provider,account_id,group_id,checked_at,verdict,successful,result,rule)
 SELECT 'legacy-channel:'||md5(source_id||':'||provider),source_id,provider,NULL,NULL,
 COALESCE((result->>'checked_at')::BIGINT,0),COALESCE(result->>'verdict','error'),
 COALESCE(result->>'verdict' IN ('pass','fail','inconclusive'),false),result,'legacy'
 FROM console_channel_checks WHERE EXISTS(SELECT 1 FROM first_run)
 UNION ALL
 SELECT 'legacy-sub:'||md5(account_id||':'||group_id),NULL,NULL,account_id,group_id,
 COALESCE((result->>'checked_at')::BIGINT,0),COALESCE(result->>'verdict','error'),
 COALESCE(result->'quality'->>'status'='success',false),result,'legacy'
 FROM console_sub_models WHERE model='gpt-6-astra' AND result IS NOT NULL AND EXISTS(SELECT 1 FROM first_run)
 ON CONFLICT DO NOTHING;
CREATE OR REPLACE VIEW console_quality_totals AS
 SELECT source_id,provider,account_id,group_id,count(*) AS total,
 count(*) FILTER(WHERE successful) AS successful,
 count(*) FILTER(WHERE successful AND verdict='pass') AS passed
 FROM console_quality_history GROUP BY source_id,provider,account_id,group_id;
CREATE TABLE IF NOT EXISTS console_quality_group_links(
 check_id TEXT PRIMARY KEY REFERENCES console_quality_history(check_id) ON DELETE CASCADE,
 account_id TEXT NOT NULL, group_id BIGINT NOT NULL,
 linked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(account_id,group_id) REFERENCES console_sub_targets(account_id,group_id) ON DELETE CASCADE);
CREATE INDEX IF NOT EXISTS console_quality_group_links_scope ON console_quality_group_links(account_id,group_id);
CREATE OR REPLACE VIEW console_quality_group_history AS
 SELECT h.id,h.check_id,h.model,h.rule,h.checked_at,h.deadline,h.verdict,h.successful,h.result,h.source_id,h.provider,
 COALESCE(h.account_id,l.account_id) AS account_id,COALESCE(h.group_id,l.group_id) AS group_id
 FROM console_quality_history h LEFT JOIN console_quality_group_links l USING(check_id)
 WHERE h.account_id IS NOT NULL OR l.account_id IS NOT NULL;`

type qualitySummary struct {
	Total      int64 `json:"total"`
	Successful int64 `json:"successful"`
	Passed     int64 `json:"passed"`
}

type qualityScope struct {
	Source, Provider, Account string
	Group                     int64
}

func (q qualityScope) where() (string, []any) {
	if q.Source != "" {
		return "source_id=$1 AND provider=$2", []any{q.Source, q.Provider}
	}
	return "account_id=$1 AND group_id=$2", []any{q.Account, q.Group}
}

func (s *controlStore) beginQuality(ctx context.Context, checkID string, q qualityScope, timeout time.Duration) error {
	var source, provider, account, group any
	if q.Source != "" {
		source, provider = q.Source, q.Provider
	} else {
		account, group = q.Account, q.Group
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO console_quality_history(check_id,source_id,provider,account_id,group_id,checked_at,deadline) VALUES($1,$2,$3,$4,$5,$6,$7)`, checkID, source, provider, account, group, time.Now().Unix(), time.Now().Add(timeout))
	return err
}

// A fresh context preserves failures/cancellations even after the request or
// job has been cancelled. Repeated finalization cannot count an attempt twice.
func (s *controlStore) finishQuality(checkID, verdict string, successful bool, checkedAt int64, result any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE console_quality_history SET verdict=$2,successful=$3,checked_at=$4,result=$5 WHERE check_id=$1 AND verdict='running'`, checkID, verdict, successful, checkedAt, string(raw))
	return err
}

func (s *controlStore) qualitySummary(ctx context.Context, q qualityScope) (qualitySummary, error) {
	where, args := q.where()
	var out qualitySummary
	err := s.db.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE successful),count(*) FILTER(WHERE successful AND verdict='pass') FROM console_quality_history WHERE `+where, args...).Scan(&out.Total, &out.Successful, &out.Passed)
	return out, err
}

func (s *Service) qualityHistory(w http.ResponseWriter, r *http.Request) {
	q := qualityScope{Source: r.PathValue("id"), Provider: r.URL.Query().Get("provider")}
	if r.PathValue("group") != "" {
		group, err := strconv.ParseInt(r.PathValue("group"), 10, 64)
		if err != nil || group < 1 {
			http.Error(w, "无效分组", 400)
			return
		}
		owner, _ := s.controlUser(r)
		var exists bool
		err = s.control.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id WHERE a.owner=$1 AND a.id=$2 AND t.group_id=$3)`, owner, q.Source, group).Scan(&exists)
		if err != nil {
			http.Error(w, "检测历史暂不可用", 503)
			return
		}
		if !exists {
			http.Error(w, "分组不存在", 404)
			return
		}
		q = qualityScope{Account: q.Source, Group: group}
	} else {
		if q.Provider == "" || q.Source == "all" {
			http.Error(w, "请选择来源和渠道", 400)
			return
		}
		if _, err := s.control.source(r.Context(), q.Source); err != nil {
			http.Error(w, "来源不存在", 404)
			return
		}
	}
	before := int64(0)
	if raw := r.URL.Query().Get("before"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			http.Error(w, "无效历史游标", 400)
			return
		}
	}
	owner, _ := s.controlUser(r)
	shared, err := s.sharedQuality(r.Context(), owner)
	if err != nil {
		http.Error(w, "检测历史暂不可用", 503)
		return
	}
	table := "console_quality_history"
	if q.Account != "" {
		table = "console_quality_group_history"
	} else {
		for _, binding := range shared.Bindings {
			if binding.Source == q.Source && binding.Provider == q.Provider {
				q = qualityScope{Account: binding.Account, Group: binding.Group}
				table = "console_quality_group_history"
				break
			}
		}
	}
	where, args := q.where()
	var summary qualitySummary
	err = s.control.db.QueryRowContext(r.Context(), `SELECT count(*),count(*) FILTER(WHERE successful),count(*) FILTER(WHERE successful AND verdict='pass') FROM `+table+` WHERE `+where, args...).Scan(&summary.Total, &summary.Successful, &summary.Passed)
	if err != nil {
		http.Error(w, "检测历史暂不可用", 503)
		return
	}
	args = append(args, before)
	rows, err := s.control.db.QueryContext(r.Context(), `SELECT id,model,rule,checked_at,CASE WHEN verdict='running' AND deadline<now() THEN 'interrupted' ELSE verdict END,successful,result FROM `+table+` WHERE `+where+` AND ($3::BIGINT=0 OR id<$3) ORDER BY id DESC LIMIT 31`, args...)
	if err != nil {
		http.Error(w, "检测历史暂不可用", 503)
		return
	}
	defer rows.Close()
	type entry struct {
		ID         string          `json:"id"`
		Model      string          `json:"model"`
		Rule       string          `json:"rule"`
		CheckedAt  int64           `json:"checked_at"`
		Verdict    string          `json:"verdict"`
		Successful bool            `json:"successful"`
		Result     json.RawMessage `json:"result"`
	}
	data := []entry{}
	for rows.Next() {
		var item entry
		if err = rows.Scan(&item.ID, &item.Model, &item.Rule, &item.CheckedAt, &item.Verdict, &item.Successful, &item.Result); err != nil {
			break
		}
		data = append(data, item)
	}
	if err != nil || rows.Err() != nil {
		http.Error(w, "检测历史读取失败", 503)
		return
	}
	next := ""
	if len(data) > 30 {
		data = data[:30]
		next = fmt.Sprint(data[len(data)-1].ID)
	}
	writeJSON(w, 200, map[string]any{"data": data, "summary": summary, "next": next})
}

// A compact projection keeps channel observation from downloading availability
// replies for every other model merely to display quality probabilities.
func (s *Service) subQualitySummary(w http.ResponseWriter, r *http.Request) {
	owner, _ := s.controlUser(r)
	shared, err := s.sharedQuality(r.Context(), owner)
	if err != nil {
		http.Error(w, "检测统计暂不可用", 503)
		return
	}
	type item struct {
		Account string         `json:"account_id"`
		Group   int64          `json:"group_id"`
		Check   *ChannelCheck  `json:"check"`
		History qualitySummary `json:"history"`
	}
	data := []item{}
	for key, p := range shared.Groups {
		if p.Check == nil {
			continue
		}
		pos := strings.LastIndex(key, ":")
		group, _ := strconv.ParseInt(key[pos+1:], 10, 64)
		data = append(data, item{key[:pos], group, p.Check, p.History})
	}
	writeJSON(w, 200, map[string]any{"data": data})
}

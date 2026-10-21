package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Account progress must not wait for model results, receipts, or serialization
// of every target. This path is also used while full results are refreshing.
func (s *Service) subAccountProgress(ctx context.Context, owner string, accounts []subAccount) error {
	rows, err := s.control.db.QueryContext(ctx, `SELECT a.id,
 (SELECT count(*) FROM console_sub_targets t WHERE t.account_id=a.id AND t.active),
 (SELECT count(*) FROM console_sub_targets t WHERE t.account_id=a.id AND t.active AND t.state='done'),
 EXISTS(SELECT 1 FROM console_sub_check_queue q WHERE q.account_id=a.id)
 FROM console_sub_accounts a WHERE a.owner=$1`, owner)
	if err != nil {
		return err
	}
	defer rows.Close()
	index := make(map[string]*subAccount, len(accounts))
	for i := range accounts {
		index[accounts[i].ID] = &accounts[i]
	}
	for rows.Next() {
		var id string
		var active, completed int
		var queued bool
		if err := rows.Scan(&id, &active, &completed, &queued); err != nil {
			return err
		}
		if a := index[id]; a != nil {
			a.ActiveGroups, a.CompletedGroups = &active, &completed
			if queued && a.State != "running" && a.State != "queued" {
				a.State = "queued"
			}
		}
	}
	return rows.Err()
}

// The list used to aggregate the entire result JSON once per target, then sort
// those large aggregates before returning any row. Read models as one stream
// instead. Keep authorization and target membership in the SQL, and retain every
// model/state used by list filters.
func (s *Service) subReadModels(ctx context.Context, owner, accountID string, groupID int64, model string, summary bool, accounts []subAccount) (int, int, error) {
	targets := map[string]*subTarget{}
	for i := range accounts {
		for j := range accounts[i].Targets {
			t := &accounts[i].Targets[j]
			targets[qualityGroupID(accounts[i].ID, t.GroupID)] = t
		}
	}
	if len(targets) == 0 {
		return 0, 0, nil
	}
	// Read the transactional projection without touching full probe bodies.
	// Keep probe IDs until current receipts are attached; details use the original.
	resultSQL := "m.result"
	projectionJoin := ""
	if summary {
		// COALESCE never detoasts the full result when its transactional
		// projection exists. Legacy rows remain readable during backfill.
		resultSQL = "COALESCE(ms.result, console_sub_model_summary_v1(m.result))"
		projectionJoin = " LEFT JOIN console_sub_model_summaries ms ON ms.account_id=m.account_id AND ms.group_id=m.group_id AND ms.model=m.model "
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT m.account_id,m.group_id,m.model,m.state,m.message,`+resultSQL+`
FROM console_sub_models m `+projectionJoin+`
JOIN console_sub_targets t ON t.account_id=m.account_id AND t.group_id=m.group_id
JOIN console_sub_accounts a ON a.id=t.account_id
WHERE a.owner=$1 AND ($2='' OR a.id=$2) AND ($3=0 OR t.group_id=$3)
AND ($4='' OR m.model=$4 OR m.model='gpt-6-astra')`, owner, accountID, groupID, model)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	count, resultBytes := 0, 0
	for rows.Next() {
		var account string
		var group int64
		var m subModelResult
		var raw []byte
		if err := rows.Scan(&account, &group, &m.Model, &m.State, &m.Message, &raw); err != nil {
			return count, resultBytes, err
		}
		resultBytes += len(raw)
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &m.Result); err != nil {
				return count, resultBytes, err
			}
		}
		if summary {
			compactSubModelResult(m.Result)
		}
		if target := targets[qualityGroupID(account, group)]; target != nil {
			target.Models = append(target.Models, m)
			count++
		}
	}
	if err := rows.Err(); err != nil {
		return count, resultBytes, err
	}
	for _, target := range targets {
		sort.Slice(target.Models, func(i, j int) bool { return target.Models[i].Model < target.Models[j].Model })
	}
	return count, resultBytes, nil
}

// Discard detail as each row arrives, before retaining the list. IDs must stay
// until subAttachUsage attaches current receipts. The result is request-local.
// compactSummary later removes IDs and truncates messages for the response.
func compactSubModelResult(result *subResult) {
	if result == nil {
		return
	}
	for _, probe := range []*subProbe{&result.Availability, &result.Quality} {
		probe.CurlToken, probe.Text, probe.StartedAt, probe.RequestIDs = "", "", 0, nil
		if usage := probe.Usage; usage != nil {
			probe.Usage = &subUsage{Status: usage.Status, InputPrice: usage.InputPrice, OutputPrice: usage.OutputPrice, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}
		}
	}
}

// Log only timings/counts and cancellation state, never account identifiers,
// probe contents, tokens or database errors that could contain SQL arguments.
type subAccountsReadTrace struct {
	r                         *http.Request
	start, last               time.Time
	stage                     string
	stages                    []string
	accounts, targets, models int
	modelBytes                int
}

func newSubAccountsReadTrace(r *http.Request) *subAccountsReadTrace {
	now := time.Now()
	return &subAccountsReadTrace{r: r, start: now, last: now, stage: "accounts"}
}

func (t *subAccountsReadTrace) next(stage string) {
	now := time.Now()
	t.stages = append(t.stages, fmt.Sprintf("%s_ms=%d", t.stage, now.Sub(t.last).Milliseconds()))
	t.stage, t.last = stage, now
}

func (t *subAccountsReadTrace) finish() {
	elapsed := time.Since(t.start)
	if elapsed < 2*time.Second && t.r.Context().Err() == nil {
		return
	}
	lastStage := t.stage
	t.next("done")
	view := summaryView(t.r)
	if view != "summary" && view != "accounts" && view != "rates" {
		view = "detail"
	}
	log.Printf("sub_accounts_read view=%s elapsed_ms=%d last_stage=%s canceled=%t accounts=%d targets=%d models=%d model_result_bytes=%d request_id=%s %s",
		view, elapsed.Milliseconds(), lastStage, t.r.Context().Err() != nil, t.accounts, t.targets, t.models, t.modelBytes, consoleRequestID(t.r.Context()), strings.Join(t.stages, " "))
}

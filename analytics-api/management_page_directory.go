package main

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
)

// The current directory already read these exact source columns under this
// transaction's ownership/model proof. Match the summary by its complete
// primary key. PostgreSQL estimates jsonb_to_recordset at 100 rows; an
// uncorrelated summary batch can consequently be rescanned for EVERY model of
// a real page with thousands of rows. Keep the lookup and fallback correlated.
const managementPageDirectoryModelsSQL = `WITH wanted AS MATERIALIZED (
 SELECT w.* FROM jsonb_to_recordset($2::jsonb) AS w(account_id text,group_id bigint)
 JOIN console_sub_accounts a ON a.id=w.account_id WHERE a.owner=$1),
 directory AS MATERIALIZED (
 SELECT m.* FROM jsonb_to_recordset($3::jsonb) AS m(account_id text,group_id bigint,model text,state text,message text)
 JOIN wanted USING(account_id,group_id))
 SELECT m.account_id,m.group_id,m.model,m.state,m.message,COALESCE(ms.result,console_sub_model_summary_v1(raw.result))
 FROM directory m LEFT JOIN LATERAL (SELECT result FROM console_sub_model_summaries ms
 WHERE ms.account_id=m.account_id AND ms.group_id=m.group_id AND ms.model=m.model OFFSET 0) ms ON true
 LEFT JOIN LATERAL (SELECT source.result FROM console_sub_models source
 WHERE ms.result IS NULL AND source.account_id=m.account_id AND source.group_id=m.group_id AND source.model=m.model OFFSET 0) raw ON true`

type managementPageDirectoryModel struct {
	Account string `json:"account_id"`
	Group   int64  `json:"group_id"`
	Model   string `json:"model"`
	State   string `json:"state"`
	Message string `json:"message"`
}

const managementPageDirectoryBudget = 8 << 20

// Binary-search the immutable directory's existing byte order. No new cache,
// time-based freshness, mutable graph or model evidence is introduced. Missing
// proof/order/budget admission keeps the original source+summary query.
func managementPageDirectoryModels(ctx context.Context, db controlQueryDB, owner string, wanted []subBoundKey) ([]managementPageDirectoryModel, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	scope := managementDirectoryScopeFor(ctx, db, owner)
	if scope == nil || !scope.ready || scope.accountRevision == "" || scope.directory == nil || !scope.directory.ordered {
		return nil, false
	}
	entry := scope.directory
	if entry.version != (managementDirectoryVersion{scope.database, owner, scope.accountRevision, scope.modelRevision}) {
		return nil, false
	}
	rowAt := func(i int) managementDirectoryRow {
		if entry.order != nil {
			i = int(entry.order[i])
		}
		return entry.rows[i]
	}
	out := []managementPageDirectoryModel{}
	bytes := 2 // JSON array brackets.
	seen := map[managementStreamIdentity]bool{}
	for _, group := range wanted {
		key := managementStreamIdentity{account: group.AccountID, group: group.GroupID}
		if seen[key] {
			continue
		}
		seen[key] = true
		start := sort.Search(len(entry.rows), func(i int) bool {
			row := rowAt(i)
			c := strings.Compare(entry.strings[row.account], group.AccountID)
			return c > 0 || c == 0 && row.group >= group.GroupID
		})
		for i := start; i < len(entry.rows); i++ {
			row := rowAt(i)
			account := entry.strings[row.account]
			if account != group.AccountID || row.group != group.GroupID {
				break
			}
			model := managementPageDirectoryModel{account, row.group, entry.strings[row.model], entry.strings[row.state], entry.strings[row.message]}
			// Account for worst-case JSON string escaping BEFORE marshaling.
			// A dictionary may share a long message across many source rows.
			n := 128 // Fixed keys, punctuation, comma and a signed int64 group.
			for _, value := range []string{model.Account, model.Model, model.State, model.Message} {
				if n > managementPageDirectoryBudget-bytes || len(value) > (managementPageDirectoryBudget-bytes-n)/6 {
					return nil, false
				}
				n += 6 * len(value)
			}
			bytes += n
			out = append(out, model)
		}
	}
	return out, true
}

func managementPageModelQuery(ctx context.Context, db controlQueryDB, owner string, wanted []subBoundKey, raw []byte) (string, []any, error) {
	query, args := managementPageModelsSQL, []any{owner, string(raw)}
	if models, ok := managementPageDirectoryModels(ctx, db, owner, wanted); ok {
		encoded, err := json.Marshal(models)
		if err != nil {
			return "", nil, err
		}
		query, args = managementPageDirectoryModelsSQL, append(args, string(encoded))
		if managementTraceEnabled(ctx) {
			log.Printf("management_page_model_directory reuse=true rows=%d request_id=%s", len(models), consoleRequestID(ctx))
		}
	}
	return query, args, nil
}

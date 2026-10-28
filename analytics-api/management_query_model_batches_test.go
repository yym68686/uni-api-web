package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"
)

// Independent pre-batching query. Compare full row multisets, including raw
// JSON and duplicate requested groups, without relying on either plan's order.
const managementPageModelsBeforeBatchesSQL = `SELECT m.account_id,m.group_id,m.model,m.state,m.message,COALESCE(ms.result,console_sub_model_summary_v1(m.result))
 FROM jsonb_to_recordset($2::jsonb) AS wanted(account_id text,group_id bigint)
 JOIN console_sub_models m USING(account_id,group_id)
 JOIN console_sub_accounts a ON a.id=m.account_id
 LEFT JOIN console_sub_model_summaries ms USING(account_id,group_id,model)
 WHERE a.owner=$1`

func TestManagementPageModelBatchesPreserveLegacyRowsAndOwnerScope(t *testing.T) {
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`
 INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('foreign','other','foreign','https://foreign.test','f',''),('b','owner','second','https://b.test','b','');
 INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('foreign',1,'private','openai'),('b',1,'second','openai'),('b',2,'second-two','openai');
 INSERT INTO console_sub_models(account_id,group_id,model,state,message,result) VALUES
 ('foreign',1,'extra-model','done','private','{"checked_at":999}'),
 ('b',1,'extra-model','done','other account','{"checked_at":456}'),('b',2,'extra-model','idle','empty','null'),
 ('a',1,'null-result','idle','',NULL),('a',2,'bad-result','done','raw','{"availability":true}'),
 ('a',3,'unicode-模型🙂','done','long','{"availability":{"status":"success","ttft_ms":0}}');
 DELETE FROM console_sub_model_summaries WHERE account_id='a' AND (group_id=1 OR model='unicode-模型🙂')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	read := func(t *testing.T, query, owner, raw string) []string {
		t.Helper()
		rows, err := tx.QueryContext(ctx, query, owner, raw)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var account, model, state, message string
			var group int64
			var result []byte
			if err := rows.Scan(&account, &group, &model, &state, &message, &result); err != nil {
				t.Fatal(err)
			}
			out = append(out, mustJSON([]any{account, group, model, state, message, json.RawMessage(result)}))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(out)
		return out
	}
	all := []subBoundKey{{AccountID: "foreign", GroupID: 1}, {AccountID: "a", GroupID: 1}, {AccountID: "b", GroupID: 1}, {AccountID: "b", GroupID: 2}, {AccountID: "missing", GroupID: 1}}
	for group := int64(1); group <= 61; group++ {
		all = append(all, subBoundKey{AccountID: "a", GroupID: group})
	}
	for _, owner := range []string{"owner", "other", "nobody", ""} {
		for _, wanted := range [][]subBoundKey{nil, {}, {{AccountID: "a", GroupID: 1}}, {{AccountID: "a", GroupID: 999}}, all} {
			raw := mustJSON(wanted)
			// Callers send an array even when no groups are requested.
			if wanted == nil {
				raw = "[]"
			}
			t.Run(fmt.Sprintf("%s/%d", owner, len(wanted)), func(t *testing.T) {
				old := read(t, managementPageModelsBeforeBatchesSQL, owner, raw)
				current := read(t, managementPageModelsSQL, owner, raw)
				if !reflect.DeepEqual(old, current) {
					t.Fatalf("batched rows differ: old=%d current=%d", len(old), len(current))
				}
				if owner == "nobody" || owner == "" {
					if len(current) != 0 {
						t.Fatal("foreign page evidence escaped owner scope")
					}
				}
			})
		}
	}
}

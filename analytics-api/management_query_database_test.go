package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestManagementDatabaseSnapshotKeepsPricesModelsReceiptsAndQueueConsistent(t *testing.T) {
	ctx := context.Background()
	// Configuration sources are global to a control schema. Other test fixtures
	// deliberately use different encryption keys, so isolate the whole schema,
	// not just this owner's account rows.
	db := migrationTestDB(t)
	if err := migrateControlSchema(ctx, db, controlMigrations()); err != nil {
		t.Fatal(err)
	}
	s := &Service{control: &controlStore{db: db, key: []byte(strings.Repeat("u", 32))}, engine: postgresTestEngine(t)}
	account, owner := "account", "owner"
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.control.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'fixture','https://fixture.test','fixture@example.test','')`, account, owner)
	exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id) VALUES($1,7,'group','openai',42)`, account)
	result := subResult{Model: "gpt-5.5", CheckedAt: 1, Availability: subProbe{ID: account + "-probe", Status: "success", Text: "private-reply", CurlToken: "private-curl", Usage: &subUsage{Status: "pending"}}, Quality: subProbe{Status: "skipped"}}
	exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,7,'gpt-5.5','done',$2)`, account, mustJSON(result))
	exec(`INSERT INTO console_sub_usage(id,account_id,group_id,key_id,model,started_at,request_ids,status,result) VALUES($1,$2,7,42,'gpt-5.5',1,'[]','matched',$3)`, account+"-probe", account, mustJSON(subUsage{Status: "matched", ActualCost: usageFloat(.5), TotalCost: usageFloat(1), InputPrice: usageFloat(2), OutputPrice: usageFloat(3), InputTokens: usageInt(100), OutputTokens: usageInt(20)}))
	price := Price{Model: "gpt-5.5", Input: 2, Output: 3, Verified: true, Source: "manual", EffectiveAt: time.Now()}
	if err := s.engine.SavePrice(ctx, price); err != nil {
		t.Fatal(err)
	}
	// Establish one repeatable-read view before an independent writer commits.
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := readSubAccountHeaders(ctx, tx, owner, ""); err != nil {
		t.Fatal(err)
	}
	write, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer write.Rollback()
	result.CheckedAt = 2
	result.Availability.Status = "error"
	for _, statement := range []struct {
		q    string
		args []any
	}{
		{`UPDATE console_sub_models SET result=$2 WHERE account_id=$1`, []any{account, mustJSON(result)}},
		{`UPDATE console_sub_usage SET result=$2 WHERE id=$1`, []any{account + "-probe", mustJSON(subUsage{Status: "matched", ActualCost: usageFloat(.25), TotalCost: usageFloat(1), InputPrice: usageFloat(9), OutputPrice: usageFloat(3)})}},
		{`INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES($1,7,'check','gpt-5.5')`, []any{account}},
		{`UPDATE ` + s.engine.historyPath + `.prices SET input=9 WHERE model='gpt-5.5'`, nil},
	} {
		if _, err := write.ExecContext(ctx, statement.q, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := write.Commit(); err != nil {
		t.Fatal(err)
	}
	old, err := s.readManagementDatabaseFrom(ctx, tx, owner)
	if err != nil {
		t.Fatalf("snapshot read: %v; cause: %v", err, errors.Unwrap(err))
	}
	assert := func(view managementDatabaseView, at int64, state, status string, input, rate float64) {
		t.Helper()
		if len(view.Input.Accounts) != 1 || len(view.Input.Accounts[0].Targets) != 1 {
			t.Fatal("owner scope changed")
		}
		target := view.Input.Accounts[0].Targets[0]
		if len(target.Models) != 1 {
			t.Fatal("model scope changed")
		}
		check := target.Models[0]
		if check.State != state || check.Result.CheckedAt != at || check.Result.Availability.Status != status || *check.Result.Availability.Usage.InputPrice != input || target.ObservedRates["gpt-5.5"].Rate != rate {
			t.Fatalf("mixed view: %s", mustJSON(target))
		}
		found := false
		for _, price := range view.Prices {
			if price.Model == "gpt-5.5" {
				found = true
				if price.Input != input {
					t.Fatal("price escaped snapshot")
				}
			}
		}
		if !found {
			t.Fatal("price missing")
		}
		raw := mustJSON(view.Input.Accounts)
		for _, private := range []string{"private-reply", "private-curl", account + "-probe", "request_ids"} {
			if strings.Contains(raw, private) {
				t.Fatal("page retained private probe detail", private)
			}
		}
	}
	assert(old, 1, "done", "success", 2, .5)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Rollback()
	current, err := s.readManagementDatabaseFrom(ctx, fresh, owner)
	if err != nil {
		t.Fatal(err)
	}
	assert(current, 2, "queued", "error", 9, .25)
	if current.Input.Accounts[0].State != "queued" {
		t.Fatal("queue state not current")
	}
}

func TestManagementAccountNormalizationMatchesWireSummary(t *testing.T) {
	probe := subProbe{ID: "id", Text: "reply", CurlToken: "curl", StartedAt: 1, RequestIDs: []string{"secret"}, Message: strings.Repeat("消息", 200), Status: "success", Usage: &subUsage{Status: "matched", InputPrice: usageFloat(0), InputTokens: usageInt(0), CacheReadTokens: usageInt(2), ActualCost: usageFloat(1)}}
	result := subResult{Model: "gpt-5.5", Availability: probe, Quality: probe}
	accounts := []subAccount{{Targets: []subTarget{{GroupID: 1, Result: &result, Models: []subModelResult{{Model: "gpt-5.5", State: "done", Message: strings.Repeat("消息", 200), Result: &result}}, QualityCheck: &ChannelCheck{QualityProbe: &probe}, ToolUse: &subCapabilityResult{Attempts: []subProbe{probe}}}}}}
	var expected subTarget
	raw, err := json.Marshal(compactAccountTarget(accounts[0].Targets[0]))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	if err := normalizeManagementAccounts(accounts); err != nil {
		t.Fatal(err)
	}
	if mustJSON(accounts[0].Targets[0]) != mustJSON(expected) {
		t.Fatalf("normalization differs from existing summary\ngot %s\nwant %s", mustJSON(accounts[0].Targets[0]), mustJSON(expected))
	}
}

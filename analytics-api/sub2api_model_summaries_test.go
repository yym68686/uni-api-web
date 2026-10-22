package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSubModelStoredSummaryPreservesEvidence(t *testing.T) {
	db := migrationTestDB(t)
	ctx := context.Background()
	if err := migrateControlSchema(ctx, db, controlMigrations()); err != nil {
		t.Fatal(err)
	}
	price, tokens := 1.5, int64(17)
	p := subProbe{ID: "receipt", Text: "private reply", CurlToken: "private curl", RequestIDs: []string{"private request"}, StartedAt: 17,
		Status: "success", TerminalStatus: "completed", RequestedModel: "custom-model", ResponseModel: "custom-model", ModelMatch: "match", Protocol: "gemini",
		TTFT: &tokens, FirstResponseMS: &tokens, ResponseCreatedMS: &tokens, Duration: 42, HTTPStatus: 200, Message: strings.Repeat("长🙂", 100),
		Usage: &subUsage{Status: "matched", InputPrice: &price, OutputPrice: &price, InputTokens: &tokens, OutputTokens: &tokens, Message: "private usage", RequestID: "private receipt", ActualCost: &price}}
	for _, raw := range []string{`null`, `{}`, `{"availability":null,"quality":null}`, `{"availability":{"usage":{}},"quality":{"usage":null}}`, mustJSON(subResult{Model: "custom-model", CheckedAt: 100, Verdict: "pass", Availability: p, Quality: p})} {
		var projected []byte
		if err := db.QueryRow(`SELECT console_sub_model_summary_v1($1::jsonb)`, raw).Scan(&projected); err != nil {
			t.Fatal(err)
		}
		var full, slim *subResult
		if err := json.Unmarshal([]byte(raw), &full); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(projected, &slim); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(compactSummary(full), compactSummary(slim)) {
			t.Fatal("stored projection changed list evidence", string(projected))
		}
		if strings.Contains(string(projected), "private") || full != nil && full.Availability.ID != slim.Availability.ID {
			t.Fatal("detail retained or receipt association lost")
		}
	}
}

func TestSubModelSummaryRollingBackfillAndLegacyWrites(t *testing.T) {
	db := migrationTestDB(t)
	ctx := context.Background()
	migrations := controlMigrations()
	if err := migrateControlSchema(ctx, db, migrations[:2]); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('legacy','owner','legacy','https://fixture.test','fixture','')`)
	exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('legacy',1,'g','openai')`)
	exec(`INSERT INTO console_sub_models(account_id,group_id,model,result) SELECT 'legacy',1,'m-'||lpad(n::text,3,'0'),CASE WHEN n=0 THEN NULL ELSE jsonb_build_object('model','m-'||n,'availability',jsonb_build_object('id','p-'||n,'status','success','text','original detail')) END FROM generate_series(0,269)n`)
	if err := migrateControlSchema(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM console_sub_model_summaries`).Scan(&n); err != nil || n != 0 {
		t.Fatal("DDL performed a locked historical backfill", n, err)
	}
	s := &Service{control: &controlStore{db: db}}
	read := func(summary bool) []subModelResult {
		t.Helper()
		accounts := []subAccount{{ID: "legacy", Targets: []subTarget{{GroupID: 1}}}}
		if n, _, err := s.subReadModels(ctx, "owner", "", 0, "", summary, accounts); err != nil || n != 270 {
			t.Fatal(n, err)
		}
		return accounts[0].Targets[0].Models
	}
	before := read(true)
	var cursor subModelSummaryCursor
	var err error
	for batch, want := range []int{128, 128, 14, 0} {
		var count int
		var done bool
		cursor, count, done, err = s.control.backfillSubModelSummaries(ctx, cursor)
		if err != nil || count != want || done != (batch == 3) {
			t.Fatal(batch, count, done, err)
		}
	}
	after := read(true)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("legacy fallback differs from stored projection")
	}
	if read(false)[1].Result.Availability.Text != "original detail" {
		t.Fatal("backfill modified detail")
	}
	// A new replica must advance through already-filled identities in bounded
	// pages. Filtering missing rows before LIMIT scanned the complete filled
	// history, exceeded the production 2s budget, and retried from zero forever.
	restarted, count, done, err := s.control.backfillSubModelSummaries(ctx, subModelSummaryCursor{})
	if err != nil || count != 0 || done || restarted.model != "m-127" {
		t.Fatal("restart scanned beyond the first bounded page", restarted, count, done, err)
	}
	// Writes in the old application shape must atomically update the summary.
	exec(`UPDATE console_sub_models SET state='done',result='{"availability":{"id":"new-probe","status":"error","text":"new private detail"}}' WHERE model='m-001'`)
	if p := read(true)[1].Result.Availability; p.ID != "new-probe" || p.Status != "error" || p.Text != "" {
		t.Fatal("legacy writer left a stale projection", p)
	}
	exec(`UPDATE console_sub_models SET state='running',message='queued' WHERE model='m-001'`)
	if m := read(true)[1]; m.State != "running" || m.Message != "queued" || m.Result.Availability.ID != "new-probe" {
		t.Fatal("state or evidence stale")
	}
	exec(`UPDATE console_sub_models SET result=NULL WHERE model='m-001'`)
	if read(true)[1].Result != nil {
		t.Fatal("NULL reset lost")
	}
	exec(`DELETE FROM console_sub_accounts WHERE id='legacy'`)
	if err := db.QueryRow(`SELECT count(*) FROM console_sub_model_summaries`).Scan(&n); err != nil || n != 0 {
		t.Fatal("deletion left summaries", n, err)
	}
}

func TestSubModelSummaryBackfillCannotOverwriteConcurrentResult(t *testing.T) {
	db := migrationTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := migrateControlSchema(ctx, db, controlMigrations()); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('race','owner','race','https://fixture.test','fixture','')`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('race',1,'g','openai')`,
		`INSERT INTO console_sub_models(account_id,group_id,model,result) VALUES('race',1,'m','{"checked_at":1}')`,
		`DELETE FROM console_sub_model_summaries`, // simulate a pre-migration row
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.ExecContext(ctx, `UPDATE console_sub_models SET result='{"checked_at":2}' WHERE account_id='race'`); err != nil {
		t.Fatal(err)
	}
	store := &controlStore{db: db}
	result := make(chan error, 1)
	go func() {
		_, _, _, err := store.backfillSubModelSummaries(ctx, subModelSummaryCursor{})
		result <- err
	}()
	// Observe the backfill waiting on the uncommitted projection's uniqueness
	// check. Its SELECT snapshot has already read the older model result.
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE 'WITH batch AS MATERIALIZED%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if !waitStartup(ctx, time.Millisecond) {
			t.Fatal("backfill did not reach conflict")
		}
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	// Lock timeout is deliberately short in production; either a successful
	// conflict or a retriable canceled batch must leave the new result intact.
	<-result
	if _, _, _, err := store.backfillSubModelSummaries(ctx, subModelSummaryCursor{}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT result::text FROM console_sub_model_summaries`).Scan(&raw); err != nil || raw != `{"checked_at": 2}` {
		t.Fatal(fmt.Sprintf("new result overwritten: %s %v", raw, err))
	}
}

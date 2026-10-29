package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func targetCacheScopeForTest() (*managementTargetScope, context.Context) {
	scope := &managementTargetScope{store: &managementTargetStreams{}, tx: &sql.Tx{}, version: managementTargetVersion{&sql.DB{}, "owner", "1", "2", "3"}}
	return scope, context.WithValue(context.Background(), managementTargetScopeKey{}, scope)
}

func TestManagementTargetCacheRequiresExactSnapshotAndRawIndependentRows(t *testing.T) {
	for _, name := range []string{"hit", "disabled", "owner", "transaction", "pool", "missing_accounts", "missing_targets", "missing_caps", "accounts_changed", "targets_changed", "caps_changed", "database_changed", "no_scope"} {
		t.Run(name, func(t *testing.T) {
			scope, ctx := targetCacheScopeForTest()
			row := managementTargetRecord{account: "a", target: subTarget{GroupID: 7, Name: "雪", Rate: 0.5, Active: true}, result: []byte(`null`), billing: []byte(`{}`), compact: []byte{}, tools: nil}
			calls := 0
			read := func(consume managementTargetConsumer) error { calls++; return consume(row, nil) }
			var got managementTargetRecord
			consume := func(r managementTargetRecord, _ *managementRowsTrace) error { got = r; return nil }
			if err := readManagementTargetStream(ctx, scope.tx, "owner", true, read, consume); err != nil || calls != 1 {
				t.Fatal(err, calls)
			}
			var db controlQueryDB = scope.tx
			owner, enabled := "owner", true
			switch name {
			case "disabled":
				enabled = false
			case "owner":
				owner = "foreign"
			case "transaction":
				db = &sql.Tx{}
			case "pool":
				db = scope.version.database
			case "missing_accounts":
				scope.version.accounts = ""
			case "missing_targets":
				scope.version.targets = ""
			case "missing_caps":
				scope.version.caps = ""
			case "accounts_changed":
				scope.version.accounts = "2"
			case "targets_changed":
				scope.version.targets = "3"
			case "caps_changed":
				scope.version.caps = "4"
			case "database_changed":
				scope.version.database = &sql.DB{}
			case "no_scope":
				ctx = context.Background()
			}
			if err := readManagementTargetStream(ctx, db, owner, enabled, read, consume); err != nil {
				t.Fatal(err)
			}
			wantCalls := 2
			if name == "hit" {
				wantCalls = 1
			}
			if calls != wantCalls || !reflect.DeepEqual(got, row) {
				t.Fatal("scope fence or raw nil/empty shape changed", calls, wantCalls)
			}
			if name == "hit" {
				got.result[0], row.result[0] = 'x', 'y'
				if err := readManagementTargetStream(ctx, db, owner, true, read, consume); err != nil || string(got.result) != "null" {
					t.Fatal("caller/source bytes alias retained rows", err)
				}
				encoded := fmt.Sprint(scope.store.entries[0].version.targets)
				if encoded != "2" {
					t.Fatal("version mutated")
				}
			}
		})
	}
}

func TestManagementTargetCacheDoesNotAdmitErrorsCanceledOrOversizeStreams(t *testing.T) {
	for _, name := range []string{"source_error", "decoder_error", "canceled", "oversize", "expanded"} {
		t.Run(name, func(t *testing.T) {
			scope, ctx := targetCacheScopeForTest()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			sentinel := errors.New("fixture failure")
			row := managementTargetRecord{account: "a", result: []byte(`{}`)}
			if name == "oversize" {
				row.result = make([]byte, managementTargetBudget)
			}
			if name == "expanded" {
				row.target.Result = &subResult{}
			}
			read := func(consume managementTargetConsumer) error {
				if err := consume(row, nil); err != nil {
					return err
				}
				if name == "source_error" {
					return sentinel
				}
				if name == "canceled" {
					cancel()
				}
				return nil
			}
			consume := func(managementTargetRecord, *managementRowsTrace) error {
				if name == "decoder_error" {
					return sentinel
				}
				return nil
			}
			err := readManagementTargetStream(ctx, scope.tx, "owner", true, read, consume)
			if (name == "source_error" || name == "decoder_error") && !errors.Is(err, sentinel) {
				t.Fatal("original error lost", err)
			}
			if name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if len(scope.store.entries) != 0 || scope.store.bytes != 0 {
				t.Fatal("incomplete/oversize stream retained")
			}
		})
	}
}

func TestManagementTargetCacheConcurrentBudgetAndEviction(t *testing.T) {
	store := &managementTargetStreams{}
	database := &sql.DB{}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				v := managementTargetVersion{database, fmt.Sprint(n), "1", fmt.Sprint(i), "1"}
				store.put(&managementTargetStream{version: v, bytes: managementTargetBudget / 2})
				if entry := store.get(v); entry != nil && entry.version != v {
					t.Error("concurrent version mismatch")
				}
			}
		}(n)
	}
	wg.Wait()
	if len(store.entries) > 2 || store.bytes > managementTargetBudget {
		t.Fatal("budget exceeded")
	}
	store.put(&managementTargetStream{version: managementTargetVersion{database, "large", "1", "1", "1"}, bytes: managementTargetBudget})
	if len(store.entries) != 1 || store.bytes != managementTargetBudget {
		t.Fatal("byte eviction failed")
	}
	for _, entry := range store.entries[len(store.entries):cap(store.entries)] {
		if entry != nil {
			t.Fatal("evicted entry still retained")
		}
	}
}

func TestManagementTargetCacheTraceSeparatesReplayFromDatabase(t *testing.T) {
	scope, ctx := targetCacheScopeForTest()
	ctx = managementTraceContext(ctx)
	row := managementTargetRecord{result: []byte(`{}`)}
	consume := func(row managementTargetRecord, trace *managementRowsTrace) error {
		start := trace.decodeStart()
		trace.decoded(start, len(row.result))
		return nil
	}
	read := func(consume managementTargetConsumer) error { return consume(row, nil) }
	if err := readManagementTargetStream(ctx, scope.tx, "owner", true, read, consume); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	if err := readManagementTargetStream(ctx, scope.tx, "owner", true, read, consume); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "stage=target_cached_rows query_ready_ms=0 ") || !strings.Contains(got, "rows=1 bytes=2 advance_ms=0 ") || strings.Contains(got, "stage=target_rows ") {
		t.Fatal("cache replay misreported as a database stream", got)
	}
}

func TestManagementTargetCacheSnapshotPreservesFullPageAndFreshEvidence(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "target-cache-123")
	filters := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	read := func() managementQuerySnapshot {
		t.Helper()
		got, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
		if err != nil {
			t.Fatal(err)
		}
		want, err := s.readManagementQuerySnapshot(context.Background(), "owner", filters, "", true)
		if err != nil || got.Page.Revision != want.Page.Revision || !reflect.DeepEqual(got.Page.Rows, want.Page.Rows) || !reflect.DeepEqual(managementContextForPage(got.View, got.Page, filters.Model), managementContextForPage(want.View, want.Page, filters.Model)) {
			t.Fatal("cache changed complete page/context/revision", err)
		}
		return got
	}
	read()
	if len(s.targetStreams.entries) != 1 {
		t.Fatal("normal snapshot did not retain target stream")
	}
	entry := s.targetStreams.entries[0]
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	read()
	if !strings.Contains(output.String(), "management_target_cache hit=true rows=61 ") {
		t.Fatal("target stream was reread")
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','987') WHERE group_id=1 AND model='gpt-5.5'; UPDATE console_sub_usage SET result='{"input_price":9,"output_price":3}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	got := read()
	check := got.Page.Rows[0].Selected.Result.Availability
	if s.targetStreams.entries[0] != entry || *check.TTFT != 987 || *check.Usage.InputPrice != 9 {
		t.Fatal("cache retained model or receipt evidence")
	}
	for _, private := range []string{"owner", "https://a.test", "gpt-5.5", "SELECT"} {
		if strings.Contains(output.String(), private) {
			t.Fatal("cache diagnostics exposed private data")
		}
	}
}

func TestManagementTargetCacheInvalidatesColumnsProjectionsAndOwnership(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	filters := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	for _, change := range []string{
		`UPDATE console_sub_targets SET name='changed',rate=0.25,state='idle',active=false WHERE group_id=1`,
		`UPDATE console_sub_targets SET result='{"availability":{"id":"new-probe"}}',billing='{"rate":0.2}',compaction='{"status":"supported"}',tool_use='{"status":"supported"}' WHERE group_id=2`,
		`UPDATE console_sub_capability_summaries SET compaction='{"status":"unsupported"}' WHERE group_id=2`,
		`DELETE FROM console_sub_capability_summaries WHERE group_id=2`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('a',100,'new','openai')`,
		`DELETE FROM console_sub_targets WHERE group_id=100`,
		`UPDATE console_sub_targets SET message='B' WHERE group_id=1; UPDATE console_sub_targets SET message='' WHERE group_id=1`,
		`UPDATE console_sub_accounts SET owner='foreign' WHERE id='a'`,
	} {
		_, err := s.readManagementQuerySnapshot(context.Background(), "owner", filters, "", false)
		if err != nil {
			t.Fatal(err)
		}
		entry := s.targetStreams.entries[0]
		if _, err := s.control.db.Exec(change); err != nil {
			t.Fatal(err)
		}
		got, err := s.readManagementQuerySnapshot(context.Background(), "owner", filters, "", false)
		if err != nil {
			t.Fatal(err)
		}
		want, err := s.readManagementQuerySnapshot(context.Background(), "owner", filters, "", true)
		if err != nil || got.Page.Revision != want.Page.Revision || !reflect.DeepEqual(got.Page.Rows, want.Page.Rows) {
			t.Fatal("stale target stream after change", err)
		}
		if s.targetStreams.entries[0] == entry {
			t.Fatal("changed source generations did not invalidate", change)
		}
	}
}

func TestManagementTargetCacheDoesNotHideNewMalformedOffPageMetadata(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	for _, change := range []string{
		`UPDATE console_sub_targets SET result='{"availability":{"ttft_ms":"invalid"}}' WHERE group_id=61`,
		`UPDATE console_sub_targets SET billing='{"rate":"invalid"}' WHERE group_id=61`,
		`UPDATE console_sub_capability_summaries SET compaction='{"checked_at":"invalid"}' WHERE group_id=61`,
		`UPDATE console_sub_capability_summaries SET tool_use='{"checked_at":"invalid"}' WHERE group_id=61`,
	} {
		if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET result=NULL,billing=NULL,compaction=NULL,tool_use=NULL`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", false); err != nil {
			t.Fatal(err)
		}
		if _, err := s.control.db.Exec(change); err != nil {
			t.Fatal(err)
		}
		_, got := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", false)
		_, want := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", true)
		if got == nil || want == nil || fmt.Sprint(got) != fmt.Sprint(want) || fmt.Sprint(errors.Unwrap(got)) != fmt.Sprint(errors.Unwrap(want)) {
			t.Fatal("cache hid original off-page typed error", change, got, want)
		}
	}
}

func TestManagementTargetCachePinnedSnapshotSurvivesConcurrentReplacement(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	if _, err := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", false); err != nil {
		t.Fatal(err)
	}
	tx, err := s.control.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	proof, _, err := s.managementReadProof(context.Background(), tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	scope := &managementTargetScope{store: &s.targetStreams, tx: tx, version: managementTargetVersion{s.control.db, "owner", proof.Epochs["console_sub_accounts"], proof.Epochs["console_sub_targets"], proof.Epochs["console_sub_capability_summaries"]}}
	ctx := context.WithValue(context.Background(), managementTargetScopeKey{}, scope)
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET name='new generation' WHERE group_id=1`); err != nil {
		t.Fatal(err)
	}
	consume := func(r managementTargetRecord, _ *managementRowsTrace) error {
		if r.target.GroupID == 1 && r.target.Name != "G1" {
			t.Fatal("new commit escaped old RR snapshot")
		}
		return nil
	}
	read := func(consume managementTargetConsumer) error {
		rows, err := tx.Query(`SELECT t.account_id,t.group_id,t.name FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id WHERE a.owner='owner' ORDER BY t.group_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r managementTargetRecord
			if err := rows.Scan(&r.account, &r.target.GroupID, &r.target.Name); err != nil {
				return err
			}
			if err := consume(r, nil); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	// The original generation hits without any SQL read callback.
	if err := readManagementTargetStream(ctx, tx, "owner", true, func(managementTargetConsumer) error { return errors.New("cache missed pinned generation") }, consume); err != nil {
		t.Fatal(err)
	}
	current, err := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", false)
	if err != nil || current.Page.Rows[0].Target.Name != "new generation" {
		t.Fatal("new snapshot reused old target", err)
	}
	// A concurrent request replaced the retained generation. Falling back to SQL
	// must still read this old transaction, not whichever generation is newest.
	if err := readManagementTargetStream(ctx, tx, "owner", true, read, consume); err != nil {
		t.Fatal(err)
	}
}

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestManagementPageReusesCurrentSourceDirectory(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	want, err := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", true)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	got, err := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", false)
	if err != nil || !reflect.DeepEqual(got.Page.Rows, want.Page.Rows) || got.Page.Revision != want.Page.Revision || !reflect.DeepEqual(managementContextForPage(got.View, got.Page, f.Model), managementContextForPage(want.View, want.Page, f.Model)) {
		t.Fatal("directory hydration changed public results", err)
	}
	if !strings.Contains(output.String(), "management_page_model_directory reuse=true") || strings.Contains(output.String(), "stage=page_models ") {
		t.Fatal("page repeated source model SQL despite the current complete directory")
	}
}

func TestManagementPageDirectoryRequiresExactPreparedTransaction(t *testing.T) {
	for _, name := range []string{"ordered", "unordered", "owner", "transaction", "pool", "nil_tx", "database", "account", "model", "unprepared", "missing_epoch", "missing_directory", "missing_order", "no_scope"} {
		t.Run(name, func(t *testing.T) {
			database, tx := &sql.DB{}, &sql.Tx{}
			version := managementDirectoryVersion{database, "owner", "10", 20}
			models := []managementStreamModel{
				{item: managementModelSignals{Account: "a", Group: -1, Model: subModelResult{Model: "Z", State: "idle"}}, message: "first"},
				{item: managementModelSignals{Account: "a", Group: -1, Model: subModelResult{Model: "模型🙂", State: "done"}}, message: "second"},
				{item: managementModelSignals{Account: "a", Group: 2, Model: subModelResult{Model: "other"}}},
				{item: managementModelSignals{Account: "b", Group: -1, Model: subModelResult{Model: "other-account"}}},
			}
			if name == "unordered" {
				models[0], models[3] = models[3], models[0]
			}
			entry := packManagementModelDirectory(version, models)
			scope := &managementDirectoryScope{store: &managementModelDirectories{}, database: database, tx: tx, owner: "owner", accountRevision: "10", modelRevision: 20, ready: true, directory: entry}
			ctx := context.WithValue(context.Background(), managementDirectoryScopeKey{}, scope)
			var db controlQueryDB = tx
			owner := "owner"
			switch name {
			case "owner":
				owner = "foreign"
			case "transaction":
				db = &sql.Tx{}
			case "pool":
				db = database
			case "nil_tx":
				db = (*sql.Tx)(nil)
			case "database":
				scope.database = &sql.DB{}
			case "account":
				scope.accountRevision = "11"
			case "model":
				scope.modelRevision++
			case "unprepared":
				scope.ready = false
			case "missing_epoch":
				scope.accountRevision = ""
			case "missing_directory":
				scope.directory = nil
			case "missing_order":
				entry.ordered = false
			case "no_scope":
				ctx = context.Background()
			}
			wanted := []subBoundKey{{AccountID: "a", GroupID: -1}, {AccountID: "a", GroupID: -1}, {AccountID: "missing", GroupID: 1}}
			got, ok := managementPageDirectoryModels(ctx, db, owner, wanted)
			if ok != (name == "ordered" || name == "unordered") {
				t.Fatal("directory crossed its proof/owner/transaction", ok)
			}
			if ok && !reflect.DeepEqual(got, []managementPageDirectoryModel{{"a", -1, "Z", "idle", "first"}, {"a", -1, "模型🙂", "done", "second"}}) {
				t.Fatal("directory lost exact fields, byte order, or group boundaries", got)
			}
			query, args, err := managementPageModelQuery(ctx, db, owner, wanted, []byte(mustJSON(wanted)))
			if err != nil || (query == managementPageDirectoryModelsSQL) != ok || len(args) != map[bool]int{true: 3, false: 2}[ok] {
				t.Fatal("unprepared reader did not retain the original query", err)
			}
		})
	}
}

func TestManagementPageDirectorySQLRetainsRawFallbackAndOwnership(t *testing.T) {
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`
 INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('foreign','other','foreign','https://foreign.test','f','');
 INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('foreign',1,'private','openai');
 INSERT INTO console_sub_models(account_id,group_id,model,state,message,result) VALUES
 ('foreign',1,'private','done','private','{"checked_at":999}'),
 ('a',1,'null-result','idle','sql null',NULL),('a',1,'json-null','idle','json null','null'),
 ('a',2,'bad-result','done','raw','{"availability":true}'),
 ('a',3,'unicode-模型🙂','done','unicode','{"availability":{"status":"success","ttft_ms":0}}');
 DELETE FROM console_sub_model_summaries WHERE account_id='a' AND (group_id=1 OR model='unicode-模型🙂')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	read := func(query string, args ...any) []string {
		t.Helper()
		rows, err := tx.QueryContext(ctx, query, args...)
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
	for _, owner := range []string{"owner", "other", "nobody", ""} {
		models, err := readManagementModelSourceDirectory(ctx, tx, owner)
		if err != nil {
			t.Fatal(err)
		}
		metadata := []managementPageDirectoryModel{}
		for _, m := range models {
			metadata = append(metadata, managementPageDirectoryModel{m.item.Account, m.item.Group, m.item.Model.Model, m.item.Model.State, m.message})
		}
		for _, wanted := range [][]subBoundKey{{}, {{AccountID: "a", GroupID: 1}}, {{AccountID: "a", GroupID: 1}, {AccountID: "a", GroupID: 1}, {AccountID: "a", GroupID: 2}, {AccountID: "a", GroupID: 3}, {AccountID: "foreign", GroupID: 1}, {AccountID: "missing", GroupID: 1}}} {
			raw := mustJSON(wanted)
			old := read(managementPageModelsSQL, owner, raw)
			got := read(managementPageDirectoryModelsSQL, owner, raw, mustJSON(metadata))
			if !reflect.DeepEqual(got, old) {
				t.Fatalf("page summary/fallback multiset changed: owner=%q groups=%d old=%d got=%d", owner, len(wanted), len(old), len(got))
			}
		}
	}
}

func TestManagementPageDirectoryBoundsExpandedMetadata(t *testing.T) {
	database, tx := &sql.DB{}, &sql.Tx{}
	version := managementDirectoryVersion{database, "owner", "10", 20}
	models := directoryModelsForTest(1)
	models[0].item.Account, models[0].item.Group = "a", 1
	models[0].message = strings.Repeat("<", 1<<20)
	models = append(models, models[0])
	models[1].item.Model.Model = "second"
	entry := packManagementModelDirectory(version, models)
	if entry == nil || !entry.ordered {
		t.Fatal("fixture directory should fit its own budget")
	}
	scope := &managementDirectoryScope{store: &managementModelDirectories{}, database: database, tx: tx, owner: "owner", accountRevision: "10", modelRevision: 20, ready: true, directory: entry}
	ctx := context.WithValue(context.Background(), managementDirectoryScopeKey{}, scope)
	wanted := []subBoundKey{{AccountID: "a", GroupID: 1}}
	query, args, err := managementPageModelQuery(ctx, tx, "owner", wanted, []byte(mustJSON(wanted)))
	if err != nil || query != managementPageModelsSQL || len(args) != 2 {
		t.Fatal("shared long messages expanded beyond the bounded input", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok := managementPageDirectoryModels(canceled, tx, "owner", wanted); ok {
		t.Fatal("canceled request expanded metadata")
	}
}

func TestManagementPageDirectoryKeepsOldSnapshotAndFreshOwnership(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	wanted := []subBoundKey{{AccountID: "a", GroupID: 1}, {AccountID: "a", GroupID: 2}}
	begin := func() (*sql.Tx, context.Context) {
		t.Helper()
		tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		scope := &managementDirectoryScope{store: &s.modelDirectories, database: s.control.db, tx: tx, owner: "owner"}
		current := context.WithValue(ctx, managementDirectoryScopeKey{}, scope)
		proof, _, err := s.managementReadProof(current, tx, "owner")
		if err != nil {
			t.Fatal(err)
		}
		scope.accountRevision = proof.Epochs["console_sub_accounts"]
		if _, err := readManagementModelDirectory(current, tx, "owner", true, func() ([]managementStreamModel, error) {
			return readManagementModelSourceDirectory(current, tx, "owner")
		}); err != nil {
			t.Fatal(err)
		}
		return tx, current
	}
	read := func(tx *sql.Tx, current context.Context) []string {
		t.Helper()
		query, args, err := managementPageModelQuery(current, tx, "owner", wanted, []byte(mustJSON(wanted)))
		if err != nil || query != managementPageDirectoryModelsSQL {
			t.Fatal("prepared read did not reuse directory", err)
		}
		scan := func(query string, args ...any) []string {
			t.Helper()
			rows, err := tx.QueryContext(current, query, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			out := []string{}
			for rows.Next() {
				var a, m, state, message string
				var g int64
				var result []byte
				if err := rows.Scan(&a, &g, &m, &state, &message, &result); err != nil {
					t.Fatal(err)
				}
				out = append(out, mustJSON([]any{a, g, m, state, message, json.RawMessage(result)}))
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			sort.Strings(out)
			return out
		}
		got := scan(query, args...)
		if want := scan(managementPageModelsSQL, "owner", mustJSON(wanted)); !reflect.DeepEqual(got, want) {
			t.Fatal("directory did not match its exact snapshot")
		}
		return got
	}
	old, oldCtx := begin()
	before := read(old, oldCtx)
	for _, change := range []string{
		`UPDATE console_sub_models SET state='idle',message='new message',result=jsonb_set(result,'{availability,ttft_ms}','987') WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`,
		`DELETE FROM console_sub_models WHERE account_id='a' AND group_id=2 AND model='extra-model'`,
		`UPDATE console_sub_accounts SET owner='foreign' WHERE id='a'`,
	} {
		if _, err := s.control.db.Exec(change); err != nil {
			t.Fatal(err)
		}
		fresh, freshCtx := begin()
		if current := read(fresh, freshCtx); reflect.DeepEqual(current, before) {
			t.Fatal("fresh read retained stale source or ownership")
		}
		if !reflect.DeepEqual(read(old, oldCtx), before) {
			t.Fatal("old RR read mixed later source or summary data")
		}
	}
}

func TestManagementPagingReusesOnlyCurrentResidentModelDirectory(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	first, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil {
		t.Fatal(err)
	}
	s.rememberManagementPagingPlan("owner", f, first)
	want, err := s.readManagementQuerySnapshot(ctx, "owner", f, first.Page.NextCursor, true)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for _, resident := range []bool{true, false} {
		if !resident {
			s.modelDirectories.mu.Lock()
			s.modelDirectories.entries, s.modelDirectories.bytes = nil, 0
			s.modelDirectories.mu.Unlock()
		}
		output.Reset()
		got, ok, err := s.currentManagementPagingPage(ctx, "owner", f, first.Page.NextCursor, false)
		if err != nil || !ok {
			t.Fatal("paging did not accept its current proof", resident, ok, err, output.String())
		}
		if !reflect.DeepEqual(got.Rows, want.Page.Rows) {
			t.Fatalf("paging changed rows resident=%t got=%s want=%s", resident, mustJSON(got.Rows), mustJSON(want.Page.Rows))
		}
		if got.Revision != want.Page.Revision {
			t.Fatal("paging changed revision", resident, got.Revision, want.Page.Revision)
		}
		// Paging restores prices from JSON. Compare their complete public
		// representation, not time.Time's private location pointer (pgx may use
		// time.Local for UTC while JSON restores time.UTC for the same instant).
		if mustJSON(got.Context) != mustJSON(managementContextForPage(want.View, want.Page, f.Model)) {
			expected := managementContextForPage(want.View, want.Page, f.Model)
			t.Fatalf("paging changed context resident=%t got=%s want=%s", resident, mustJSON(got.Context), mustJSON(expected))
		}
		if reused := strings.Contains(output.String(), "management_page_model_directory reuse=true"); reused != resident {
			t.Fatal("paging reread a verified source directory or used an absent one", resident, output.String())
		}
		if strings.Contains(output.String(), "stage=page_models ") == resident {
			t.Fatal("paging source SQL did not follow proof/residency", resident)
		}
	}
}

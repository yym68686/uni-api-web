package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestManagementPageModelCacheReusesOverlappingGroups(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	all := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	if _, err := s.readManagementQuerySnapshot(ctx, "owner", all, "", false); err != nil {
		t.Fatal(err)
	}
	f := all
	f.AccountID = "a"
	want, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", true)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	got, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	gotContext, _ := json.Marshal(managementContextForPage(got.View, got.Page, f.Model))
	wantContext, _ := json.Marshal(managementContextForPage(want.View, want.Page, f.Model))
	if err != nil || !reflect.DeepEqual(got.Page.Rows, want.Page.Rows) || got.Page.Revision != want.Page.Revision || !bytes.Equal(gotContext, wantContext) {
		t.Fatal("overlapping group reuse changed the complete public page", err)
	}
	if strings.Contains(output.String(), "stage=page_model_summaries ") || strings.Contains(output.String(), "stage=page_models ") {
		t.Fatal("overlapping current groups repeated page evidence SQL")
	}
}

func TestManagementPageModelCacheScopeAndIndependentRows(t *testing.T) {
	for _, name := range []string{"hit", "owner", "database", "transaction", "pool", "nil_tx", "no_scope", "no_directory", "unready", "unordered", "account_epoch", "model_epoch", "summary_epoch", "missing_account", "missing_summary", "duplicates", "large", "cancel", "decode_error"} {
		t.Run(name, func(t *testing.T) {
			database, tx := &sql.DB{}, &sql.Tx{}
			v := managementDirectoryVersion{database, "owner", "1", 2}
			entry := packManagementModelDirectory(v, []managementStreamModel{{item: managementModelSignals{Account: "a", Group: 1, Model: subModelResult{Model: "模型🙂"}}}})
			directory := &managementDirectoryScope{store: &managementModelDirectories{}, database: database, tx: tx, owner: "owner", accountRevision: "1", modelRevision: 2, ready: true, directory: entry}
			scope := &managementPageModelScope{store: &managementPageModelStreams{}, tx: tx, summaries: "3"}
			ctx := context.WithValue(context.Background(), managementDirectoryScopeKey{}, directory)
			ctx = context.WithValue(ctx, managementPageModelScopeKey{}, scope)
			version := managementPageModelVersion{v, "3"}
			row := managementPageModelRecord{account: "a", group: 1, model: "模型🙂", state: "done", message: "source", result: []byte(`{"checked_at":12}`)}
			group := managementPageModelGroup{"a", 1}
			scope.store.put([]*managementPageModelStream{{version: version, group: group, rows: []managementPageModelRecord{row}, bytes: 512}})
			var db controlQueryDB = tx
			owner := "owner"
			wanted := []subBoundKey{{AccountID: "a", GroupID: 1, AccountName: "ignored SQL metadata"}}
			switch name {
			case "owner":
				owner = "foreign"
			case "database":
				directory.database = &sql.DB{}
			case "transaction":
				db = &sql.Tx{}
			case "pool":
				db = database
			case "nil_tx":
				db = (*sql.Tx)(nil)
			case "no_scope":
				ctx = context.WithValue(ctx, managementPageModelScopeKey{}, (*managementPageModelScope)(nil))
			case "no_directory":
				directory.directory = nil
			case "unready":
				directory.ready = false
			case "unordered":
				entry.ordered = false
			case "account_epoch":
				directory.accountRevision = "changed"
			case "model_epoch":
				directory.modelRevision++
			case "summary_epoch":
				scope.summaries = "changed"
			case "missing_account":
				directory.accountRevision = ""
			case "missing_summary":
				scope.summaries = ""
			case "duplicates":
				wanted = append(wanted, subBoundKey{AccountID: "a", GroupID: 1})
			case "large":
				wanted = make([]subBoundKey, managementPageModelGroups+1)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got, actual := managementPageModelScopeFor(ctx, db, owner, wanted)
			eligible := name == "hit" || name == "summary_epoch" || name == "cancel" || name == "decode_error"
			if (got != nil) != eligible {
				t.Fatal("scope crossed its prepared transaction/source proof")
			}
			if !eligible {
				return
			}
			if name == "summary_epoch" {
				if len(scope.store.current(actual)) != 0 {
					t.Fatal("summary mutation reused previous rows")
				}
				return
			}
			calls := 0
			sentinel := errors.New("decoder rejected summary")
			err := readManagementPageModelStream(ctx, db, owner, wanted, func(got managementPageModelRecord, _ *managementRowsTrace) error {
				calls++
				if !reflect.DeepEqual(got, row) {
					t.Fatal("raw row changed on reuse")
				}
				got.result[0] = 'x'
				if name == "decode_error" {
					return sentinel
				}
				return nil
			})
			if name == "cancel" {
				if !errors.Is(err, context.Canceled) || calls != 0 {
					t.Fatal("canceled reuse consumed rows", err)
				}
			} else if name == "decode_error" {
				if !errors.Is(err, sentinel) || calls != 1 {
					t.Fatal("reuse hid decode failure", err)
				}
			} else if err != nil || calls != 1 {
				t.Fatal("fully cached group queried SQL", err)
			}
			if row.result[0] != '{' {
				t.Fatal("consumer mutated retained raw evidence")
			}
		})
	}
}

func TestManagementPageModelCacheBudgetAndEviction(t *testing.T) {
	var store managementPageModelStreams
	v := managementPageModelVersion{managementDirectoryVersion{&sql.DB{}, "owner", "1", 2}, "3"}
	for i := 0; i < managementPageModelGroups+3; i++ {
		store.put([]*managementPageModelStream{{version: v, group: managementPageModelGroup{"a", int64(i)}, bytes: 1000}})
	}
	if len(store.entries) != managementPageModelGroups || store.bytes != managementPageModelGroups*1000 {
		t.Fatal("group cap not retained")
	}
	store.put([]*managementPageModelStream{{version: v, group: managementPageModelGroup{"a", 1000}, bytes: managementPageModelBudget}})
	if len(store.entries) != 1 || store.bytes != managementPageModelBudget {
		t.Fatal("shared byte cap exceeded")
	}
	previous := store.entries[0]
	store.put([]*managementPageModelStream{{version: v, group: managementPageModelGroup{"a", 1001}, bytes: managementPageModelBudget + 1}})
	if len(store.entries) != 1 || store.entries[0] != previous {
		t.Fatal("oversized group evicted valid cache")
	}
	v.summaries = "4"
	store.put([]*managementPageModelStream{{version: v, group: managementPageModelGroup{"a", 1}, bytes: 256}})
	if len(store.entries) != 1 || store.bytes != 256 {
		t.Fatal("old-generation evidence retained")
	}
}

func TestManagementPageModelCacheMatchesCurrentSQLAcrossMutations(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`UPDATE console_sub_model_summaries SET result=jsonb_set(result,'{packing_fixture}',to_jsonb(repeat('x',5000))) WHERE account_id='a' AND group_id=1 AND result IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	wanted := []subBoundKey{{AccountID: "a", GroupID: 1}, {AccountID: "a", GroupID: 2}, {AccountID: "q", GroupID: 1}, {AccountID: "missing", GroupID: 1}}
	begin := func() (*sql.Tx, context.Context) {
		t.Helper()
		tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		directory := &managementDirectoryScope{store: &s.modelDirectories, database: s.control.db, tx: tx, owner: "owner"}
		call := context.WithValue(ctx, managementDirectoryScopeKey{}, directory)
		proof, _, err := s.managementReadProof(call, tx, "owner")
		if err != nil {
			t.Fatal(err)
		}
		directory.accountRevision = proof.Epochs["console_sub_accounts"]
		if _, err := readManagementModelDirectory(call, tx, "owner", true, func() ([]managementStreamModel, error) { return readManagementModelSourceDirectory(call, tx, "owner") }); err != nil {
			t.Fatal(err)
		}
		return tx, context.WithValue(call, managementPageModelScopeKey{}, &managementPageModelScope{store: &s.pageModelStreams, tx: tx, summaries: proof.Epochs["console_sub_model_summaries"]})
	}
	read := func(tx *sql.Tx, call context.Context) []managementPageModelRecord {
		t.Helper()
		var got, want []managementPageModelRecord
		if err := readManagementPageModelStream(call, tx, "owner", wanted, func(row managementPageModelRecord, _ *managementRowsTrace) error { got = append(got, row); return nil }); err != nil {
			t.Fatal(err)
		}
		if err := readManagementPageModelSQL(context.Background(), tx, "owner", wanted, func(row managementPageModelRecord, _ *managementRowsTrace) error {
			want = append(want, row)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		less := func(rows []managementPageModelRecord) func(int, int) bool {
			return func(i, j int) bool {
				a, b := rows[i], rows[j]
				return fmt.Sprint(a.account, a.group, a.model) < fmt.Sprint(b.account, b.group, b.model)
			}
		}
		sort.Slice(got, less(got))
		sort.Slice(want, less(want))
		if !reflect.DeepEqual(got, want) {
			t.Fatal("cached stream diverged from current SQL")
		}
		return got
	}
	old, call := begin()
	before := read(old, call)
	for _, query := range []string{
		`UPDATE console_sub_model_summaries SET result='{"checked_at":987,"availability":{"id":"a-1-gpt-5.5"}}' WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`,
		`DELETE FROM console_sub_model_summaries WHERE account_id='a' AND group_id=1`,
		`UPDATE console_sub_models SET state='running',message='changed' WHERE account_id='a' AND group_id=1`,
		`UPDATE console_sub_models SET result='{"availability":{"ttft_ms":123}}' WHERE account_id='a' AND group_id=1`,
		`INSERT INTO console_sub_models(account_id,group_id,model,state,message,result) VALUES('a',1,'new-模型🙂','done','new','null')`,
		`DELETE FROM console_sub_models WHERE account_id='a' AND group_id=2`,
		`UPDATE console_sub_accounts SET owner='foreign' WHERE id='a'`,
		`UPDATE console_sub_accounts SET owner='owner' WHERE id='a'`,
	} {
		if _, err := s.control.db.Exec(query); err != nil {
			t.Fatal(err)
		}
		tx, fresh := begin()
		read(tx, fresh)
		tx.Rollback()
		if !reflect.DeepEqual(read(old, call), before) {
			t.Fatal("fresh generation contaminated old RR snapshot")
		}
	}
}

func TestManagementPageModelCacheNeverStoresFailedBatch(t *testing.T) {
	for _, cancelRead := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelRead), func(t *testing.T) {
			s := managementHydrationFixture(t)
			prepareManagementRevisionsForTest(t, s.control.db)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			directory := &managementDirectoryScope{store: &s.modelDirectories, database: s.control.db, tx: tx, owner: "owner"}
			ctx = context.WithValue(ctx, managementDirectoryScopeKey{}, directory)
			proof, _, err := s.managementReadProof(ctx, tx, "owner")
			if err != nil {
				t.Fatal(err)
			}
			directory.accountRevision = proof.Epochs["console_sub_accounts"]
			if _, err = readManagementModelDirectory(ctx, tx, "owner", true, func() ([]managementStreamModel, error) { return readManagementModelSourceDirectory(ctx, tx, "owner") }); err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(ctx, managementPageModelScopeKey{}, &managementPageModelScope{store: &s.pageModelStreams, tx: tx, summaries: proof.Epochs["console_sub_model_summaries"]})
			sentinel := errors.New("read failure")
			err = readManagementPageModelStream(ctx, tx, "owner", []subBoundKey{{AccountID: "a", GroupID: 1}, {AccountID: "a", GroupID: 2}}, func(row managementPageModelRecord, _ *managementRowsTrace) error {
				if cancelRead {
					cancel()
					return nil
				}
				return sentinel
			})
			if err == nil || len(s.pageModelStreams.entries) != 0 {
				t.Fatal("partial/failed batch was published", err)
			}
			if !cancelRead && !errors.Is(err, sentinel) {
				t.Fatal("consumer failure changed", err)
			}
		})
	}
}

func TestManagementPageModelCacheReadsOnlyNewGroups(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := managementTraceContext(context.Background())
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	directory := &managementDirectoryScope{store: &s.modelDirectories, database: s.control.db, tx: tx, owner: "owner"}
	ctx = context.WithValue(ctx, managementDirectoryScopeKey{}, directory)
	proof, _, err := s.managementReadProof(ctx, tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	directory.accountRevision = proof.Epochs["console_sub_accounts"]
	if _, err = readManagementModelDirectory(ctx, tx, "owner", true, func() ([]managementStreamModel, error) { return readManagementModelSourceDirectory(ctx, tx, "owner") }); err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, managementPageModelScopeKey{}, &managementPageModelScope{store: &s.pageModelStreams, tx: tx, summaries: proof.Epochs["console_sub_model_summaries"]})
	if err := readManagementPageModelStream(ctx, tx, "owner", []subBoundKey{{AccountID: "a", GroupID: 1}, {AccountID: "a", GroupID: 2}}, func(managementPageModelRecord, *managementRowsTrace) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	var rows []managementPageModelRecord
	if err := readManagementPageModelStream(ctx, tx, "owner", []subBoundKey{{AccountID: "a", GroupID: 2}, {AccountID: "a", GroupID: 3}}, func(row managementPageModelRecord, trace *managementRowsTrace) error {
		rows = append(rows, row)
		trace.counted(len(row.result))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 || !strings.Contains(output.String(), "reused_groups=1 raw_groups=1 stored_groups=1") {
		t.Fatal("overlapping group was lost or reread", len(rows), output.String())
	}
	if strings.Count(output.String(), "stage=page_model_summaries ") != 1 {
		t.Fatal("SQL read was not limited to the missing group", output.String())
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.Contains(line, "stage=page_model_summaries ") && !strings.Contains(line, "rows=3 ") {
			t.Fatal("SQL did not read exactly the new group's three models", line)
		}
	}
}

func BenchmarkManagementPageModelCache5000Rows(b *testing.B) {
	database, tx := &sql.DB{}, &sql.Tx{}
	v := managementDirectoryVersion{database, "owner", "1", 2}
	directory := &managementDirectoryScope{store: &managementModelDirectories{}, database: database, tx: tx, owner: "owner", accountRevision: "1", modelRevision: 2, ready: true, directory: &managementModelDirectory{version: v, ordered: true}}
	scope := &managementPageModelScope{store: &managementPageModelStreams{}, tx: tx, summaries: "3"}
	ctx := context.WithValue(context.Background(), managementDirectoryScopeKey{}, directory)
	ctx = context.WithValue(ctx, managementPageModelScopeKey{}, scope)
	wanted := make([]subBoundKey, 25)
	for g := range wanted {
		wanted[g] = subBoundKey{AccountID: "a", GroupID: int64(g)}
		e := &managementPageModelStream{version: managementPageModelVersion{v, "3"}, group: managementPageModelGroup{"a", int64(g)}, bytes: 256}
		for m := 0; m < 200; m++ {
			raw := []byte(`{"checked_at":12,"verdict":"pass","availability":{"id":"probe","status":"success","message":"` + strings.Repeat("x", 160) + `","ttft_ms":123},"quality":{"message":"` + strings.Repeat("y", 160) + `"}}`)
			row := managementPageModelRecord{account: "a", group: int64(g), model: fmt.Sprint("model-", m), state: "done", result: raw}
			e.rows = append(e.rows, row)
			e.bytes += row.retainedBytes()
		}
		scope.store.put([]*managementPageModelStream{e})
	}
	consume := func(row managementPageModelRecord, _ *managementRowsTrace) error {
		var result *subResult
		return json.Unmarshal(row.result, &result)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := readManagementPageModelStream(ctx, tx, "owner", wanted, consume); err != nil {
			b.Fatal(err)
		}
	}
}

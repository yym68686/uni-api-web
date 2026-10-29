package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func directoryModelsForTest(n int) []managementStreamModel {
	out := make([]managementStreamModel, n)
	for i := range out {
		out[i] = managementStreamModel{item: managementModelSignals{Account: fmt.Sprintf("private-account-%d", i%49), Group: int64(i / 288), Model: subModelResult{Model: fmt.Sprintf("model-%d雪🙂", i%288), State: "done"}}, message: "directory message"}
	}
	return out
}

func TestManagementModelDirectoryPackingIsLosslessBoundedAndPrivate(t *testing.T) {
	if unsafe.Sizeof(managementDirectoryRow{}) != managementDirectoryRowBytes {
		t.Fatal("retained row admission no longer matches the actual layout")
	}
	models := directoryModelsForTest(1000)
	models[0].item.Group, models[0].message = -9223372036854775808, "\xff\x00雪"
	models[1].item.Model.State, models[1].item.Model.Model = "", ""
	version := managementDirectoryVersion{&sql.DB{}, "private-owner", "10", 11}
	entry := packManagementModelDirectory(version, models)
	if entry == nil || !reflect.DeepEqual(entry.expand(), models) || entry.bytes > managementDirectoryBudget || cap(entry.strings) != len(entry.strings) {
		t.Fatal("packing lost bytes, order, empty fields, or exact backing-array admission")
	}
	want := entry.expand()
	models[0].item.Account = "changed"
	got := entry.expand()
	got[0].item.Model.Result = &subResult{}
	got[1].item.Model.Model = "changed"
	if !reflect.DeepEqual(entry.expand(), want) {
		t.Fatal("retained directory aliases a caller's envelopes")
	}
	encoded, _ := json.Marshal(entry)
	if string(encoded) != "{}" {
		t.Fatal("private directory entered JSON")
	}
	for _, mutate := range []func(*managementStreamModel){
		func(m *managementStreamModel) { m.item.Model.Result = &subResult{} },
		func(m *managementStreamModel) { m.item.Model.Message = "expanded" },
		func(m *managementStreamModel) { m.evidence = []byte{} },
		func(m *managementStreamModel) { m.probe = "private-probe" },
		func(m *managementStreamModel) { m.quality = "private-quality" },
	} {
		model := want[0]
		mutate(&model)
		if packManagementModelDirectory(version, []managementStreamModel{model}) != nil {
			t.Fatal("expanded evidence entered directory storage")
		}
	}
	empty := packManagementModelDirectory(version, []managementStreamModel{})
	if empty == nil || empty.expand() == nil || len(empty.expand()) != 0 {
		t.Fatal("empty directory lost its non-nil source shape")
	}
	if packManagementModelDirectory(version, nil) != nil {
		t.Fatal("nil source shape was converted to a successful empty directory")
	}
	model := directoryModelsForTest(1)[0]
	model.message = ""
	base := packManagementModelDirectory(version, []managementStreamModel{model})
	model.message = strings.Repeat("x", managementDirectoryBudget-base.bytes)
	exact := packManagementModelDirectory(version, []managementStreamModel{model})
	if exact == nil || exact.bytes != managementDirectoryBudget {
		t.Fatal("exact retained budget was rejected")
	}
	model.message += "x"
	if packManagementModelDirectory(version, []managementStreamModel{model}) != nil {
		t.Fatal("over-budget directory was admitted")
	}
}

func TestManagementModelDirectoryRequiresExactPreparedScope(t *testing.T) {
	for _, test := range []string{"hit", "owner", "transaction", "pool", "nil_tx", "legacy", "unprepared", "missing_epoch", "account_version", "model_version", "database", "no_scope"} {
		t.Run(test, func(t *testing.T) {
			store, tx, database := &managementModelDirectories{}, &sql.Tx{}, &sql.DB{}
			scope := &managementDirectoryScope{store: store, tx: tx, database: database, owner: "private-owner", accountRevision: "10", modelRevision: 20, ready: true}
			ctx := context.WithValue(context.Background(), managementDirectoryScopeKey{}, scope)
			models := directoryModelsForTest(2)
			calls := 0
			read := func() ([]managementStreamModel, error) {
				calls++
				return append([]managementStreamModel{}, models...), nil
			}
			if _, err := readManagementModelDirectory(ctx, tx, "private-owner", true, read); err != nil || calls != 1 {
				t.Fatal("initial directory was not read")
			}
			var db controlQueryDB = tx
			owner, compact := "private-owner", true
			switch test {
			case "owner":
				owner = "other-private-owner"
			case "transaction":
				db = &sql.Tx{}
			case "pool":
				db = database
			case "nil_tx":
				db = (*sql.Tx)(nil)
			case "legacy":
				compact = false
			case "unprepared":
				scope.ready = false
			case "missing_epoch":
				scope.accountRevision = ""
			case "account_version":
				scope.accountRevision = "11"
			case "model_version":
				scope.modelRevision++
			case "database":
				scope.database = &sql.DB{}
			case "no_scope":
				ctx = context.Background()
			}
			got, err := readManagementModelDirectory(ctx, db, owner, compact, read)
			wantCalls := 2
			if test == "hit" {
				wantCalls = 1
			}
			if err != nil || calls != wantCalls || !reflect.DeepEqual(got, models) {
				t.Fatal("directory crossed a scope or changed source values", calls, err)
			}
		})
	}
}

func TestManagementModelDirectoryRetainsErrorsCancellationAndConcurrentIsolation(t *testing.T) {
	store, tx := &managementModelDirectories{}, &sql.Tx{}
	scope := &managementDirectoryScope{store: store, database: &sql.DB{}, tx: tx, owner: "owner", accountRevision: "0", ready: true}
	ctx := context.WithValue(context.Background(), managementDirectoryScopeKey{}, scope)
	cause := errors.New("private-source-error")
	if _, err := readManagementModelDirectory(ctx, tx, "owner", true, func() ([]managementStreamModel, error) { return nil, cause }); err != cause || len(store.entries) != 0 {
		t.Fatal("failed source read was retained or its error changed")
	}
	models := directoryModelsForTest(200)
	if _, err := readManagementModelDirectory(ctx, tx, "owner", true, func() ([]managementStreamModel, error) { return models, nil }); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := readManagementModelDirectory(canceled, tx, "owner", true, func() ([]managementStreamModel, error) { t.Fatal("canceled hit read the source"); return nil, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled cache hit succeeded")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := *scope
			local.tx = &sql.Tx{}
			localCtx := context.WithValue(context.Background(), managementDirectoryScopeKey{}, &local)
			for i := 0; i < 20; i++ {
				got, err := readManagementModelDirectory(localCtx, local.tx, "owner", true, func() ([]managementStreamModel, error) { t.Error("same version was read again"); return nil, cause })
				if err != nil || !reflect.DeepEqual(got, models) {
					t.Error("concurrent directory changed", err)
					return
				}
				got[0].item.Account = "caller-edited"
			}
		}()
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		entry := packManagementModelDirectory(managementDirectoryVersion{scope.database, fmt.Sprint(i), "0", 0}, models)
		store.put(entry)
	}
	if len(store.entries) > 2 || store.bytes > managementDirectoryBudget {
		t.Fatal("directory store exceeded shared entry/byte bounds")
	}
}

func TestManagementModelDirectorySnapshotKeepsFreshPredicatesAndWholePage(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "directory-page-123")
	if _, err := s.readManagementQuerySnapshot(ctx, "owner", managementQueryFilters{GroupStatus: "all"}, "", true); err != nil || len(s.modelDirectories.entries) != 0 {
		t.Fatal("complete action selection populated the display-only directory", err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for _, model := range []string{"gpt-5.5", "", "extra-model", "missing-extra"} {
		filters := managementQueryFilters{GroupStatus: "all", Model: model}
		got, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
		if err != nil {
			t.Fatal(err)
		}
		want, err := s.readManagementQuerySnapshot(context.Background(), "owner", filters, "", true)
		if err != nil || got.Page.Revision != want.Page.Revision || !reflect.DeepEqual(got.Page.Rows, want.Page.Rows) || !reflect.DeepEqual(managementContextForPage(got.View, got.Page, model), managementContextForPage(want.View, want.Page, model)) {
			t.Fatal("reused directory changed complete hydrated rows, context, revision or fallback", model, err)
		}
	}
	if !strings.Contains(output.String(), "management_model_directory_cache hit=true rows=183 ") || len(s.modelDirectories.entries) != 1 {
		t.Fatal("normal management snapshot did not reuse its source-only directory")
	}
	filters := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	before, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
	if err != nil {
		t.Fatal(err)
	}
	entry := s.modelDirectories.entries[0]
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,ttft_ms}','987') WHERE group_id=1 AND model='gpt-5.5'; UPDATE console_sub_usage SET result='{"input_price":9,"output_price":3}' WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	after, err := s.readManagementQuerySnapshot(ctx, "owner", filters, "", false)
	if err != nil {
		t.Fatal(err)
	}
	probe := after.Page.Rows[0].Selected.Result.Availability
	if s.modelDirectories.entries[0] != entry || !strings.Contains(output.String(), "management_model_directory_cache hit=true ") || before.Page.Revision == after.Page.Revision || *probe.TTFT != 987 || *probe.Usage.InputPrice != 9 {
		t.Fatal("directory reuse retained stale predicate/display/receipt data")
	}
	for _, private := range []string{"private-account", "gpt-5.5", "owner"} {
		if strings.Contains(output.String(), private) {
			t.Fatal("directory log exposed private values")
		}
	}
}

func TestManagementModelDirectoryConcurrentReplacementClearsEvictedReferences(t *testing.T) {
	store, database := &managementModelDirectories{}, &sql.DB{}
	models := directoryModelsForTest(5)
	for i := 0; i < 2; i++ {
		store.put(packManagementModelDirectory(managementDirectoryVersion{database, fmt.Sprint(i), "0", 0}, models))
	}
	large := packManagementModelDirectory(managementDirectoryVersion{database, "0", "0", 1}, models)
	large.bytes = managementDirectoryBudget // Exercise byte eviction independently of row size.
	store.put(large)
	if len(store.entries) != 1 || store.entries[0] != large || store.bytes != managementDirectoryBudget {
		t.Fatal("replacement did not evict the other owner's entry")
	}
	for _, retained := range store.entries[len(store.entries):cap(store.entries)] {
		if retained != nil {
			t.Fatal("evicted directory remains retained in slice capacity")
		}
	}
	var wg sync.WaitGroup
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				version := managementDirectoryVersion{database, fmt.Sprint(n % 3), "0", int64(i)}
				store.put(packManagementModelDirectory(version, models))
				if entry := store.get(version); entry != nil && !reflect.DeepEqual(entry.expand(), models) {
					t.Error("concurrent replacement changed directory")
				}
				store.mu.Lock()
				if len(store.entries) > 2 || store.bytes > managementDirectoryBudget {
					t.Error("concurrent replacement exceeded retained bounds")
				}
				store.mu.Unlock()
			}
		}(n)
	}
	wg.Wait()
}

func TestManagementModelDirectoryInvalidatesEverySourceColumnAndOwnership(t *testing.T) {
	for i, change := range []string{
		`UPDATE console_sub_models SET message='changed' WHERE group_id=1`,
		`UPDATE console_sub_models SET state='running' WHERE group_id=1`,
		`UPDATE console_sub_models SET model='renamed' WHERE group_id=1 AND model='gpt-5.5'`,
		`UPDATE console_sub_models SET group_id=2 WHERE group_id=1 AND model='gpt-5.5';`,
		`UPDATE console_sub_models SET account_id='b' WHERE group_id=1 AND model='gpt-5.5'`,
		`DELETE FROM console_sub_models WHERE group_id=1`,
		`INSERT INTO console_sub_models(account_id,group_id,model,state) VALUES('a',1,'new','idle')`,
		`UPDATE console_sub_accounts SET owner='another-owner' WHERE id='a'`,
		`TRUNCATE console_sub_models CASCADE`,
	} {
		t.Run(fmt.Sprintf("change_%d", i), func(t *testing.T) {
			s := managementHydrationFixture(t)
			prepareManagementRevisionsForTest(t, s.control.db)
			if strings.Contains(change, "account_id='b'") {
				if _, err := s.control.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('b','owner','second','https://b.test','fixture',''); INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('b',1,'G1','openai')`); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Contains(change, "group_id=2 WHERE") {
				if _, err := s.control.db.Exec(`DELETE FROM console_sub_models WHERE group_id=2 AND model='gpt-5.5'`); err != nil {
					t.Fatal(err)
				}
			}
			read := func() []managementStreamModel {
				t.Helper()
				tx, err := s.control.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				scope := &managementDirectoryScope{store: &s.modelDirectories, database: s.control.db, tx: tx, owner: "owner"}
				ctx := context.WithValue(context.Background(), managementDirectoryScopeKey{}, scope)
				proof, _, err := s.managementReadProof(ctx, tx, "owner")
				if err != nil {
					t.Fatal(err)
				}
				scope.accountRevision = proof.Epochs["console_sub_accounts"]
				want, err := readManagementModelSourceDirectory(ctx, tx, "owner")
				if err != nil {
					t.Fatal(err)
				}
				got, err := readManagementModelDirectory(ctx, tx, "owner", true, func() ([]managementStreamModel, error) { return readManagementModelSourceDirectory(ctx, tx, "owner") })
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("directory differs from current authorized source", err)
				}
				return got
			}
			read()
			previous := s.modelDirectories.entries[0]
			if _, err := s.control.db.Exec(change); err != nil {
				t.Fatal(err)
			}
			read()
			if len(s.modelDirectories.entries) != 1 || s.modelDirectories.entries[0] == previous {
				t.Fatal("changed source/owner retained the previous directory")
			}
		})
	}
}

func TestManagementModelDirectoryRemainsInsideCurrentSnapshotAndPreparation(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	begin := func() *sql.Tx {
		t.Helper()
		tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		return tx
	}
	calls := 0
	read := func(tx *sql.Tx) ([]managementStreamModel, *managementDirectoryScope) {
		t.Helper()
		scope := &managementDirectoryScope{store: &s.modelDirectories, database: s.control.db, tx: tx, owner: "owner"}
		trace := context.WithValue(ctx, managementDirectoryScopeKey{}, scope)
		proof, _, err := s.managementReadProof(trace, tx, "owner")
		if err != nil {
			t.Fatal(err)
		}
		scope.accountRevision = proof.Epochs["console_sub_accounts"]
		got, err := readManagementModelDirectory(trace, tx, "owner", true, func() ([]managementStreamModel, error) {
			calls++
			return readManagementModelSourceDirectory(trace, tx, "owner")
		})
		if err != nil {
			t.Fatal(err)
		}
		return got, scope
	}
	old := begin()
	before, _ := read(old)
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET message='new snapshot' WHERE group_id=1`); err != nil {
		t.Fatal(err)
	}
	stillOld, _ := read(old)
	if calls != 1 || !reflect.DeepEqual(stillOld, before) {
		t.Fatal("a committed edit escaped the pinned snapshot")
	}
	current := begin()
	fresh, _ := read(current)
	if calls != 2 || reflect.DeepEqual(fresh, before) {
		t.Fatal("a later transaction reused the old source")
	}
	// Replacing a last-directory entry with an older snapshot is safe: the
	// version key forces the later snapshot to miss again, never use old rows.
	read(old)
	read(current)
	if calls != 4 {
		t.Fatal("interleaved snapshots bypassed their own version")
	}
	old.Rollback()
	current.Rollback()
	if _, err := s.control.db.Exec(`UPDATE console_management_revision_state SET complete=false WHERE relation_name='console_sub_models'`); err != nil {
		t.Fatal(err)
	}
	_, unprepared := read(begin())
	if unprepared.ready || calls != 5 {
		t.Fatal("unprepared counters reused a directory")
	}
}

// Opt-in, isolated, source-query-only measurement; never an E2E claim. The
// preparation/fencing behavior is covered by the normal real-schema tests.
func TestManagementModelDirectorySourceScale(t *testing.T) {
	if os.Getenv("RUN_MANAGEMENT_DIRECTORY_SCALE") != "1" {
		t.Skip("opt-in isolated source directory scale fixture")
	}
	db := migrationTestDB(t)
	if _, err := db.Exec(`CREATE TABLE console_sub_accounts(id text PRIMARY KEY,owner text NOT NULL);
CREATE TABLE console_sub_models(account_id text NOT NULL,group_id bigint NOT NULL,model text NOT NULL,state text NOT NULL,message text NOT NULL);
INSERT INTO console_sub_accounts SELECT 'a-'||n,'fixture-owner' FROM generate_series(0,48)n;
INSERT INTO console_sub_models SELECT 'a-'||(g%49),g,'model-'||m,'done','directory diagnostic' FROM generate_series(1,749)g CROSS JOIN generate_series(1,255)m;`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scope := &managementDirectoryScope{store: &managementModelDirectories{}, database: db, tx: tx, owner: "fixture-owner", accountRevision: "0", ready: true}
	ctx = context.WithValue(ctx, managementDirectoryScopeKey{}, scope)
	read := func() ([]managementStreamModel, error) {
		return readManagementModelSourceDirectory(ctx, tx, "fixture-owner")
	}
	started := time.Now()
	first, err := readManagementModelDirectory(ctx, tx, "fixture-owner", true, read)
	if err != nil || len(first) != 190995 || len(scope.store.entries) != 1 {
		t.Fatal("scale directory did not fit safely", len(first), err)
	}
	t.Logf("local cold source + dictionary admission: %s, rows=%d retained_bytes=%d", time.Since(started), len(first), scope.store.bytes)
	for i := 0; i < 3; i++ {
		started = time.Now()
		want, err := read()
		if err != nil {
			t.Fatal(err)
		}
		sourceTime := time.Since(started)
		started = time.Now()
		got, err := readManagementModelDirectory(ctx, tx, "fixture-owner", true, func() ([]managementStreamModel, error) { t.Fatal("hit repeated source SQL"); return nil, nil })
		hitTime := time.Since(started)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("scale values/order changed", err)
		}
		t.Logf("local source-only round=%d raw_sql=%s current_directory_hit=%s", i+1, sourceTime, hitTime)
	}
}

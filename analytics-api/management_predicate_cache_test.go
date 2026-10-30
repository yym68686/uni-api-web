package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func predicateCacheFixture() (*managementDirectoryScope, context.Context, []managementPredicateValue, managementStreamPositionIndex) {
	revision := int64(7)
	directory := &managementDirectoryScope{store: &managementModelDirectories{}, predicates: &managementPredicateSets{}, database: &sql.DB{}, tx: &sql.Tx{}, owner: "owner", accountRevision: "5", modelRevision: 6, signalRevision: &revision, ready: true}
	ctx := context.WithValue(context.Background(), managementDirectoryScopeKey{}, directory)
	price, tokens := math.SmallestNonzeroFloat64, int64(math.MaxInt64)
	values := []managementPredicateValue{{Account: "a", Group: 1, Model: "模型", Valid: true, Present: true, HasUsage: true, Evidence: bytes.Repeat([]byte{1}, sha256.Size), CheckedAt: math.MaxInt64, Probe: "p", Usage: managementStreamUsage{InputPrice: &price, OutputTokens: &tokens}}}
	positions := managementStreamPositionIndex{values: map[managementStreamIdentity]int{{"a", 1, "模型"}: 0}}
	return directory, ctx, values, positions
}

func TestManagementPredicateCacheFencesAndIsolation(t *testing.T) {
	for _, name := range []string{"hit", "pool", "transaction", "database", "owner", "account", "model", "signal", "selection", "unready", "no_signal", "negative_signal", "no_account", "no_scope", "not_seek", "not_strict"} {
		t.Run(name, func(t *testing.T) {
			directory, ctx, values, positions := predicateCacheFixture()
			store, version := managementPredicateCacheVersion(ctx, directory.tx, "owner", "keys", true, true)
			store.put(ctx, version, values, positions)
			var db controlQueryDB = directory.tx
			owner, keys, seek, strict := "owner", "keys", true, true
			switch name {
			case "pool":
				db = directory.database
			case "transaction":
				db = &sql.Tx{}
			case "database":
				directory.database = &sql.DB{}
			case "owner":
				owner = "foreign"
			case "account":
				directory.accountRevision = "next"
			case "model":
				directory.modelRevision++
			case "signal":
				*directory.signalRevision++
			case "selection":
				keys = "other keys"
			case "unready":
				directory.ready = false
			case "no_signal":
				directory.signalRevision = nil
			case "negative_signal":
				*directory.signalRevision = -1
			case "no_account":
				directory.accountRevision = ""
			case "no_scope":
				ctx = context.Background()
			case "not_seek":
				seek = false
			case "not_strict":
				strict = false
			}
			next, v := managementPredicateCacheVersion(ctx, db, owner, keys, seek, strict)
			got, hit := next.get(ctx, v)
			if hit != (name == "hit") {
				t.Fatal("cache escaped source, owner, transaction or selection fence", name)
			}
			if hit {
				if !reflect.DeepEqual(got, values) {
					t.Fatal("typed SQL projection changed")
				}
				got[0].Evidence[0] = 2
				*got[0].Usage.InputPrice = 10
				*values[0].Usage.OutputTokens = 0
				again, _ := next.get(ctx, v)
				if again[0].Evidence[0] != 1 || *again[0].Usage.InputPrice != math.SmallestNonzeroFloat64 || *again[0].Usage.OutputTokens != math.MaxInt64 {
					t.Fatal("receipt hydration or source aliases contaminated retained data")
				}
			}
		})
	}
}

func TestManagementPredicateCacheRejectsPartialAndUnboundedOutput(t *testing.T) {
	for _, name := range []string{"missing", "duplicate", "foreign", "invalid", "digest", "non_finite", "oversize", "cancel"} {
		t.Run(name, func(t *testing.T) {
			directory, ctx, values, positions := predicateCacheFixture()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			store, version := managementPredicateCacheVersion(ctx, directory.tx, "owner", "keys", true, true)
			switch name {
			case "missing":
				values = nil
			case "duplicate":
				values = append(values, values[0])
				positions.values[managementStreamIdentity{"a", 2, "模型"}] = 1
			case "foreign":
				values[0].Account = "foreign"
			case "invalid":
				values[0].Valid = false
			case "digest":
				values[0].Evidence = []byte{1}
			case "non_finite":
				*values[0].Usage.InputPrice = math.NaN()
			case "oversize":
				values[0].Verdict = strings.Repeat("x", managementPredicateBudget)
			case "cancel":
				cancel()
			}
			store.put(ctx, version, values, positions)
			if len(store.entries) != 0 || store.bytes != 0 {
				t.Fatal("unsafe output was retained")
			}
		})
	}
	// Empty selected sets remain distinct from a missing cache entry.
	directory, ctx, _, _ := predicateCacheFixture()
	store, version := managementPredicateCacheVersion(ctx, directory.tx, "owner", "[]", true, true)
	store.put(ctx, version, []managementPredicateValue{}, managementStreamPositionIndex{values: map[managementStreamIdentity]int{}})
	if got, ok := store.get(ctx, version); !ok || got == nil || len(got) != 0 {
		t.Fatal("empty selection changed")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok := store.get(canceled, version); ok {
		t.Fatal("canceled read accepted cache hit")
	}
}

func TestManagementPredicateCacheConcurrentBudget(t *testing.T) {
	store := &managementPredicateSets{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			directory, ctx, values, positions := predicateCacheFixture()
			directory.predicates, directory.owner = store, fmt.Sprint(i)
			for n := 0; n < 10; n++ {
				*directory.signalRevision = int64(n)
				s, v := managementPredicateCacheVersion(ctx, directory.tx, directory.owner, "keys", true, true)
				s.put(ctx, v, values, positions)
				if got, ok := s.get(ctx, v); ok && !reflect.DeepEqual(got, values) {
					t.Error("concurrent data changed")
				}
			}
		}(i)
	}
	wg.Wait()
	total := 0
	for _, entry := range store.entries {
		total += entry.bytes
		if cap(entry.body) != len(entry.body) {
			t.Fatal("unaccounted byte capacity retained")
		}
	}
	if total != store.bytes || store.bytes > managementPredicateBudget || len(store.entries) > 2 {
		t.Fatal("cache exceeded byte/entry budget")
	}
}

func preparePredicateFixture(t *testing.T) *Service {
	t.Helper()
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state)
 SELECT 'a',g,'off-scope-'||m,'idle' FROM generate_series(1,61) g CROSS JOIN generate_series(1,32) m`); err != nil {
		t.Fatal(err)
	}
	prepareManagementRevisionsForTest(t, s.control.db)
	return s
}

// Compare every cached field and receipt ID with the original SQL in the same
// snapshot. Invalidation uses real triggers, including direct projection writes.
func TestManagementPredicateCacheUsesTransactionalVersions(t *testing.T) {
	s := preparePredicateFixture(t)
	db, ctx := s.control.db, managementTraceContext(context.Background())
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	read := func(tx *sql.Tx, owner string) ([]managementStreamModel, bool) {
		t.Helper()
		output.Reset()
		directory := &managementDirectoryScope{store: &s.modelDirectories, predicates: &s.predicateSets, database: db, tx: tx, owner: owner}
		cached := context.WithValue(ctx, managementDirectoryScopeKey{}, directory)
		proof, _, err := s.managementReadProof(cached, tx, owner)
		if err != nil || proof.ExportStamp == "" {
			t.Fatal("no current proof", err)
		}
		directory.accountRevision = proof.Epochs["console_sub_accounts"]
		scope := managementStreamScope{CompactRevision: true, Model: "gpt-5.5", Names: []string{"gpt-5.5", "gpt-6-astra"}}
		got, ids, err := readManagementStreamModels(cached, tx, owner, true, scope)
		hit := strings.Contains(output.String(), "management_predicate_cache hit=true")
		if hit && strings.Contains(output.String(), "stage=model_signal_rows ") {
			t.Fatal("hit repeated SQL")
		}
		want, wantIDs, wantErr := readManagementStreamModels(context.Background(), tx, owner, true, scope)
		slices.Sort(ids)
		slices.Sort(wantIDs)
		if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(ids, wantIDs) {
			t.Fatal("cached predicates differ from SQL", err, wantErr)
		}
		return got, hit
	}
	fresh := func(owner string) ([]managementStreamModel, bool) {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		return read(tx, owner)
	}
	if _, hit := fresh("owner"); hit {
		t.Fatal("first read did not populate")
	}
	if _, hit := fresh("owner"); !hit {
		t.Fatal("unchanged inputs repeated SQL", output.String())
	}
	old, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback()
	before, _ := read(old, "owner")
	for _, query := range []string{
		`UPDATE console_sub_models SET message='new diagnostic' WHERE group_id=1 AND model='gpt-5.5'`,
		`UPDATE console_sub_models SET state='running' WHERE group_id=1 AND model='gpt-5.5'`,
		`UPDATE console_sub_models SET result=jsonb_set(result,'{availability,status}','"error"') WHERE group_id=1 AND model='gpt-5.5'`,
		`UPDATE console_management_model_signal SET verdict='direct projection change' WHERE group_id=1 AND model='gpt-5.5'`,
		`UPDATE console_management_model_signal SET probe_id='new-receipt',quality_probe_id='new-quality',has_usage=true,input_price=9 WHERE group_id=1 AND model='gpt-5.5'`,
		`UPDATE console_sub_accounts SET owner='foreign' WHERE id='a'`,
		`UPDATE console_sub_accounts SET owner='owner' WHERE id='a'`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES('a',62,'new group','openai'); INSERT INTO console_sub_models(account_id,group_id,model,state) VALUES('a',62,'gpt-5.5','idle')`,
		`DELETE FROM console_sub_models WHERE group_id=62`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		if _, hit := fresh("owner"); hit {
			t.Fatal("changed source reused retained predicates", query)
		}
		if _, hit := fresh("owner"); !hit {
			t.Fatal("new committed version was not retained", query)
		}
	}
	if after, _ := read(old, "owner"); !reflect.DeepEqual(after, before) {
		t.Fatal("concurrent writes escaped old repeatable-read snapshot")
	}
	if foreign, _ := fresh("foreign"); len(foreign) != 0 {
		t.Fatal("ownership cache leaked models")
	}
	fresh("owner")
	write, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := write.Exec(`UPDATE console_sub_models SET state='error'`); err != nil {
		t.Fatal(err)
	}
	write.Rollback()
	if _, hit := fresh("owner"); !hit {
		t.Fatal("rollback invalidated committed generation")
	}
	// Unrelated receipt/queue changes must keep predicates reusable while their
	// callers continue to read those current dependencies separately.
	for _, query := range []string{
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{input_price}','7') WHERE id='q-1-gpt-5.5'`,
		`INSERT INTO console_sub_check_queue(account_id,group_id,kind,model) VALUES('a',1,'model','gpt-5.5')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		if _, hit := fresh("owner"); !hit {
			t.Fatal("unrelated write repeated model SQL", query)
		}
	}
}

func TestManagementPredicateCacheKeepsFallbackAndCompletePages(t *testing.T) {
	s := preparePredicateFixture(t)
	db, ctx := s.control.db, context.Background()
	f := managementQueryFilters{GroupStatus: "available", Model: "gpt-5.5"}
	for _, query := range []string{
		`SELECT 1`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{input_price}','8') WHERE id='a-1-gpt-5.5'`,
		`UPDATE console_management_projection_state SET phase='verify_models',complete=false WHERE singleton`,
		`UPDATE console_management_projection_state SET phase='complete',complete=true WHERE singleton; UPDATE console_management_model_signal SET valid=false WHERE group_id=1 AND model='gpt-5.5'`,
		`UPDATE console_management_model_signal SET valid=true,evidence_digest=decode('ab','hex') WHERE group_id=1 AND model='gpt-5.5'`,
		`DELETE FROM console_management_model_signal WHERE group_id=1 AND model='gpt-5.5'`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			got, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
			want, wantErr := s.readManagementQuerySnapshot(ctx, "owner", f, "", true)
			got.Page.ReadAtMS = want.Page.ReadAtMS
			if !errors.Is(err, wantErr) || !reflect.DeepEqual(got.Page, want.Page) ||
				!reflect.DeepEqual(managementContextForPage(got.View, got.Page, f.Model), managementContextForPage(want.View, want.Page, f.Model)) {
				t.Fatal("page/context or original fallback changed", query, err, wantErr)
			}
		}
	}
}

func TestManagementPredicateCacheLocalMeasurement(t *testing.T) {
	s := preparePredicateFixture(t)
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	directory := &managementDirectoryScope{store: &s.modelDirectories, predicates: &s.predicateSets, database: s.control.db, tx: tx, owner: "owner"}
	cached := context.WithValue(ctx, managementDirectoryScopeKey{}, directory)
	proof, _, err := s.managementReadProof(cached, tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	directory.accountRevision = proof.Epochs["console_sub_accounts"]
	scope := managementStreamScope{CompactRevision: true, Model: "gpt-5.5", Names: []string{"gpt-5.5", "gpt-6-astra"}}
	if _, _, err := readManagementStreamModels(cached, tx, "owner", true, scope); err != nil {
		t.Fatal(err)
	}
	for _, reuse := range []bool{false, true} {
		if reuse {
			directory.predicates = &s.predicateSets
		} else {
			directory.predicates = nil
		}
		start := time.Now()
		for i := 0; i < 10; i++ {
			if _, _, err := readManagementStreamModels(cached, tx, "owner", true, scope); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("local_predicate_read cache=%t mean_ms=%.3f selected=122", reuse, float64(time.Since(start).Microseconds())/10000)
	}
}

// Match the selected row count observed online. This measures only immutable
// projection decoding, not the complete HTTP request or production cold I/O.
func BenchmarkManagementPredicateCache1535(b *testing.B) {
	directory, ctx, sample, _ := predicateCacheFixture()
	values := make([]managementPredicateValue, 1535)
	positions := managementStreamPositionIndex{values: map[managementStreamIdentity]int{}}
	for i := range values {
		values[i] = sample[0]
		values[i].Group = int64(i)
		values[i].Probe = fmt.Sprint("availability-receipt-", i)
		values[i].Quality = fmt.Sprint("quality-receipt-", i)
		positions.values[managementStreamIdentity{values[i].Account, int64(i), values[i].Model}] = i
	}
	store, version := managementPredicateCacheVersion(ctx, directory.tx, "owner", "keys", true, true)
	store.put(ctx, version, values, positions)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got, hit := store.get(ctx, version); !hit || len(got) != len(values) {
			b.Fatal("bounded projection was not retained")
		}
	}
}

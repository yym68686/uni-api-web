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
	"strings"
	"sync"
	"testing"
)

func qualityCacheScopeForTest() (*managementQualityScope, context.Context) {
	scope := &managementQualityScope{store: &managementTargetStreams{}, tx: &sql.Tx{}, version: managementQualityVersion{&sql.DB{}, "owner", "1", "2", "3"}}
	return scope, context.WithValue(context.Background(), managementQualityScopeKey{}, scope)
}

func TestManagementQualityCacheRequiresExactSnapshotAndIndependentRawRows(t *testing.T) {
	for _, name := range []string{"hit", "owner", "account", "transaction", "pool", "missing_accounts", "missing_history", "missing_links", "accounts_changed", "history_changed", "links_changed", "database_changed", "no_scope"} {
		t.Run(name, func(t *testing.T) {
			scope, ctx := qualityCacheScopeForTest()
			rows := []managementQualityRecord{{account: "雪", group: 7, at: 123, history: qualitySummary{Total: 2, Successful: 1}, raw: []byte(`null`)}, {account: "a", raw: []byte{}}, {account: "b", raw: nil}}
			calls := 0
			read := func(consume managementQualityConsumer) error {
				calls++
				for _, row := range rows {
					if err := consume(row, nil); err != nil {
						return err
					}
				}
				return nil
			}
			var got []managementQualityRecord
			consume := func(r managementQualityRecord, _ *managementRowsTrace) error { got = append(got, r); return nil }
			if err := readManagementQualityStream(ctx, scope.tx, "owner", "", read, consume); err != nil || calls != 1 {
				t.Fatal(err, calls)
			}
			var db controlQueryDB = scope.tx
			owner, account := "owner", ""
			switch name {
			case "owner":
				owner = "foreign"
			case "account":
				account = "a"
			case "transaction":
				db = &sql.Tx{}
			case "pool":
				db = scope.version.database
			case "missing_accounts":
				scope.version.accounts = ""
			case "missing_history":
				scope.version.history = ""
			case "missing_links":
				scope.version.links = ""
			case "accounts_changed":
				scope.version.accounts = "2"
			case "history_changed":
				scope.version.history = "3"
			case "links_changed":
				scope.version.links = "4"
			case "database_changed":
				scope.version.database = &sql.DB{}
			case "no_scope":
				ctx = context.Background()
			}
			got = nil
			if err := readManagementQualityStream(ctx, db, owner, account, read, consume); err != nil || !reflect.DeepEqual(got, rows) {
				t.Fatal("raw values, ordering or nil/empty shape changed", err)
			}
			wantCalls := 2
			if name == "hit" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatal("scope fence failed", calls, wantCalls)
			}
			if name == "hit" {
				got[0].raw[0], rows[0].raw[0] = 'x', 'y'
				got = nil
				if err := readManagementQualityStream(ctx, db, owner, account, read, consume); err != nil || string(got[0].raw) != "null" {
					t.Fatal("retained raw bytes alias consumer or source", err)
				}
			}
		})
	}
	for _, missing := range []string{"export", "console_sub_accounts", "console_quality_history", "console_quality_group_links"} {
		proof := managementFreshProof{ExportStamp: "proof", Epochs: map[string]string{"console_sub_accounts": "0", "console_quality_history": "0", "console_quality_group_links": "0"}}
		if missing == "export" {
			proof.ExportStamp = ""
		} else {
			delete(proof.Epochs, missing)
		}
		if managementQualityScopeFromProof(&managementTargetStreams{}, &sql.DB{}, &sql.Tx{}, "owner", proof) != nil {
			t.Fatal("incomplete proof admitted", missing)
		}
	}
}

func TestManagementQualityCacheDoesNotAdmitFailedCanceledOrOversizeReads(t *testing.T) {
	for _, name := range []string{"source", "decoder", "cancel", "oversize"} {
		t.Run(name, func(t *testing.T) {
			scope, ctx := qualityCacheScopeForTest()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			sentinel := errors.New("fixture failure")
			row := managementQualityRecord{raw: []byte(`{}`)}
			if name == "oversize" {
				row.raw = make([]byte, managementTargetBudget)
			}
			read := func(consume managementQualityConsumer) error {
				if err := consume(row, nil); err != nil {
					return err
				}
				if name == "source" {
					return sentinel
				}
				if name == "cancel" {
					cancel()
				}
				return nil
			}
			err := readManagementQualityStream(ctx, scope.tx, "owner", "", read, func(managementQualityRecord, *managementRowsTrace) error {
				if name == "decoder" {
					return sentinel
				}
				return nil
			})
			if (name == "source" || name == "decoder") && !errors.Is(err, sentinel) || name == "cancel" && !errors.Is(err, context.Canceled) || name == "oversize" && err != nil {
				t.Fatal("original error lost", err)
			}
			if len(scope.store.quality) != 0 || scope.store.bytes != 0 {
				t.Fatal("incomplete/oversize read retained")
			}
		})
	}
}

func TestManagementQualityAndTargetStreamsShareOriginalCountAndByteBudget(t *testing.T) {
	store, database := &managementTargetStreams{}, &sql.DB{}
	q := func(owner string, n int) *managementQualityStream {
		return &managementQualityStream{version: managementQualityVersion{database, owner, "1", "2", "3"}, bytes: n}
	}
	r := func(owner string, n int) *managementTargetStream {
		return &managementTargetStream{version: managementTargetVersion{database, owner, "1", "2", "3"}, bytes: n}
	}
	quality, target := q("a", 2400000), r("a", 3500000)
	store.putQuality(quality)
	store.put(target)
	if store.getQuality(quality.version) != quality || store.get(target.version) != target || store.bytes != 5900000 {
		t.Fatal("representative quality and target streams did not coexist")
	}
	store.putQuality(q("b", 100))
	if store.getQuality(quality.version) != nil || store.get(target.version) != target || len(store.entries)+len(store.quality) != 2 {
		t.Fatal("shared FIFO count eviction failed")
	}
	store.putQuality(q("b", managementTargetBudget))
	if len(store.entries) != 0 || len(store.quality) != 1 || store.bytes != managementTargetBudget {
		t.Fatal("shared byte eviction or version replacement failed")
	}
	store.put(r("c", managementTargetBudget))
	if len(store.entries) != 1 || len(store.quality) != 0 || store.bytes != managementTargetBudget {
		t.Fatal("target admission did not release quality bytes")
	}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				owner := fmt.Sprint(n)
				if i%2 == 0 {
					entry := q(owner, managementTargetBudget/2)
					store.putQuality(entry)
					if got := store.getQuality(entry.version); got != nil && got.version != entry.version {
						t.Error("concurrent quality version mismatch")
					}
				} else {
					store.put(r(owner, managementTargetBudget/2))
				}
			}
		}(n)
	}
	wg.Wait()
	if len(store.entries)+len(store.quality) > 2 || store.bytes > managementTargetBudget {
		t.Fatal("shared concurrent budget exceeded")
	}
	for _, entry := range store.quality[len(store.quality):cap(store.quality)] {
		if entry != nil {
			t.Fatal("evicted quality row remains retained")
		}
	}
}

func TestManagementQualityCacheHitPreservesCancellationAndDecoderErrors(t *testing.T) {
	scope, ctx := qualityCacheScopeForTest()
	read := func(consume managementQualityConsumer) error {
		for range 3 {
			if err := consume(managementQualityRecord{raw: []byte(`{}`)}, nil); err != nil {
				return err
			}
		}
		return nil
	}
	if err := readManagementQualityStream(ctx, scope.tx, "owner", "", read, func(managementQualityRecord, *managementRowsTrace) error { return nil }); err != nil {
		t.Fatal(err)
	}
	noRead := func(managementQualityConsumer) error { t.Fatal("cached generation unexpectedly reread"); return nil }
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := readManagementQualityStream(canceled, scope.tx, "owner", "", noRead, func(managementQualityRecord, *managementRowsTrace) error {
		t.Fatal("canceled read decoded a row")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("cached cancellation ignored", err)
	}
	ctx, cancel = context.WithCancel(ctx)
	defer cancel()
	calls := 0
	if err := readManagementQualityStream(ctx, scope.tx, "owner", "", noRead, func(managementQualityRecord, *managementRowsTrace) error { calls++; cancel(); return nil }); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("mid-replay cancellation ignored", err, calls)
	}
	sentinel := errors.New("decoder fixture")
	ctx = context.WithValue(context.Background(), managementQualityScopeKey{}, scope)
	if err := readManagementQualityStream(ctx, scope.tx, "owner", "", noRead, func(managementQualityRecord, *managementRowsTrace) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal("cached decoder error hidden", err)
	}
}

func BenchmarkManagementQualityRawReplay(b *testing.B) {
	// Match the observed 788-row/~2.34MB scale with synthetic, non-private data.
	scope, ctx := qualityCacheScopeForTest()
	raw := []byte(`{"quality":{"text":"` + strings.Repeat("x", 2880) + `","duration_ms":7},"availability":{"status":"success"}}`)
	read := func(consume managementQualityConsumer) error {
		for n := range 788 {
			if err := consume(managementQualityRecord{account: "fixture", group: int64(n), raw: raw}, nil); err != nil {
				return err
			}
		}
		return nil
	}
	consume := func(row managementQualityRecord, _ *managementRowsTrace) error {
		var result subResult
		return json.Unmarshal(row.raw, &result)
	}
	if err := readManagementQualityStream(ctx, scope.tx, "owner", "", read, consume); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(raw) * 788))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := readManagementQualityStream(ctx, scope.tx, "owner", "", func(managementQualityConsumer) error { b.Fatal("replay made a database read"); return nil }, consume); err != nil {
			b.Fatal(err)
		}
	}
}

func pinnedQualityScopeForTest(t *testing.T, s *Service, owner string) (*sql.Tx, context.Context) {
	t.Helper()
	tx, err := s.control.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	proof, _, err := s.managementReadProof(context.Background(), tx, owner)
	if err != nil {
		t.Fatal(err)
	}
	scope := managementQualityScopeFromProof(&s.targetStreams, s.control.db, tx, owner, proof)
	if scope == nil {
		t.Fatal("fixture lacks complete snapshot proof")
	}
	ctx := context.WithValue(managementTraceContext(context.Background()), managementQualityScopeKey{}, scope)
	return tx, context.WithValue(ctx, consoleRequestIDKey{}, "quality-cache-123")
}

func TestManagementQualityCachePreservesProjectionBindingsAndFreshEvidence(t *testing.T) {
	s := managementQualityTraceFixture(t)
	s.engine = postgresTestEngine(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	bindings := []qualityBinding{{Source: "quality-source", Provider: "private-provider", Account: "quality-a", Group: 4}}
	read := func(owner string, bindings []qualityBinding) sharedQuality {
		t.Helper()
		tx, ctx := pinnedQualityScopeForTest(t, s, owner)
		defer tx.Rollback()
		got, err := readSharedQualityProjectionFrom(ctx, tx, owner, "", bindings)
		want, wantErr := readSharedQualityProjectionFrom(context.Background(), tx, owner, "", bindings)
		if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("cache changed projection, owner, history or bindings", err, wantErr)
		}
		return got
	}
	want := read("private-owner", bindings)
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	got := read("private-owner", bindings)
	if !reflect.DeepEqual(got, want) || !strings.Contains(output.String(), "management_quality_cache hit=true rows=5 ") || !strings.Contains(output.String(), "stage=shared_quality_cached_rows query_ready_ms=0 ") {
		t.Fatal("complete quality stream not replayed")
	}
	got.Groups[qualityGroupID("quality-a", 1)].Check.Text = "changed consumer"
	got.Groups[qualityGroupID("quality-a", 1)].Check.History.Total = 999
	if !reflect.DeepEqual(read("private-owner", bindings), want) {
		t.Fatal("mutable decoded checks escaped a request")
	}
	changedBindings := []qualityBinding{{Source: "different", Provider: "new", Account: "quality-a", Group: 1}}
	if q := read("private-owner", changedBindings); !reflect.DeepEqual(q.Bindings, changedBindings) || q.channel("quality-source", "private-provider") != nil || q.channel("different", "new") == nil {
		t.Fatal("cached history retained old binding attribution")
	}
	for _, change := range []string{
		`INSERT INTO console_quality_history(check_id,account_id,group_id,checked_at,verdict,successful,result) VALUES('new-history','quality-a',1,900,'pass',true,'{"quality":{"text":"new"}}')`,
		`UPDATE console_quality_history SET result='{"quality":{"text":"updated"}}',verdict='fail',successful=false WHERE check_id='new-history'`,
		`UPDATE console_quality_history SET result='{}' WHERE check_id='new-history'; UPDATE console_quality_history SET result='{"quality":{"text":"updated"}}' WHERE check_id='new-history'`,
		`DELETE FROM console_quality_history WHERE check_id='new-history'`,
		`INSERT INTO console_quality_group_links(check_id,account_id,group_id) VALUES('unlinked','quality-a',5)`,
		`UPDATE console_quality_group_links SET group_id=3 WHERE check_id='unlinked'`,
		`DELETE FROM console_quality_group_links WHERE check_id='unlinked'`,
		`UPDATE console_sub_accounts SET owner='new-owner' WHERE id='quality-a'`,
		`DELETE FROM console_sub_targets WHERE account_id='quality-b'`,
		`TRUNCATE console_quality_group_links`,
		`TRUNCATE console_quality_history CASCADE`,
	} {
		entry := s.targetStreams.quality[0]
		if _, err := s.control.db.Exec(change); err != nil {
			t.Fatal(err)
		}
		read("private-owner", bindings)
		if len(s.targetStreams.quality) != 1 || s.targetStreams.quality[0] == entry {
			t.Fatal("quality/account generation did not invalidate", change)
		}
	}
	if q := read("foreign-owner", nil); len(q.Groups) != 0 {
		t.Fatal("foreign owner received private or truncated history")
	}
	for _, private := range []string{"private-", "quality-a", "quality-source", "new-owner", "SELECT"} {
		if strings.Contains(output.String(), private) {
			t.Fatal("cache diagnostics exposed private data")
		}
	}
}

func TestManagementQualityCachePreservesTypedErrorsAndPinnedSnapshot(t *testing.T) {
	s := managementQualityTraceFixture(t)
	s.engine = postgresTestEngine(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	oldTx, oldCtx := pinnedQualityScopeForTest(t, s, "private-owner")
	old, err := readSharedQualityProjectionFrom(oldCtx, oldTx, "private-owner", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_quality_history SET result='{"availability":{"ttft_ms":"bad"}}' WHERE check_id='tie-last'`); err != nil {
		t.Fatal(err)
	}
	tx, ctx := pinnedQualityScopeForTest(t, s, "private-owner")
	got, gotErr := readSharedQualityProjectionFrom(ctx, tx, "private-owner", "", nil)
	want, wantErr := readSharedQualityProjectionFrom(context.Background(), tx, "private-owner", "", nil)
	tx.Rollback()
	if gotErr == nil || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) {
		t.Fatal("new malformed non-quality field was hidden", gotErr, wantErr)
	}
	// The earlier transaction still authorizes the earlier raw generation.
	if q, err := readSharedQualityProjectionFrom(oldCtx, oldTx, "private-owner", "", nil); err != nil || !reflect.DeepEqual(q, old) {
		t.Fatal("concurrent commit escaped pinned repeatable-read snapshot", err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_quality_history SET result='{"quality":{"text":"new"}}' WHERE check_id='tie-last'`); err != nil {
		t.Fatal(err)
	}
	newTx, newCtx := pinnedQualityScopeForTest(t, s, "private-owner")
	if _, err := readSharedQualityProjectionFrom(newCtx, newTx, "private-owner", "", nil); err != nil {
		t.Fatal(err)
	}
	newTx.Rollback()
	// The new generation displaced the old cache entry. A miss must query the
	// old pinned transaction rather than silently borrowing current data.
	if q, err := readSharedQualityProjectionFrom(oldCtx, oldTx, "private-owner", "", nil); err != nil || !reflect.DeepEqual(q, old) {
		t.Fatal("fallback read escaped pinned repeatable-read snapshot", err)
	}
	oldTx.Rollback()
}

func TestManagementQualityCacheSnapshotMatchesFullSelectionAndPageContext(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	if _, err := s.control.db.Exec(`INSERT INTO console_quality_history(check_id,account_id,group_id,checked_at,verdict,successful,result)
 SELECT 'shared-'||g,'a',g,123,'pass',true,'{"quality":{"status":"success","text":"shared text","duration_ms":7}}' FROM generate_series(1,61) g`); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "quality-page-123")
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	read := func() {
		t.Helper()
		got, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
		want, wantErr := s.readManagementQuerySnapshot(context.Background(), "owner", f, "", true)
		if err != nil || wantErr != nil || got.Page.Revision != want.Page.Revision || !reflect.DeepEqual(got.Page.Rows, want.Page.Rows) || !reflect.DeepEqual(managementContextForPage(got.View, got.Page, f.Model), managementContextForPage(want.View, want.Page, f.Model)) {
			t.Fatal("raw quality reuse changed complete page, selection or metadata", err, wantErr)
		}
	}
	read()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	read()
	if !strings.Contains(output.String(), "management_quality_cache hit=true rows=61 ") || !strings.Contains(output.String(), "management_target_cache hit=true rows=61 ") {
		t.Fatal("normal complete snapshot failed to reuse both raw streams")
	}
	if s.targetStreams.bytes > managementTargetBudget || len(s.targetStreams.entries)+len(s.targetStreams.quality) != 2 {
		t.Fatal("full snapshot exceeded original retention budget")
	}
}

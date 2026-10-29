package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func rateCacheScopeForTest() (*managementRateScope, context.Context) {
	scope := &managementRateScope{store: &managementRateSnapshots{}, tx: &sql.Tx{}, version: managementRateVersion{&sql.DB{}, "private-owner", "proof"}}
	return scope, context.WithValue(context.Background(), managementRateScopeKey{}, scope)
}

func TestManagementRateCacheRequiresEverySourceEpoch(t *testing.T) {
	proof := managementFreshProof{ExportStamp: "proof", Epochs: map[string]string{"console_sub_accounts": "0", "console_sub_targets": "1", "console_sub_usage": "2", "console_sub_observed_rates": "3"}}
	store, database, tx := &managementRateSnapshots{}, &sql.DB{}, &sql.Tx{}
	if managementRateScopeFromProof(store, database, tx, "owner", proof) == nil {
		t.Fatal("complete current proof rejected")
	}
	for relation, value := range proof.Epochs {
		delete(proof.Epochs, relation)
		if managementRateScopeFromProof(store, database, tx, "owner", proof) != nil {
			t.Fatal("incomplete source epoch admitted", relation)
		}
		proof.Epochs[relation] = value
	}
	proof.ExportStamp = ""
	if managementRateScopeFromProof(store, database, tx, "owner", proof) != nil {
		t.Fatal("unprepared compact proof admitted")
	}
}

func TestManagementRateCacheFencesAndIndependentTypedMaps(t *testing.T) {
	for _, name := range []string{"hit", "transaction", "pool", "database", "owner", "account", "empty_proof", "changed_proof", "no_scope"} {
		t.Run(name, func(t *testing.T) {
			scope, ctx := rateCacheScopeForTest()
			want := map[string]map[string]subObservedRate{"a:1": {"模型": {Rate: math.SmallestNonzeroFloat64, ObservedAt: math.MaxInt64}}, "nil": nil, "empty": {}}
			calls := 0
			read := func() (map[string]map[string]subObservedRate, error) { calls++; return want, nil }
			if _, err := readManagementRates(ctx, scope.tx, scope.version.database, scope.version.owner, "", read); err != nil {
				t.Fatal(err)
			}
			var db controlQueryDB = scope.tx
			database, owner, account := scope.version.database, scope.version.owner, ""
			switch name {
			case "transaction":
				db = &sql.Tx{}
			case "pool":
				db = database
			case "database":
				database = &sql.DB{}
			case "owner":
				owner = "foreign"
			case "account":
				account = "a"
			case "empty_proof":
				scope.version.stamp = ""
			case "changed_proof":
				scope.version.stamp = "next"
			case "no_scope":
				ctx = context.Background()
			}
			got, err := readManagementRates(ctx, db, database, owner, account, read)
			if err != nil || !reflect.DeepEqual(got, want) || calls != map[bool]int{true: 1, false: 2}[name == "hit"] {
				t.Fatal("scope or typed rate contract changed", calls, err)
			}
			if name == "hit" {
				got["a:1"]["模型"] = subObservedRate{Rate: 9}
				want["a:1"]["模型"] = subObservedRate{Rate: 10}
				delete(got, "empty")
				next, err := readManagementRates(ctx, db, database, owner, account, read)
				if err != nil || next["a:1"]["模型"].Rate != math.SmallestNonzeroFloat64 || next["a:1"]["模型"].ObservedAt != math.MaxInt64 || next["empty"] == nil || next["nil"] != nil {
					t.Fatal("caller/source map mutation contaminated cache", err)
				}
			}
		})
	}
}

func TestManagementRateCacheRejectsIncompleteOrOversizeResults(t *testing.T) {
	for _, name := range []string{"error", "fallback", "canceled", "oversize", "non_finite", "nil"} {
		t.Run(name, func(t *testing.T) {
			scope, ctx := rateCacheScopeForTest()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			failure := errors.New("fixture error")
			out := map[string]map[string]subObservedRate{"a:1": {"m": {Rate: 0.3}}}
			read := func() (map[string]map[string]subObservedRate, error) {
				switch name {
				case "error":
					return out, failure
				case "fallback":
					return nil, errManagementPredicateFallback
				case "canceled":
					cancel()
				case "oversize":
					out["a:1"][strings.Repeat("x", managementRateBudget)] = subObservedRate{}
				case "non_finite":
					out["a:1"]["m"] = subObservedRate{Rate: math.NaN()}
				case "nil":
					out = nil
				}
				return out, nil
			}
			got, err := readManagementRates(ctx, scope.tx, scope.version.database, scope.version.owner, "", read)
			if name == "error" && !errors.Is(err, failure) || name == "fallback" && !errors.Is(err, errManagementPredicateFallback) || name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("read error lost", err)
			}
			if name != "nil" && len(scope.store.entries) != 0 {
				t.Fatal("unsuccessful/unbounded output retained")
			}
			if name == "nil" {
				if got != nil || err != nil {
					t.Fatal("nil output changed", got, err)
				}
				got, err = readManagementRates(ctx, scope.tx, scope.version.database, scope.version.owner, "", read)
				if got != nil || err != nil {
					t.Fatal("nil hit changed", got, err)
				}
				cancel()
				if _, err = readManagementRates(ctx, scope.tx, scope.version.database, scope.version.owner, "", read); !errors.Is(err, context.Canceled) {
					t.Fatal("query-free hit bypassed cancellation", err)
				}
			}
		})
	}
}

func TestManagementRateCacheConcurrentBudget(t *testing.T) {
	store, database := &managementRateSnapshots{}, &sql.DB{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				scope := &managementRateScope{store: store, tx: &sql.Tx{}, version: managementRateVersion{database, fmt.Sprint(i), fmt.Sprint(n)}}
				ctx := context.WithValue(context.Background(), managementRateScopeKey{}, scope)
				want := map[string]map[string]subObservedRate{"a:1": {strings.Repeat("m", 300000): {Rate: float64(i)}}}
				got, err := readManagementRates(ctx, scope.tx, database, scope.version.owner, "", func() (map[string]map[string]subObservedRate, error) { return want, nil })
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Error("concurrent read changed", err)
				}
			}
		}(i)
	}
	wg.Wait()
	if len(store.entries) > 2 || store.bytes > managementRateBudget {
		t.Fatal("cache exceeded entry/byte bound")
	}
	total := 0
	for _, entry := range store.entries {
		total += entry.bytes
		if len(entry.body) != cap(entry.body) {
			t.Fatal("retained byte capacity exceeds admission accounting")
		}
	}
	if total != store.bytes {
		t.Fatal("eviction byte accounting changed")
	}
}

func TestManagementRateCacheUsesCurrentDatabaseProofAndKeepsOldSnapshot(t *testing.T) {
	s := managementHydrationFixture(t)
	db, ctx := s.control.db, managementTraceContext(context.Background())
	exec := func(query string) {
		t.Helper()
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE console_sub_usage SET result='{"status":"matched","actual_cost":0.3,"total_cost":1,"input_price":2}'`)
	prepareManagementRevisionsForTest(t, db)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	read := func(tx *sql.Tx, owner string) (map[string]map[string]subObservedRate, string) {
		t.Helper()
		proof, _, err := s.managementReadProof(ctx, tx, owner)
		if err != nil || proof.ExportStamp == "" {
			t.Fatal("missing current proof", err)
		}
		scope := &managementRateScope{store: &s.rateSnapshots, tx: tx, version: managementRateVersion{db, owner, proof.ExportStamp}}
		got, err := s.control.subObservedRatesPreparedFrom(context.WithValue(ctx, managementRateScopeKey{}, scope), tx, owner, "")
		want, wantErr := s.control.subObservedRatesForAccountFrom(context.Background(), tx, owner, "")
		if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("rates differ from original", got, want, err, wantErr)
		}
		return got, proof.ExportStamp
	}
	fresh := func() (map[string]map[string]subObservedRate, string) {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		return read(tx, "owner")
	}
	first, stamp := fresh()
	if first["a:1"]["gpt-5.5"].Rate != 0.3 || !strings.Contains(logs.String(), "hit=false stored=true") {
		t.Fatal("prepared miss was not retained", logs.String())
	}
	logs.Reset()
	if _, now := fresh(); now != stamp || !strings.Contains(logs.String(), "management_rate_cache hit=true") || strings.Contains(logs.String(), "stage=rate_rows ") {
		t.Fatal("unchanged proof still ran rate SQL", logs.String())
	}
	old, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback()
	before, oldStamp := read(old, "owner")
	for _, query := range []string{
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{actual_cost}','0.7') WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{total_cost}','2') WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{rate_multiplier}','0.35') WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{created_at}','"2026-10-29T00:00:00Z"') WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{input_price}','3') WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{output_price}','4') WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET result=jsonb_set(result,'{status}','"pending"') WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET started_at=99 WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET status='pending' WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_observed_rates SET result=jsonb_set(result,'{actual_cost}','0.4') WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`,
		`UPDATE console_sub_targets SET remote_key_id=999 WHERE account_id='a' AND group_id=1`,
		`UPDATE console_sub_targets SET remote_key_id=1 WHERE account_id='a' AND group_id=1`,
		`UPDATE console_sub_accounts SET owner='other' WHERE id='a'`,
		`UPDATE console_sub_accounts SET owner='owner' WHERE id='a'`,
		`DELETE FROM console_sub_usage WHERE group_id=1`,
	} {
		exec(query)
		logs.Reset()
		_, next := fresh()
		if next == stamp || strings.Contains(logs.String(), "management_rate_cache hit=true") {
			t.Fatal("rate input change reused old proof", query)
		}
		stamp = next
	}
	if after, same := read(old, "owner"); same != oldStamp || !reflect.DeepEqual(after, before) {
		t.Fatal("concurrent writes contaminated old RR snapshot")
	}
	// A rollback must leave the committed generation unchanged.
	_, stamp = fresh()
	write, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = write.Exec(`UPDATE console_sub_targets SET remote_key_id=999 WHERE group_id=1`); err != nil {
		t.Fatal(err)
	}
	write.Rollback()
	if _, same := fresh(); same != stamp {
		t.Fatal("rollback changed committed proof")
	}
}

func TestManagementRateCacheKeepsProjectionFallbackAndIncompleteness(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	if _, err := db.Exec(`UPDATE console_sub_usage SET result='{"status":"matched","actual_cost":0.3,"total_cost":1,"input_price":2}'`); err != nil {
		t.Fatal(err)
	}
	prepareManagementRevisionsForTest(t, db)
	ctx := managementTraceContext(context.Background())
	for _, query := range []string{
		`SELECT 1`,
		`UPDATE console_management_projection_state SET phase='verify_models',complete=false WHERE singleton`,
		`UPDATE console_management_projection_state SET phase='complete',complete=true WHERE singleton; DELETE FROM console_management_receipt_signal WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET result='{"Status":"matched","actual_cost":0.7,"total_cost":1,"input_price":2}' WHERE id='q-1-gpt-5.5'`,
		`UPDATE console_sub_usage SET result='{"status":"matched","actual_cost":0.7,"total_cost":1,"input_price":2,"duration_ms":1.0}' WHERE id='q-1-gpt-5.5'`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		proof, _, err := s.managementReadProof(ctx, tx, "owner")
		if err != nil {
			t.Fatal(err)
		}
		scope := &managementRateScope{store: &s.rateSnapshots, tx: tx, version: managementRateVersion{db, "owner", proof.ExportStamp}}
		var output bytes.Buffer
		previous := log.Writer()
		log.SetOutput(&output)
		got, err := s.control.subObservedRatesPreparedFrom(context.WithValue(ctx, managementRateScopeKey{}, scope), tx, "owner", "")
		log.SetOutput(previous)
		want, wantErr := s.control.subObservedRatesForAccountFrom(context.Background(), tx, "owner", "")
		tx.Rollback()
		if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("projection fallback changed full decoder result", query, err, wantErr)
		}
		if query != `SELECT 1` && (strings.Contains(output.String(), "management_rate_cache hit=true") || !strings.Contains(output.String(), "stage=rate_raw_rows ")) {
			t.Fatal("incomplete/missing/unsupported projection bypassed original reader", query, output.String())
		}
	}
}

func TestManagementRateCachePreservesCompletePageAndContext(t *testing.T) {
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"status":"matched","actual_cost":0.3,"total_cost":1,"input_price":2}'`); err != nil {
		t.Fatal(err)
	}
	prepareManagementRevisionsForTest(t, s.control.db)
	f := managementQueryFilters{GroupStatus: "available", Model: "gpt-5.5"}
	ctx := context.Background()
	for n := 0; n < 2; n++ {
		got, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
		want, wantErr := s.readManagementQuerySnapshot(ctx, "owner", f, "", true)
		got.Page.ReadAtMS = want.Page.ReadAtMS
		if err != nil || wantErr != nil || !reflect.DeepEqual(got.Page, want.Page) || !reflect.DeepEqual(managementContextForPage(got.View, got.Page, f.Model), managementContextForPage(want.View, want.Page, f.Model)) {
			t.Fatal("page/context changed", err, wantErr)
		}
	}
	if len(s.rateSnapshots.entries) != 1 {
		t.Fatal("normal snapshot did not install bounded rate scope")
	}
}

func TestManagementRateCacheLocalMeasurement(t *testing.T) {
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`UPDATE console_sub_usage SET result='{"status":"matched","actual_cost":0.3,"total_cost":1,"input_price":2}'`); err != nil {
		t.Fatal(err)
	}
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := managementTraceContext(context.Background())
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	proof, _, err := s.managementReadProof(ctx, tx, "owner")
	if err != nil || proof.ExportStamp == "" {
		t.Fatal(err)
	}
	scope := &managementRateScope{store: &s.rateSnapshots, tx: tx, version: managementRateVersion{s.control.db, "owner", proof.ExportStamp}}
	cached := context.WithValue(ctx, managementRateScopeKey{}, scope)
	if _, err := s.control.subObservedRatesPreparedFrom(cached, tx, "owner", ""); err != nil {
		t.Fatal(err)
	}
	for _, reuse := range []bool{false, true} {
		readCtx := ctx
		if reuse {
			readCtx = cached
		}
		start := time.Now()
		for i := 0; i < 10; i++ {
			if _, err := s.control.subObservedRatesPreparedFrom(readCtx, tx, "owner", ""); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("local_rate_read cache=%t mean_ms=%.3f groups=61 models=183", reuse, float64(time.Since(start).Microseconds())/10000)
	}
}

package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func directoryOrderFixture(n int) []managementStreamModel {
	models := streamOrderFixture(n)
	for i := range models {
		models[i].item.Model.Message = ""
		models[i].probe, models[i].quality, models[i].evidence = "", "", nil
	}
	return models
}

func directoryOrderScope(models []managementStreamModel) (context.Context, *managementDirectoryScope) {
	tx, database := &sql.Tx{}, &sql.DB{}
	scope := &managementDirectoryScope{store: &managementModelDirectories{}, tx: tx, database: database, owner: "private-owner", accountRevision: "10", modelRevision: 11, ready: true}
	scope.directory = packManagementModelDirectory(managementDirectoryVersion{database, scope.owner, scope.accountRevision, scope.modelRevision}, models)
	return context.WithValue(context.Background(), managementDirectoryScopeKey{}, scope), scope
}

func TestManagementModelDirectoryOrderKeepsSourceAndCurrentEvidence(t *testing.T) {
	for _, n := range []int{0, 1, 2, 13, 749, 190000} {
		models := directoryOrderFixture(n)
		before := slices.Clone(models)
		ctx, scope := directoryOrderScope(models)
		if scope.directory == nil || !scope.directory.ordered || !reflect.DeepEqual(scope.directory.expand(), models) {
			t.Fatal("order metadata changed source order or failed normal admission", n)
		}
		for i := range models {
			models[i].item.Model.Result = &subResult{CheckedAt: int64(i)}
			models[i].item.Model.State = "current state"
			models[i].probe, models[i].evidence = fmt.Sprint(i), []byte{byte(i)}
		}
		want := slices.Clone(models)
		previousManagementStreamOrder(want)
		order := managementDirectoryStreamOrder(ctx, scope.tx, scope.owner, models)
		for i := range want {
			got := &models[i]
			if order != nil {
				got = order[i]
			}
			if !reflect.DeepEqual(*got, want[i]) || got != &models[got.item.Model.Result.CheckedAt] {
				t.Fatal("C-byte order or current request's evidence changed", n, i)
			}
		}
		if !reflect.DeepEqual(scope.directory.expand(), before) {
			t.Fatal("current predicate graph was retained in the directory")
		}
	}
}

func TestManagementModelDirectoryOrderByteKeysExtremesAndDuplicates(t *testing.T) {
	models := []managementStreamModel{}
	for _, account := range []string{"🙂", "雪", "e\u0301", "é", "a", ""} {
		for _, group := range []int64{1<<63 - 1, 1, 0, -1, -1 << 63} {
			for _, name := range []string{"🙂", "雪", "e\u0301", "é", "a\x00", "a", "<>&", ""} {
				models = append(models, managementStreamModel{item: managementModelSignals{Account: account, Group: group, Model: subModelResult{Model: name}}, message: "\xff\x00"})
			}
		}
	}
	for _, duplicate := range []bool{false, true} {
		fixture := slices.Clone(models)
		if duplicate {
			fixture = append(fixture, fixture[17])
		}
		ctx, scope := directoryOrderScope(fixture)
		if scope.directory == nil || scope.directory.ordered == duplicate {
			t.Fatal("duplicate identities did not retain the original sorter")
		}
		for i := range fixture {
			fixture[i].item.Model.Result = &subResult{CheckedAt: int64(i)}
		}
		want := managementOrderedStreamModels(fixture)
		got := managementDirectoryStreamOrder(ctx, scope.tx, scope.owner, fixture)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("byte keys, int64 extremes or duplicate evidence changed")
		}
	}
}

func TestManagementModelDirectoryOrderRejectsMismatchedScopeOrIdentity(t *testing.T) {
	for _, test := range []string{"reuse", "transaction", "owner", "account_epoch", "model_revision", "database", "unprepared", "no_entry", "identity", "positions", "length", "legacy_read"} {
		t.Run(test, func(t *testing.T) {
			models := directoryOrderFixture(50)
			ctx, scope := directoryOrderScope(models)
			ctx = managementTraceContext(ctx)
			var db controlQueryDB = scope.tx
			owner := scope.owner
			switch test {
			case "transaction":
				db = &sql.Tx{}
			case "owner":
				owner = "other-private-owner"
			case "account_epoch":
				scope.accountRevision = "12"
			case "model_revision":
				scope.modelRevision++
			case "database":
				scope.database = &sql.DB{}
			case "unprepared":
				scope.ready = false
			case "no_entry":
				scope.directory = nil
			case "identity":
				models[0].item.Account = "changed identity"
			case "positions":
				slices.Reverse(models)
			case "length":
				models = models[:len(models)-1]
			case "legacy_read":
				if _, err := readManagementModelDirectory(ctx, scope.tx, owner, false, func() ([]managementStreamModel, error) { return models, nil }); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(previous)
			want := managementOrderedStreamModels(models)
			got := managementDirectoryStreamOrder(ctx, db, owner, models)
			if !reflect.DeepEqual(got, want) || strings.Contains(output.String(), "management_model_order reuse=true") != (test == "reuse") {
				t.Fatal("mismatch reused ordering or changed original fallback")
			}
			if strings.Contains(output.String(), "private-owner") {
				t.Fatal("order log exposed an owner")
			}
		})
	}
}

func TestManagementModelDirectoryOrderKeepsExactBudgetFallback(t *testing.T) {
	models := []managementStreamModel{{item: managementModelSignals{Account: "z"}}, {item: managementModelSignals{Account: "a"}}}
	ctx, scope := directoryOrderScope(models)
	base := scope.directory
	if len(base.order) != 2 {
		t.Fatal("fixture has no order index")
	}
	models[0].message = strings.Repeat("x", managementDirectoryBudget-base.bytes+len(base.order)*4-16)
	entry := packManagementModelDirectory(base.version, models)
	if entry == nil || entry.bytes != managementDirectoryBudget || entry.ordered || entry.order != nil {
		t.Fatal("optional order metadata changed exact directory budget admission")
	}
	scope.directory = entry
	if !reflect.DeepEqual(managementDirectoryStreamOrder(ctx, scope.tx, scope.owner, models), managementOrderedStreamModels(models)) {
		t.Fatal("order budget fallback changed results")
	}
}

func BenchmarkManagementModelDirectoryOrdering(b *testing.B) {
	models := directoryOrderFixture(190000)
	ctx, scope := directoryOrderScope(models)
	for _, mode := range []string{"current_pointer_sort", "versioned_index"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if mode == "current_pointer_sort" {
					streamOrderBenchmarkSink = managementOrderedStreamModels(models)
				} else {
					streamOrderBenchmarkSink = managementDirectoryStreamOrder(ctx, scope.tx, scope.owner, models)
				}
			}
		})
	}
}

func BenchmarkManagementModelDirectoryColdOrdering(b *testing.B) {
	models := directoryOrderFixture(190000)
	_, scope := directoryOrderScope(models)
	base := *scope.directory
	base.bytes -= len(base.order) * 4
	base.order, base.ordered = nil, false
	for _, mode := range []string{"current_pointer_sort", "prepare_index_and_expand"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if mode == "current_pointer_sort" {
					streamOrderBenchmarkSink = managementOrderedStreamModels(models)
				} else {
					entry := base
					entry.prepareOrder()
					local := *scope
					local.directory = &entry
					localCtx := context.WithValue(context.Background(), managementDirectoryScopeKey{}, &local)
					streamOrderBenchmarkSink = managementDirectoryStreamOrder(localCtx, local.tx, local.owner, models)
				}
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
)

func TestManagementScopedIndexKeepsCompleteDirectoryAndPredicateValues(t *testing.T) {
	s := managementHydrationFixture(t)
	for _, scope := range []managementStreamScope{
		{CompactRevision: true, Model: "gpt-5.5", Names: []string{"gpt-5.5", "gpt-6-astra"}},
		{CompactRevision: true, Model: "gpt-5.5", Names: []string{"gpt-5.5", "extra-model"}},
		{CompactRevision: true, Model: "gpt-5.5", Names: []string{}},
		{CompactRevision: true, Model: "extra-model", Names: []string{"extra-model", "gpt-6-astra"}},
		{Model: "gpt-5.5", Names: []string{"gpt-5.5", "gpt-6-astra"}},
	} {
		baselineScope := scope
		baselineScope.CompactRevision = false
		before, _, err := readManagementStreamModels(context.Background(), s.control.db, "owner", true, baselineScope)
		if err != nil {
			t.Fatal(err)
		}
		var logs bytes.Buffer
		previous := log.Writer()
		func() {
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			got, _, err := readManagementStreamModels(managementTraceContext(context.Background()), s.control.db, "owner", true, scope)
			if err != nil || len(got) != len(before) || len(got) != 183 {
				t.Fatal("complete directory changed", err)
			}
			for i := range before {
				if !reflect.DeepEqual(got[i].item, before[i].item) || got[i].message != before[i].message {
					t.Fatal("public predicates or directory metadata changed")
				}
			}
		}()
		positions, scoped := 183, false
		if scope.CompactRevision && scope.Model == "gpt-5.5" {
			positions, scoped = len(scope.Names)*61, true
		}
		if !strings.Contains(logs.String(), fmt.Sprintf("management_signal_index models=183 positions=%d scoped=%t ", positions, scoped)) {
			t.Fatal("unexpected position membership", logs.String())
		}
	}
}

func TestManagementStreamPositionIndexPreservesLastMatchAndInput(t *testing.T) {
	models := []managementModelSignals{
		{Account: "a", Group: 1, Model: subModelResult{Model: "selected"}},
		{Account: "a", Group: 1, Model: subModelResult{Model: "off-scope"}},
		{Account: "b", Group: 1, Model: subModelResult{Model: "selected"}},
		{Account: "a", Group: 2, Model: subModelResult{Model: "selected"}},
		{Account: "a", Group: 1, Model: subModelResult{Model: "selected"}},
		{Account: "a", Group: 1, Model: subModelResult{Model: "雪🙂"}},
	}
	before := append([]managementModelSignals{}, models...)
	for _, names := range []map[string]bool{nil, {}, {"selected": true, "雪🙂": true}, {"off-scope": true}} {
		p := managementStreamPositionIndex{values: map[managementStreamIdentity]int{}, only: names}
		all := map[managementStreamIdentity]int{}
		for i, item := range models {
			p.add(item, i)
			all[managementStreamIdentity{item.Account, item.Group, item.Model.Model}] = i
		}
		for key := range all {
			if names != nil && !names[key.model] {
				delete(all, key)
			}
		}
		if !reflect.DeepEqual(p.values, all) || !reflect.DeepEqual(models, before) {
			t.Fatal("directory membership, duplicate winner or input changed")
		}
	}
	managementStreamPositionIndex{}.add(models[0], 0) // catalog path has no index.
}

var managementStreamIndexBenchmarkSink managementStreamPositionIndex

func BenchmarkManagementStreamPositionIndex(b *testing.B) {
	models := make([]managementModelSignals, 189656)
	for i := range models {
		models[i] = managementModelSignals{Account: fmt.Sprint(i / 4000), Group: int64(i / 288), Model: subModelResult{Model: fmt.Sprintf("model-%03d", i%288)}}
	}
	for _, mode := range []string{"legacy_all", "all", "selected"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				p := managementStreamPositionIndex{values: map[managementStreamIdentity]int{}}
				if mode == "selected" {
					p.only = map[string]bool{"model-001": true, "model-002": true}
				}
				if mode == "legacy_all" {
					for i, item := range models {
						p.values[managementStreamIdentity{item.Account, item.Group, item.Model.Model}] = i
					}
				} else {
					for i, item := range models {
						p.add(item, i)
					}
				}
				managementStreamIndexBenchmarkSink = p
			}
			b.ReportMetric(float64(len(managementStreamIndexBenchmarkSink.values)), "positions/op")
		})
	}
}

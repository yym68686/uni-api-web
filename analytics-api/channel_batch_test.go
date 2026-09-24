package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBatchSnapshotMatchingBindingsAreNoop(t *testing.T) {
	snapshot := retainedSnapshot{Version: 2, Settings: map[string]json.RawMessage{}, Rules: []retainedRule{{KeyID: "key", Model: "m", Order: []string{"peer", "copy"}, Disabled: []string{"peer"}}}}
	before := mustJSON(snapshot)
	in := channelBatchInput{Part: "all", Targets: []channelBatchTarget{{Key: "key", Provider: "copy", Current: map[string]string{"m": "up"}, Models: map[string]string{"m": "up"}, Positions: map[string]int{"m": 2}}}}
	rows := map[string][]batchCatalogRow{"key": {{Provider: "peer", Model: "m"}, {Provider: "copy", Model: "m", Upstream: "up"}}}
	changed, err := buildBatchSnapshot(&snapshot, in, rows, nil)
	if err != nil || changed || mustJSON(snapshot) != before {
		t.Fatal(changed, err, snapshot)
	}
}

func TestBatchSnapshotMovesPreserveMatchingAnchors(t *testing.T) {
	snapshot := retainedSnapshot{Version: 2, Settings: map[string]json.RawMessage{}, Rules: []retainedRule{
		{KeyID: "key", Model: "m", Order: []string{"a", "b", "c"}, Disabled: []string{"other"}},
		{KeyID: "untouched", Model: "m", Order: []string{"a", "b", "c"}},
	}}
	in := channelBatchInput{Part: "positions"}
	for p, pos := range map[string]int{"b": 2, "c": 1} {
		in.Targets = append(in.Targets, channelBatchTarget{Key: "key", Provider: p, Current: map[string]string{"m": "m"}, Models: map[string]string{"m": "m"}, Positions: map[string]int{"m": pos}})
	}
	rows := map[string][]batchCatalogRow{"key": {{Provider: "a", Model: "m"}, {Provider: "b", Model: "m"}, {Provider: "c", Model: "m"}}}
	changed, err := buildBatchSnapshot(&snapshot, in, rows, nil)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if !reflect.DeepEqual(snapshot.Rules[0].Order, []string{"c", "b", "a"}) || !reflect.DeepEqual(snapshot.Rules[0].Disabled, []string{"other"}) || !reflect.DeepEqual(snapshot.Rules[1].Order, []string{"a", "b", "c"}) {
		t.Fatal(snapshot.Rules)
	}
}

func TestBatchSnapshotReplacesNativeAndCopyAcrossKeys(t *testing.T) {
	snapshot := retainedSnapshot{Version: 2, Settings: map[string]json.RawMessage{}, Channels: []retainedChannel{{KeyID: "two", Provider: "copy", Models: []string{"old"}}}, Rules: []retainedRule{{KeyID: "two", Model: "old", Order: []string{"copy", "peer"}, Disabled: []string{"copy"}}}}
	in := channelBatchInput{Part: "models", Targets: []channelBatchTarget{
		{Key: "one", Provider: "native", Origin: "native", Current: map[string]string{"old": "up"}, Models: map[string]string{"new": "up"}, Positions: map[string]int{"new": 1}},
		{Key: "two", Provider: "copy", Origin: "native", Current: map[string]string{"old": "up"}, Models: map[string]string{"new": "up"}, Positions: map[string]int{"new": 2}},
	}}
	rows := map[string][]batchCatalogRow{
		"one": {{Provider: "native", Model: "old", Upstream: "up"}, {Provider: "peer", Model: "new"}},
		"two": {{Provider: "copy", Model: "old", Upstream: "up"}, {Provider: "peer", Model: "new"}},
	}
	docs := map[string]map[string]any{}
	for _, target := range in.Targets {
		docs[target.Key+"\n"+target.Provider] = map[string]any{"provider": target.Provider, "engine": "claude", "api": "secret", "base_url": "https://example.test", "preferences": map[string]any{"only_request_types": []string{"compaction"}}}
	}
	changed, err := buildBatchSnapshot(&snapshot, in, rows, docs)
	if err != nil || !changed || len(snapshot.Channels) != 2 {
		t.Fatal(changed, err, snapshot)
	}
	if !snapshotExcluded(snapshot.Rules, snapshot.Channels, "one")["native"] {
		t.Fatal("native not suppressed")
	}
	if len(snapshot.Rules[0].Disabled) != 0 || !reflect.DeepEqual(snapshot.Rules[0].Order, []string{"peer"}) {
		t.Fatal("removed model not pruned", snapshot.Rules)
	}
	for _, c := range snapshot.Channels {
		var doc map[string]any
		_ = json.Unmarshal(c.Definition, &doc)
		if doc["engine"] != "claude" || doc["api"] != "secret" || doc["preferences"] == nil {
			t.Fatal("settings lost")
		}
	}
	if len(rows["one"]) != 2 || len(rows["two"]) != 2 {
		t.Fatal(rows)
	}
}

func TestBatchSnapshotValidatesEveryCurrentMappingBeforeChanges(t *testing.T) {
	snapshot := retainedSnapshot{Version: 2, Settings: map[string]json.RawMessage{}}
	before := mustJSON(snapshot)
	in := channelBatchInput{Part: "delete", Targets: []channelBatchTarget{
		{Key: "one", Provider: "native", Origin: "native", Current: map[string]string{"m": "m"}},
		{Key: "two", Provider: "native", Origin: "native", Current: map[string]string{"m": "wrong-upstream"}},
	}}
	rows := map[string][]batchCatalogRow{"one": {{Provider: "native", Model: "m"}}, "two": {{Provider: "native", Model: "m"}}}
	if _, err := buildBatchSnapshot(&snapshot, in, rows, nil); err == nil || mustJSON(snapshot) != before {
		t.Fatal("partial mutation", err, snapshot)
	}
}

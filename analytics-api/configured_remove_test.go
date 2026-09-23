package main

import (
	"encoding/json"
	"testing"
)

func TestRemoveConfiguredBindingPreservesOtherKeysAndDefinitions(t *testing.T) {
	snapshot := retainedSnapshot{Version: 2, Settings: map[string]json.RawMessage{"native": json.RawMessage(`{"set":{"/engine":"gpt"}}`), "copy": json.RawMessage(`{"set":{}}`)}, Channels: []retainedChannel{{Provider: "copy", KeyID: "one", Models: []string{"alias"}}, {Provider: "other-copy", KeyID: "two"}}, Rules: []retainedRule{
		{KeyID: "one", Model: "astra", Order: []string{"native", "peer"}, Disabled: []string{"peer"}},
		{KeyID: "two", Model: "astra", Order: []string{"native", "peer"}},
		{KeyID: "one", Order: []string{}, Disabled: []string{"older"}},
	}}
	other := mustJSON(snapshot.Rules[1])
	settings := mustJSON(snapshot.Settings)
	if err := removeConfiguredFromSnapshot(&snapshot, "one", "native", true); err != nil {
		t.Fatal(err)
	}
	if mustJSON(snapshot.Rules[1]) != other || mustJSON(snapshot.Settings) != settings {
		t.Fatal("changed other key or base settings")
	}
	if !snapshotExcluded(snapshot.Rules, snapshot.Channels, "one")["native"] || snapshotExcluded(snapshot.Rules, snapshot.Channels, "two")["native"] {
		t.Fatal("wrong removal scope")
	}
	before := mustJSON(snapshot)
	if removeConfiguredFromSnapshot(&snapshot, "two", "copy", false) == nil || mustJSON(snapshot) != before {
		t.Fatal("accepted cross-key deletion")
	}
	if removeConfiguredFromSnapshot(&snapshot, "one", "missing", false) == nil || mustJSON(snapshot) != before {
		t.Fatal("accepted unknown deletion")
	}
	if err := removeConfiguredFromSnapshot(&snapshot, "one", "copy", false); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Channels) != 1 || snapshot.Channels[0].Provider != "other-copy" || snapshot.Settings["native"] == nil || snapshot.Settings["copy"] != nil {
		t.Fatal(snapshot)
	}
	if !snapshotExcluded(snapshot.Rules, snapshot.Channels, "one")["native"] {
		t.Fatal("removal revived suppressed base provider")
	}
}

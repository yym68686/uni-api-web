package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSingleChannelSaveCompactsRoutesAtScopeLimit(t *testing.T) {
	const key = "selected-key"
	channel := retainedChannel{Provider: "sub2api-edited", KeyID: key, Base: "https://fixture.test/v1/responses", Key: "fixture", Models: []string{"glm-5.3", "glm-5.3-flash"}}
	snapshot := retainedSnapshot{Version: 2, Channels: []retainedChannel{channel}, Settings: map[string]json.RawMessage{}}
	for i := 0; i < 125; i++ {
		snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: "other-key", Model: fmt.Sprintf("other-%d", i), Order: []string{"first", "last"}, Disabled: []string{"last"}})
	}
	snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: key, Order: []string{"first", channel.Provider, "last"}, Disabled: []string{}})
	catalog := []batchCatalogRow{}
	for _, model := range channel.Models {
		snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: key, Model: model, Order: []string{"first", channel.Provider, "last"}, Disabled: []string{}})
		for _, p := range []string{"first", channel.Provider, "last"} {
			catalog = append(catalog, batchCatalogRow{Provider: p, Model: model})
		}
	}
	snapshot.Rules[126].Disabled = []string{"last"}
	for _, model := range []string{"deepseek-v4.1-flash", "untouched"} {
		for _, p := range []string{"first", "last"} {
			catalog = append(catalog, batchCatalogRow{Provider: p, Model: model})
		}
	}
	var applied retainedSnapshot
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/model-channels":
			writeJSON(w, 200, map[string]any{"data": catalog})
		case "/v1/channel-controls/restore":
			var input struct {
				Snapshot retainedSnapshot `json:"snapshot"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if len(input.Snapshot.Rules) > 128 {
				writeJSON(w, 400, map[string]any{"error": map[string]string{"message": "Invalid retained configuration"}})
				return
			}
			applied = input.Snapshot
			writeJSON(w, 200, map[string]any{"revision": "saved"})
		default:
			t.Error("unexpected request", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	channel.Models = append(channel.Models, "deepseek-v4.1-flash")
	src := controlSource{sourceView: sourceView{Base: server.URL}}
	_, code, err := (&Service{}).applyImportSnapshot(context.Background(), src, snapshot, "before", channel, 1, map[string]int{"glm-5.3": 2, "glm-5.3-flash": 2, "deepseek-v4.1-flash": 1})
	if err != nil || code != 200 {
		t.Fatalf("save failed: %d %v", code, err)
	}
	if len(applied.Rules) > 128 {
		t.Fatal("scope limit exceeded")
	}
	if !reflect.DeepEqual(applied.Rules[:125], snapshot.Rules[:125]) {
		t.Fatal("unrelated key or disabled policy changed")
	}
	preservedDisabled := false
	for _, rule := range applied.Rules {
		if rule.KeyID == key && rule.Model == "glm-5.3" && reflect.DeepEqual(rule.Disabled, []string{"last"}) {
			preservedDisabled = true
		}
	}
	if !preservedDisabled {
		t.Fatal("edited model disabled policy lost")
	}
	for model, want := range map[string][]string{
		"glm-5.3":             {"first", channel.Provider, "last"},
		"glm-5.3-flash":       {"first", channel.Provider, "last"},
		"deepseek-v4.1-flash": {channel.Provider, "first", "last"},
		"untouched":           {"first", "last"},
	} {
		var order []string
		for _, scope := range []string{"", model} {
			for _, rule := range applied.Rules {
				if rule.KeyID == key && rule.Model == scope && len(rule.Order) > 0 {
					order = rule.Order
				}
			}
		}
		if got := projectedRouteOrder(want, order); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s order changed: %v", model, got)
		}
	}
	if len(applied.Channels) != 1 || !reflect.DeepEqual(applied.Channels[0].Models, channel.Models) {
		t.Fatal("selected models not saved")
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Exercise the same atomic restore contract as production with synthetic
// credentials and an isolated local gateway. No model requests are sent.
func TestSingleChannelSaveAtCapacityWithRealGateway(t *testing.T) {
	binary := os.Getenv("TEST_UNI_API_BINARY")
	if binary == "" {
		t.Skip("local uni-api binary required")
	}
	models := []string{"glm-5.3", "glm-5.3-flash", "deepseek-v4.1-flash", "untouched"}
	for i := 0; i < 125; i++ {
		models = append(models, fmt.Sprintf("other-%d", i))
	}
	providers := []any{}
	for _, name := range []string{"first", "last"} {
		providers = append(providers, map[string]any{"provider": name, "base_url": "http://127.0.0.1:1/v1/responses", "api": "fixture-upstream", "model": models})
	}
	config := map[string]any{"providers": providers, "api_keys": []any{
		map[string]any{"api": "fixture-admin", "role": "admin", "model": []string{"all"}},
		map[string]any{"api": "fixture-other", "model": []string{"all"}},
	}}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "api.json")
	if err := os.WriteFile(configPath, []byte(mustJSON(config)), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.Split(listener.Addr().String(), ":")[1]
	listener.Close()
	log, err := os.Create(filepath.Join(dir, "gateway.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(binary)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, log, log
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "PORT=" + port, "DISABLE_DATABASE=true", "UNI_API_CONFIG_PATH=" + configPath, "RUST_RESPONSES_CONFIG_SNAPSHOT_PATH=" + filepath.Join(dir, "snapshot.json"), "UNI_API_SHARED_MEMORY_RESERVATION_PATH=" + filepath.Join(dir, "ledger"), "RUST_REQUEST_SPOOL_DIRECTORY=" + filepath.Join(dir, "spool"), "RUST_REQUEST_SPOOL_DISK_RESERVE_BPS=0", "RUST_REQUEST_SPOOL_INODE_RESERVE_BPS=0", "NO_PROXY=127.0.0.1,localhost"}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			raw, _ := os.ReadFile(log.Name())
			t.Log(string(raw))
		}
	}()
	src := controlSource{sourceView: sourceView{Base: "http://127.0.0.1:" + port}, Key: "fixture-admin"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var state map[string]any
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		state, _, err = subGateway(ctx, src, "GET", "/v1/channel-controls", nil)
		if err == nil {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	key, other := "key-"+tokenHash("fixture-admin"), "key-"+tokenHash("fixture-other")
	channel := retainedChannel{Provider: "sub2api-edited", KeyID: key, Base: "http://127.0.0.1:1/v1/responses", Key: "fixture-upstream", Models: models[:2]}
	snapshot := retainedSnapshot{Version: 2, Channels: []retainedChannel{channel}, Settings: map[string]json.RawMessage{}}
	for i := 0; i < 125; i++ {
		snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: other, Model: fmt.Sprintf("other-%d", i), Order: []string{"last", "first"}, Disabled: []string{"last"}})
	}
	snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: key, Order: []string{"first", channel.Provider, "last"}, Disabled: []string{}})
	for _, model := range channel.Models {
		snapshot.Rules = append(snapshot.Rules, retainedRule{KeyID: key, Model: model, Order: []string{"first", channel.Provider, "last"}, Disabled: []string{}})
	}
	snapshot.Rules[126].Disabled = []string{"last"}
	state, _, err = subGateway(ctx, src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": state["revision"], "snapshot": snapshot})
	if err != nil {
		t.Fatal("baseline restore", err)
	}
	read := func(k string) []batchCatalogRow {
		t.Helper()
		raw, _, e := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {k}, "endpoint": {"all"}, "stream": {"all"}})
		var rows []batchCatalogRow
		if e != nil || decodeMap(raw["data"], &rows) != nil {
			t.Fatal("read catalog", e)
		}
		return rows
	}
	before, beforeOther := read(key), read(other)
	byModel := func(rows []batchCatalogRow) map[string][]batchCatalogRow {
		grouped := map[string][]batchCatalogRow{}
		for _, row := range rows {
			grouped[row.Model] = append(grouped[row.Model], row)
		}
		return grouped
	}
	// Reproduce the rejected 129-scope snapshot without changing serving state.
	var inflated retainedSnapshot
	_ = json.Unmarshal([]byte(mustJSON(snapshot)), &inflated)
	inflated.Channels[0].Models = append(append([]string{}, channel.Models...), "deepseek-v4.1-flash")
	inflated.Rules = append(inflated.Rules, retainedRule{KeyID: key, Model: "deepseek-v4.1-flash", Order: []string{channel.Provider, "first", "last"}, Disabled: []string{}})
	if _, code, e := subGateway(ctx, src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": state["revision"], "snapshot": inflated}); code != 400 || e == nil {
		t.Fatal("did not reproduce capacity rejection", code, e)
	}
	channel.Models = append(append([]string{}, channel.Models...), "deepseek-v4.1-flash")
	applied, code, err := (&Service{}).applyImportSnapshot(ctx, src, snapshot, state["revision"].(string), channel, 1, map[string]int{"glm-5.3": 2, "glm-5.3-flash": 2, "deepseek-v4.1-flash": 1})
	if err != nil || code != 200 {
		t.Fatal("single save", code, err)
	}
	var live retainedLive
	if decodeMap(applied, &live) != nil || len(live.Rules) > 128 {
		t.Fatal("invalid saved scope count")
	}
	if got := read(other); !reflect.DeepEqual(byModel(got), byModel(beforeOther)) {
		t.Fatal("other API key routing changed")
	}
	after := read(key)
	withoutNew := []batchCatalogRow{}
	newOrder := []string{}
	for _, row := range after {
		if row.Model == "deepseek-v4.1-flash" {
			newOrder = append(newOrder, row.Provider)
		}
		if row.Provider != channel.Provider || row.Model != "deepseek-v4.1-flash" {
			withoutNew = append(withoutNew, row)
		}
	}
	if !reflect.DeepEqual(byModel(before), byModel(withoutNew)) {
		for model, want := range byModel(before) {
			if got := byModel(withoutNew)[model]; !reflect.DeepEqual(got, want) {
				t.Errorf("existing %s order changed: before=%v after=%v", model, want, got)
			}
		}
		t.Fatal("existing model catalog changed")
	}
	if !reflect.DeepEqual(newOrder, []string{channel.Provider, "first", "last"}) {
		t.Fatal("new model position not saved", newOrder)
	}
	for _, old := range snapshot.Rules {
		if len(old.Disabled) == 0 {
			continue
		}
		found := false
		for _, rule := range live.Rules {
			if rule.KeyID == old.KeyID && rule.Model == old.Model && reflect.DeepEqual(rule.Disabled, old.Disabled) {
				found = true
			}
		}
		if !found {
			t.Fatal("disabled policy lost")
		}
	}
	t.Logf("old save rejected at 129 scopes; fixed save accepted with %d scopes", len(live.Rules))
}

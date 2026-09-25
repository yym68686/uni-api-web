package main

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestLargeBatchRestoresRealGatewayWithoutExceedingScopeBudget(t *testing.T) {
	binary := os.Getenv("TEST_UNI_API_BINARY")
	if binary == "" {
		t.Skip("local uni-api binary required")
	}
	f := loadBatchFixture(t, "digitalocean")
	models := map[string]map[string]string{}
	add := func(p, m, up string) {
		if models[p] == nil {
			models[p] = map[string]string{}
		}
		models[p][m] = up
	}
	for _, rows := range f.Catalogs {
		for _, r := range rows {
			add(r.Provider, r.Model, r.Upstream)
		}
	}
	keys := map[string]bool{}
	for _, r := range f.Snapshot.Rules {
		keys[r.KeyID] = true
		for _, p := range append(append([]string{}, r.Order...), r.Disabled...) {
			if r.Model != "" {
				add(p, r.Model, r.Model)
			} else if models[p] == nil {
				add(p, "fixture-placeholder", "fixture-placeholder")
			}
		}
	}
	providers := []any{}
	names := []string{}
	for p := range models {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		doc := map[string]any{"provider": p, "engine": "gpt", "base_url": "http://127.0.0.1:1/v1/responses", "api": "fixture", "model": importModelDefinition(nil, models[p])}
		providers = append(providers, doc)
	}
	keyNames := []string{}
	for k := range keys {
		keyNames = append(keyNames, k)
	}
	sort.Strings(keyNames)
	apiKeys := []any{}
	for _, k := range keyNames {
		apiKeys = append(apiKeys, map[string]any{"api": "fixture-" + k, "role": "admin", "model": []string{"all"}})
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "api.json")
	if err := os.WriteFile(path, []byte(mustJSON(map[string]any{"providers": providers, "api_keys": apiKeys})), 0600); err != nil {
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
	cmd.Dir = dir
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "PORT=" + port, "DISABLE_DATABASE=true", "UNI_API_CONFIG_PATH=" + path, "RUST_RESPONSES_CONFIG_SNAPSHOT_PATH=" + filepath.Join(dir, "snapshot.json"), "UNI_API_SHARED_MEMORY_RESERVATION_PATH=" + filepath.Join(dir, "ledger"), "RUST_REQUEST_SPOOL_DIRECTORY=" + filepath.Join(dir, "spool"), "RUST_REQUEST_SPOOL_DISK_RESERVE_BPS=0", "RUST_REQUEST_SPOOL_INODE_RESERVE_BPS=0", "NO_PROXY=127.0.0.1,localhost"}
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
	src := controlSource{sourceView: sourceView{Base: "http://127.0.0.1:" + port}, Key: "fixture-" + keyNames[0]}
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
	id := func(k string) string { return "key-" + tokenHash("fixture-"+k) }
	for i := range f.Snapshot.Rules {
		f.Snapshot.Rules[i].KeyID = id(f.Snapshot.Rules[i].KeyID)
	}
	state, _, err = subGateway(ctx, src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": state["revision"], "snapshot": f.Snapshot})
	if err != nil {
		t.Fatal("baseline", err)
	}
	f.Catalogs = map[string][]batchCatalogRow{}
	f.Documents = map[string]map[string]any{}
	before := map[string][]batchCatalogRow{}
	read := func(key string) []batchCatalogRow {
		t.Helper()
		raw, _, e := fetchSource(ctx, src, "/v1/model-channels", url.Values{"api_key_id": {key}, "endpoint": {"all"}, "stream": {"all"}})
		var rows []batchCatalogRow
		if e != nil || decodeMap(raw["data"], &rows) != nil {
			t.Fatal(e)
		}
		return rows
	}
	for i := range f.Input.Targets {
		target := &f.Input.Targets[i]
		target.Key = id(target.Key)
		target.Current = map[string]string{}
		target.Positions = map[string]int{}
		excluded := snapshotExcluded(f.Snapshot.Rules, f.Snapshot.Channels, target.Key)
		counts := map[string]int{}
		for _, r := range read(target.Key) {
			if excluded[r.Provider] {
				continue
			}
			before[target.Key] = append(before[target.Key], r)
			f.Catalogs[target.Key] = append(f.Catalogs[target.Key], r)
			counts[r.Model]++
			if r.Provider == target.Provider {
				target.Current[r.Model] = r.Upstream
				target.Positions[r.Model] = counts[r.Model]
			}
		}
		target.Models = map[string]string{}
		for m, up := range target.Current {
			target.Models[m] = up
		}
		target.Models["gemini-3.8-flash"] = "gemini-3.8-flash"
		target.Positions["gemini-3.8-flash"] = 1
		f.Documents[target.Key+"\n"+target.Provider] = map[string]any{"provider": "aiwave", "engine": "gpt", "base_url": "http://127.0.0.1:1/v1/responses", "api": "fixture"}
	}
	if _, err = buildBatchSnapshot(&f.Snapshot, f.Input, f.Catalogs, f.Documents); err != nil {
		t.Fatal(err)
	}
	// Materializing every model again reproduces the original 128-scope failure.
	var inflated retainedSnapshot
	_ = json.Unmarshal([]byte(mustJSON(f.Snapshot)), &inflated)
	for _, target := range f.Input.Targets {
		rows := []routeMove{}
		moves := []routeMove{}
		for _, r := range f.Catalogs[target.Key] {
			rows = append(rows, routeMove{Provider: r.Provider, Model: r.Model})
		}
		for m, pos := range target.Positions {
			moves = append(moves, routeMove{Provider: configuredImportName("aiwave", target.Key), Model: m, Position: pos})
		}
		if err = applyRouteMoves(&inflated, target.Key, rows, moves); err != nil {
			t.Fatal(err)
		}
	}
	if len(inflated.Rules) <= 128 {
		t.Fatal("fixture did not reproduce old limit")
	}
	if _, code, e := subGateway(ctx, src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": state["revision"], "snapshot": inflated}); code != 400 || e == nil || !strings.Contains(e.Error(), "数量上限") {
		t.Fatal(code, e)
	}
	state, _, err = subGateway(ctx, src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": state["revision"], "snapshot": f.Snapshot})
	if err != nil {
		t.Fatal("compacted restore", err)
	}
	for _, target := range f.Input.Targets {
		rows := read(target.Key)
		copy := configuredImportName("aiwave", target.Key)
		actual := map[string][]string{}
		excluded := snapshotExcluded(f.Snapshot.Rules, f.Snapshot.Channels, target.Key)
		for _, r := range rows {
			if excluded[r.Provider] {
				continue
			}
			actual[r.Model] = append(actual[r.Model], r.Provider)
		}
		wanted := map[string][]string{}
		for _, r := range before[target.Key] {
			p := r.Provider
			if p == "aiwave" {
				p = copy
			}
			wanted[r.Model] = append(wanted[r.Model], p)
		}
		wanted["gemini-3.8-flash"] = append([]string{copy}, wanted["gemini-3.8-flash"]...)
		if !reflect.DeepEqual(actual, wanted) {
			for m, order := range wanted {
				if !reflect.DeepEqual(actual[m], order) {
					t.Logf("%s: want %v got %v", m, order, actual[m])
				}
			}
			t.Fatalf("routing order changed for %s", target.Key)
		}
	}
	if _, _, err = subGateway(ctx, src, "POST", "/v1/channel-controls/restore", map[string]any{"revision": state["revision"], "snapshot": f.Snapshot}); err != nil {
		t.Fatal("subsequent recovery", err)
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestManagedConfigurationVersionsAuthAndAtomicPublication(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	id := "managed-" + randomID()[:8]
	defer func() {
		for _, table := range []string{"console_managed_config_state", "console_managed_config_operations", "console_managed_config_versions", "console_sources"} {
			key := "source_id"
			if table == "console_sources" {
				key = "id"
			}
			if _, err := store.db.Exec("DELETE FROM "+table+" WHERE "+key+"=$1", id); err != nil {
				t.Error(err)
			}
		}
	}()
	src := controlSource{sourceView: sourceView{ID: id, Name: "test", Base: "https://gateway.example"}, Key: "observer-key", ConfigKey: "test-admin-key"}
	if _, err = store.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	svc := &Service{control: store}
	get := func(key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/runtime-config/"+id, nil)
		r.SetPathValue("id", id)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		svc.runtimeConfiguration(w, r)
		return w
	}
	if w := get("observer-key"); w.Code != 401 {
		t.Fatal("observation key must not read a managed bundle")
	}
	if w := get(src.ConfigKey); w.Code != 200 || strings.Contains(w.Body.String(), "bundle") {
		t.Fatal("uninitialized source must not send a configuration")
	}
	bundle := json.RawMessage(`{"schema":1,"base":{"api_keys":["secret-for-testing"]},"controls":{"version":2}}`)
	save := func(in managedAction) managedCommit {
		result, err := store.saveManagedConfig(ctx, id, in)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	draft := save(managedAction{Action: "draft", Bundle: bundle}).Revision
	if w := get(src.ConfigKey); strings.Contains(w.Body.String(), "secret-for-testing") {
		t.Fatal("unrequested draft must not be served")
	}
	save(managedAction{Action: "publish", Draft: draft})
	if w := get(src.ConfigKey); !strings.Contains(w.Body.String(), "pending") {
		t.Fatal("missing pending draft")
	}
	save(managedAction{Action: "reject", Draft: draft})
	if w := get(src.ConfigKey); strings.Contains(w.Body.String(), "secret-for-testing") {
		t.Fatal("rejected draft must not replace active configuration")
	}
	save(managedAction{Action: "publish", Draft: draft})
	save(managedAction{Action: "promote", Draft: draft})
	credits := 10.0
	intent := managedAction{Action: "commit", Expected: draft, Bundle: bundle, Operation: "credit-transaction", Digest: strings.Repeat("a", 64), Credits: &credits}
	var wg sync.WaitGroup
	results := make(chan managedCommit, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := store.saveManagedConfig(ctx, id, intent)
			if err != nil {
				t.Error(err)
			}
			results <- result
		}()
	}
	wg.Wait()
	first, second := <-results, <-results
	if first.Revision <= draft || first.Revision != second.Revision || first.Duplicate == second.Duplicate {
		t.Fatal("concurrent retry did not resolve to a single committed version")
	}
	intent.Operation, intent.Digest, intent.Credits = "", "", nil
	if _, err := store.saveManagedConfig(ctx, id, intent); !errors.Is(err, errManagedConfigConflict) {
		t.Fatal("stale write was not fenced")
	}
	var ciphertext string
	if err := store.db.QueryRow(`SELECT encrypted_bundle FROM console_managed_config_versions WHERE source_id=$1 AND revision=$2`, id, first.Revision).Scan(&ciphertext); err != nil || strings.Contains(ciphertext, "secret-for-testing") {
		t.Fatal("bundle must be encrypted on disk", err)
	}
}

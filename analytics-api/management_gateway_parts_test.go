package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func managementGatewayPartsFixture(source controlSource, revision, config string, controls map[string]any) managementGatewayFacts {
	if controls == nil {
		controls = map[string]any{"revision": revision, "config_revision": config, "rules": []any{}, "temporary_channels": []any{}, "channel_settings": true, "temporary_channel_restore": true, "automatic_engine": true, "temporary_channel_management": true}
	}
	row := managementModel{Provider: "native", Model: "gpt", Upstream: "upstream", Engine: "gpt"}
	return managementGatewayFacts{Source: source, AtomicSnapshot: true, Revision: revision, ConfigRevision: config, Controls: controls,
		Keys:    []managementGatewayKey{{ID: "key-1", Prefix: "masked", Position: 1}},
		Catalog: []managementModel{row}, Routes: map[string][]managementModel{"key-1": {row}},
		Providers: []configuredProvider{{Provider: "native", Base: "https://private-host", API: []any{"private-upstream"}}},
		Export:    map[string]any{"revision": revision, "channel_settings": map[string]any{}, "temporary_definitions": map[string]any{}}}
}

func TestManagementGatewayPartsStoreBoundAndReplace(t *testing.T) {
	var store managementGatewayPartsStore
	for _, entry := range []managementGatewayPartsEntry{
		{owner: "a", identity: "one", revision: "1", bytes: 3, raw: []byte("one")},
		{owner: "a", identity: "one", revision: "2", bytes: 3, raw: []byte("two")},
		{owner: "b", identity: "one", revision: "1", bytes: 3, raw: []byte("tri")},
		{owner: "a", identity: "two", revision: "1", bytes: 3, raw: []byte("for")},
	} {
		store.put(entry)
	}
	if store.bytes != 6 || len(store.entries) != 2 {
		t.Fatal("entry or byte ceiling changed", len(store.entries), store.bytes)
	}
	if _, ok := store.get("a", "one"); ok {
		t.Fatal("FIFO retained the oldest owner/version")
	}
	if got, ok := store.get("a", "two"); !ok || string(got.raw) != "for" {
		t.Fatal("newest private representation missing")
	}
	store.put(managementGatewayPartsEntry{owner: "large", identity: "x", bytes: managementGatewayPartsBytes + 1})
	if store.bytes != 6 || len(store.entries) != 2 {
		t.Fatal("oversize representation evicted useful entries")
	}
}

func TestManagementGatewayPartsReuseProvesFreshAuthorizationAndVersion(t *testing.T) {
	var revision, config atomic.Value
	revision.Store("r1")
	config.Store("c1")
	var snapshots, controls, revisionReads, observerRevisionReads atomic.Int32
	var receipt atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("gateway reuse issued a mutation", r.Method)
		}
		switch r.URL.Path {
		case "/v1/channel-settings/management-snapshot":
			snapshots.Add(1)
			row := managementModel{Provider: "native", Model: "gpt", Upstream: "upstream", Engine: "gpt"}
			writeJSON(w, 200, map[string]any{"format": "management-snapshot-v1", "revision": revision.Load(), "config_revision": config.Load(),
				"controls": map[string]any{"revision": revision.Load(), "config_revision": config.Load(), "temporary_channels": []any{}, "rules": []any{}, "channel_settings": true, "temporary_channel_restore": true, "automatic_engine": true, "temporary_channel_management": true},
				"keys":     []managementGatewayKey{{ID: "key-1", Prefix: "masked", Position: 1}}, "catalog": []managementModel{row}, "routes": map[string][]managementModel{"key-1": {row}},
				"providers": []configuredProvider{{Provider: "native", Base: "https://private-host", API: []any{"private-upstream"}}}, "export": map[string]any{"revision": revision.Load(), "channel_settings": map[string]any{}, "temporary_definitions": map[string]any{}}})
		case "/v1/channel-settings/management-revision":
			revisionReads.Add(1)
			if r.Header.Get("Authorization") != "Bearer admin" {
				http.Error(w, "denied", 403)
				return
			}
			writeJSON(w, 200, map[string]any{"format": "management-revision-v1", "revision": revision.Load(), "config_revision": config.Load()})
		case "/v1/channel-controls":
			controls.Add(1)
			if r.Header.Get("Authorization") != "Bearer observer" {
				http.Error(w, "denied", 403)
				return
			}
			writeJSON(w, 200, map[string]any{"revision": revision.Load(), "config_revision": config.Load(), "temporary_channels": []any{}, "rules": []any{}, "channel_settings": true, "temporary_channel_restore": true, "automatic_engine": true, "temporary_channel_management": true, "bootstrap_restore": map[string]any{"snapshot_id": fmt.Sprint(receipt.Load()), "unchanged": true}})
		case "/v1/channel-controls/revision":
			observerRevisionReads.Add(1)
			writeJSON(w, 200, map[string]any{"format": "management-revision-v1", "revision": revision.Load(), "config_revision": config.Load()})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	source := controlSource{sourceView: sourceView{ID: "source", Name: "Source", Base: server.URL}, Key: "observer", ConfigKey: "admin"}
	var store managementGatewayPartsStore
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "parts-test-123")
	ctx = context.WithValue(ctx, managementGatewayPartsScopeKey{}, &managementGatewayPartsScope{store: &store, owner: "owner-a"})
	first, err := readManagementGatewayWithParts(ctx, source)
	if err != nil || first.Revision != "r1" || snapshots.Load() != 1 {
		t.Fatal("initial live snapshot failed", err, snapshots.Load())
	}
	rememberManagementGatewayParts(ctx, []managementGatewayFacts{first})
	// The controls reply changes at unchanged revisions. Reuse must include the
	// complete CURRENT value, not preserve stale bootstrap_restore metadata.
	first.Controls["bootstrap_restore"] = map[string]any{"snapshot_id": "mutated-caller"}
	receipt.Store(1)
	second, err := readManagementGatewayWithParts(ctx, source)
	if err != nil || snapshots.Load() != 1 || second.Controls["bootstrap_restore"] == nil || mustJSON(second.Controls["bootstrap_restore"]) == mustJSON(first.Controls["bootstrap_restore"]) {
		t.Fatal("private parts or live controls were not independently refreshed", err, snapshots.Load())
	}
	if second.Keys[0].ID != "key-1" || second.Providers[0].API == nil {
		t.Fatal("current immutable catalog/config parts were lost")
	}
	// Compare every retained field with the original freshly decoded snapshot.
	if !reflect.DeepEqual(second.Keys, first.Keys) || !reflect.DeepEqual(second.Catalog, first.Catalog) || !reflect.DeepEqual(second.Routes, first.Routes) || !reflect.DeepEqual(second.Providers, first.Providers) || !reflect.DeepEqual(second.Export, first.Export) {
		t.Fatal("reused immutable facts differ from full current snapshot")
	}
	if observerRevisionReads.Load() != 0 || revisionReads.Load() != 1 || controls.Load() != 2 {
		t.Fatal("did not freshly authorize both identities and versions on each reuse", revisionReads.Load(), controls.Load(), observerRevisionReads.Load())
	}
	second.Keys[0].ID = "caller-key"
	second.Catalog[0].Model = "caller-model"
	second.Routes["key-1"][0].Model = "caller-route"
	second.Providers[0].API.([]any)[0] = "caller-secret"
	second.Export["channel_settings"].(map[string]any)["caller"] = true
	independent, err := readManagementGatewayWithParts(ctx, source)
	if err != nil || independent.Keys[0].ID != "key-1" || independent.Catalog[0].Model != "gpt" || independent.Routes["key-1"][0].Model != "gpt" || independent.Providers[0].API.([]any)[0] != "private-upstream" || len(independent.Export["channel_settings"].(map[string]any)) != 0 {
		t.Fatal("one request changed another request's retained evidence", err)
	}
	// A same-process setting import mutates the controls sequence; a changed
	// config revision invalidates all versioned facts and requires full snapshot.
	revision.Store("r2")
	config.Store("c2")
	third, err := readManagementGatewayWithParts(ctx, source)
	if err != nil || third.Revision != "r2" || snapshots.Load() != 2 {
		t.Fatal("new revision did not force a complete snapshot", err, snapshots.Load())
	}
	rememberManagementGatewayParts(ctx, []managementGatewayFacts{third})
	// Simulated process restart changes the instance component of controls
	// revision, so a restart can never validate the previous private entry.
	revision.Store("another-process:0:c2")
	fourth, err := readManagementGatewayWithParts(ctx, source)
	if err != nil || fourth.Revision == third.Revision || snapshots.Load() != 3 {
		t.Fatal("restart identity did not invalidate prior parts", err, snapshots.Load())
	}
}

func TestManagementGatewayPartsNeverCacheForWrongScopeOrUntrustedSource(t *testing.T) {
	source := controlSource{sourceView: sourceView{ID: "source", Base: "http://gateway"}, Key: "observer", ConfigKey: "admin"}
	for _, mode := range []string{"unscoped", "nil-scope", "nil-store", "empty-owner", "canceled", "legacy", "config-error", "missing-revision", "mixed-export", "nil-providers", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			var store managementGatewayPartsStore
			facts := managementGatewayPartsFixture(source, "r1", "c1", nil)
			scope := &managementGatewayPartsScope{store: &store, owner: "owner"}
			if mode == "nil-scope" {
				scope = nil
			}
			if mode == "nil-store" {
				scope.store = nil
			}
			if mode == "empty-owner" {
				scope.owner = ""
			}
			ctx := context.Background()
			if mode != "unscoped" {
				ctx = context.WithValue(ctx, managementGatewayPartsScopeKey{}, scope)
			}
			if mode == "canceled" {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			switch mode {
			case "legacy":
				facts.AtomicSnapshot = false
			case "config-error":
				facts.Source.configKeyError = errors.New("private config identity failure")
			case "missing-revision":
				facts.Revision = ""
			case "mixed-export":
				facts.Export["revision"] = "other"
			case "nil-providers":
				facts.Providers = nil
			case "oversize":
				facts.Providers[0].API = strings.Repeat("private", managementGatewayPartsBytes/7+1)
			}
			rememberManagementGatewayParts(ctx, []managementGatewayFacts{facts})
			if len(store.entries) != 0 || store.bytes != 0 {
				t.Fatal("incomplete or out-of-scope facts were retained", mode)
			}
		})
	}
}

func managementGatewayPartsDocument(f managementGatewayFacts) map[string]any {
	return map[string]any{"format": "management-snapshot-v1", "revision": f.Revision, "config_revision": f.ConfigRevision, "controls": f.Controls, "keys": f.Keys, "catalog": f.Catalog, "routes": f.Routes, "providers": f.Providers, "export": f.Export}
}

func TestManagementGatewayPartsMissingRevisionReadsFreshSnapshot(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(fmt.Sprint(denied), func(t *testing.T) {
			var snapshots, revisions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fixture := managementGatewayPartsFixture(controlSource{}, "new", "c2", nil)
				switch r.URL.Path {
				case "/v1/channel-settings/management-revision":
					revisions.Add(1)
					http.NotFound(w, r)
				case "/v1/channel-controls":
					if denied {
						http.Error(w, "observation permission revoked", 403)
						return
					}
					writeJSON(w, 200, fixture.Controls)
				case "/v1/channel-settings/management-snapshot":
					snapshots.Add(1)
					writeJSON(w, 200, managementGatewayPartsDocument(fixture))
				default:
					t.Error("unexpected negotiation request", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			source := controlSource{sourceView: sourceView{ID: "source", Base: server.URL}, Key: "observer", ConfigKey: "admin"}
			var store managementGatewayPartsStore
			ctx := context.WithValue(context.Background(), managementGatewayPartsScopeKey{}, &managementGatewayPartsScope{store: &store, owner: "owner"})
			rememberManagementGatewayParts(ctx, []managementGatewayFacts{managementGatewayPartsFixture(source, "old", "c1", nil)})
			got, err := readManagementGatewayWithParts(ctx, source)
			if revisions.Load() != 1 {
				t.Fatal("negotiation retried an unavailable revision endpoint")
			}
			if denied {
				if err == nil || got.Catalog != nil || snapshots.Load() != 0 {
					t.Fatal("observation failure triggered a stale fallback", err, snapshots.Load())
				}
			} else if err != nil || got.Revision != "new" || got.ConfigRevision != "c2" || snapshots.Load() != 1 {
				t.Fatal("missing revision did not negotiate a fresh snapshot", err, got.Revision, snapshots.Load())
			}
		})
	}
}

func TestManagementGatewayPartsRejectsLiveErrorsWithoutStaleFallback(t *testing.T) {
	for _, mode := range []string{"admin-denied", "observer-denied", "admin-unavailable", "observer-unavailable", "admin-transport", "observer-transport", "format", "empty-revision", "mixed-revision", "mixed-config", "bad-json", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			var snapshots atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("cache issued a mutation")
				}
				switch r.URL.Path {
				case "/v1/channel-settings/management-revision":
					if r.Header.Get("Authorization") != "Bearer admin" {
						t.Error("administrator identity changed")
					}
					switch mode {
					case "admin-denied":
						http.Error(w, "private denial", 403)
						return
					case "admin-unavailable":
						http.Error(w, "private outage", 503)
						return
					case "admin-transport":
						panic(http.ErrAbortHandler)
					case "bad-json":
						fmt.Fprint(w, `{"private":`)
						return
					}
					format, revision := "management-revision-v1", "r1"
					if mode == "format" {
						format = "invalid"
					}
					if mode == "empty-revision" {
						revision = ""
					}
					writeJSON(w, 200, map[string]any{"format": format, "revision": revision, "config_revision": "c1"})
				case "/v1/channel-controls":
					if r.Header.Get("Authorization") != "Bearer observer" {
						t.Error("observation identity changed")
					}
					switch mode {
					case "observer-denied":
						http.Error(w, "private denial", 403)
						return
					case "observer-unavailable":
						http.Error(w, "private outage", 503)
						return
					case "observer-transport":
						panic(http.ErrAbortHandler)
					}
					revision, config := "r1", "c1"
					if mode == "mixed-revision" {
						revision = "r2"
					}
					if mode == "mixed-config" {
						config = "c2"
					}
					writeJSON(w, 200, map[string]any{"revision": revision, "config_revision": config})
				case "/v1/channel-settings/management-snapshot":
					snapshots.Add(1)
					http.Error(w, "must not retry", 503)
				default:
					t.Error("unexpected gateway request", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			source := controlSource{sourceView: sourceView{ID: "source", Base: server.URL}, Key: "observer", ConfigKey: "admin"}
			var store managementGatewayPartsStore
			ctx := context.WithValue(context.Background(), managementGatewayPartsScopeKey{}, &managementGatewayPartsScope{store: &store, owner: "owner"})
			rememberManagementGatewayParts(ctx, []managementGatewayFacts{managementGatewayPartsFixture(source, "r1", "c1", nil)})
			if len(store.entries) != 1 {
				t.Fatal("fixture did not seed exact prior version")
			}
			if mode == "canceled" {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			got, err := readManagementGatewayWithParts(ctx, source)
			if err == nil || got.Catalog != nil || snapshots.Load() != 0 {
				t.Fatal("failed live authorization/version returned cached facts or retried", mode, err, snapshots.Load())
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation cause was lost", err)
			}
		})
	}
}

func TestManagementGatewayPartsOwnerSourceAndOrdinaryReadIsolation(t *testing.T) {
	for _, mode := range []string{"owner", "source-id", "name", "base", "observer-key", "admin-key", "unscoped", "same-key", "no-config-key"} {
		t.Run(mode, func(t *testing.T) {
			var snapshots, revisions, controls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/alternate")
				fixture := managementGatewayPartsFixture(controlSource{}, "r1", "c1", nil)
				switch path {
				case "/v1/channel-settings/management-snapshot":
					snapshots.Add(1)
					writeJSON(w, 200, managementGatewayPartsDocument(fixture))
				case "/v1/channel-settings/management-revision":
					revisions.Add(1)
					writeJSON(w, 200, map[string]any{"format": "management-revision-v1", "revision": "r1", "config_revision": "c1"})
				case "/v1/channel-controls":
					controls.Add(1)
					writeJSON(w, 200, fixture.Controls)
				default:
					t.Error("unexpected endpoint", path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			source := controlSource{sourceView: sourceView{ID: "source", Name: "name", Base: server.URL}, Key: "observer", ConfigKey: "admin"}
			if mode == "same-key" {
				source.ConfigKey = source.Key
			}
			if mode == "no-config-key" {
				source.ConfigKey = ""
			}
			var store managementGatewayPartsStore
			scope := &managementGatewayPartsScope{store: &store, owner: "owner-a"}
			ctx := context.WithValue(context.Background(), managementGatewayPartsScopeKey{}, scope)
			rememberManagementGatewayParts(ctx, []managementGatewayFacts{managementGatewayPartsFixture(source, "r1", "c1", nil)})
			switch mode {
			case "owner":
				scope = &managementGatewayPartsScope{store: &store, owner: "owner-b"}
				ctx = context.WithValue(ctx, managementGatewayPartsScopeKey{}, scope)
			case "source-id":
				source.ID = "new-id"
			case "name":
				source.Name = "new-name"
			case "base":
				source.Base += "/alternate"
			case "observer-key":
				source.Key = "new-observer"
			case "admin-key":
				source.ConfigKey = "new-admin"
			case "unscoped":
				ctx = context.Background()
			}
			got, err := readManagementGatewayWithParts(ctx, source)
			if err != nil || got.Revision != "r1" || got.Source != source {
				t.Fatal("source or owner read failed", err)
			}
			hit := mode == "same-key" || mode == "no-config-key"
			if (snapshots.Load() == 0) != hit || (revisions.Load() == 1) != hit || controls.Load() != 1 {
				t.Fatal("owner/credential isolation or fresh single-identity authorization changed", snapshots.Load(), revisions.Load(), controls.Load())
			}
		})
	}
}

func TestManagementGatewayPartsProjectionCoversAllStableFactFields(t *testing.T) {
	excluded := map[string]bool{"Source": true, "Controls": true, "AtomicSnapshot": true}
	parts := reflect.TypeOf(managementGatewayParts{})
	facts := reflect.TypeOf(managementGatewayFacts{})
	for i := 0; i < facts.NumField(); i++ {
		field := facts.Field(i)
		if excluded[field.Name] {
			continue
		}
		projected, ok := parts.FieldByName(field.Name)
		if !ok || projected.Type != field.Type {
			t.Fatal("new stable fact field omitted from private projection", field.Name)
		}
	}
	source := controlSource{sourceView: sourceView{ID: "private-source"}, Key: "private-key"}
	f := managementGatewayPartsFixture(source, "r1", "c1", nil)
	f.Controls["bootstrap_restore"] = map[string]any{"snapshot_id": "private-volatile"}
	var store managementGatewayPartsStore
	ctx := context.WithValue(context.Background(), managementGatewayPartsScopeKey{}, &managementGatewayPartsScope{store: &store, owner: "private-owner"})
	rememberManagementGatewayParts(ctx, []managementGatewayFacts{f})
	if len(store.entries) != 1 {
		t.Fatal("fixture did not enter bounded private store")
	}
	entry := store.entries[0]
	var raw map[string]json.RawMessage
	if json.Unmarshal(entry.raw, &raw) != nil || raw["Controls"] != nil || raw["Source"] != nil || strings.Contains(string(entry.raw), "private-volatile") || strings.Contains(string(entry.raw), "private-owner") || strings.Contains(string(entry.raw), "private-key") {
		t.Fatal("volatile controls or source identity was retained in private projection")
	}
	want := len(entry.raw) + len(entry.owner) + len(entry.identity) + len(entry.revision) + len(entry.config)
	if entry.bytes != want || store.bytes != want {
		t.Fatal("retained byte budget omitted a private representation", entry.bytes, want)
	}
}

func TestManagementGatewayPartsConcurrentAccessIsSafe(t *testing.T) {
	var store managementGatewayPartsStore
	var group sync.WaitGroup
	for i := 0; i < 24; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			for j := 0; j < 100; j++ {
				identity := fmt.Sprintf("identity-%d", i%4)
				store.put(managementGatewayPartsEntry{owner: "owner", identity: identity, revision: fmt.Sprint(j), config: "config", raw: []byte(strings.Repeat("x", 100)), bytes: 100})
				if entry, ok := store.get("owner", identity); ok {
					if len(entry.raw) != 100 {
						t.Error("partial cache entry")
					}
				}
			}
		}(i)
	}
	group.Wait()
	if store.bytes > managementGatewayPartsBytes || len(store.entries) > 2 {
		t.Fatal("shared admission exceeded its fixed budget")
	}
}

func TestManagementGatewayPartsAdmissionWaitsForEveryOriginalFence(t *testing.T) {
	for _, mode := range []string{"stable", "unscoped", "database-error", "changed-gateway", "revoked-at-fence", "source-disabled", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			s := managementBindingTestService(t)
			var afterRead atomic.Bool
			var snapshots atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fixture := managementGatewayPartsFixture(controlSource{}, "r1", "c1", nil)
				switch r.URL.Path {
				case "/v1/channel-settings/management-snapshot":
					snapshots.Add(1)
					writeJSON(w, 200, managementGatewayPartsDocument(fixture))
				case "/v1/channel-controls":
					writeJSON(w, 200, fixture.Controls)
				case "/v1/channel-settings/management-revision", "/v1/channel-controls/revision":
					if afterRead.Load() && mode == "revoked-at-fence" {
						http.Error(w, "private denial", 403)
						return
					}
					revision := "r1"
					if afterRead.Load() && mode == "changed-gateway" {
						revision = "r2"
					}
					writeJSON(w, 200, map[string]any{"format": "management-revision-v1", "revision": revision, "config_revision": "c1"})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			source := controlSource{sourceView: sourceView{ID: "source", Name: "Source", Base: server.URL}, Key: "observer", ConfigKey: "admin"}
			if _, err := s.control.saveSource(context.Background(), source, false); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode != "unscoped" {
				ctx = context.WithValue(ctx, managementGatewayPartsScopeKey{}, &managementGatewayPartsScope{store: &s.gatewayParts, owner: "owner"})
			}
			read := func(tx *sql.Tx, facts []managementGatewayFacts) (managementDatabaseView, error) {
				var readOnly string
				if err := tx.QueryRowContext(ctx, "SHOW transaction_read_only").Scan(&readOnly); err != nil || readOnly != "on" {
					t.Error("snapshot lost its read-only transaction", err, readOnly)
				}
				if len(facts) != 1 || facts[0].Revision != "r1" {
					t.Error("incomplete gateway facts")
				}
				afterRead.Store(true)
				switch mode {
				case "database-error":
					return managementDatabaseView{}, errors.New("isolated database failure")
				case "source-disabled":
					if _, err := s.control.db.Exec(`UPDATE console_sources SET enabled=false WHERE id='source'`); err != nil {
						t.Error(err)
					}
				case "canceled":
					cancel()
				}
				return managementDatabaseView{}, nil
			}
			_, err := s.withManagementSnapshot(ctx, read)
			good := mode == "stable" || mode == "unscoped"
			if (err == nil) != good {
				t.Fatal("original failure/fence behavior changed", mode, err)
			}
			if (len(s.gatewayParts.entries) == 1) != (mode == "stable") {
				t.Fatal("failed or unscoped snapshot seeded private parts", mode, len(s.gatewayParts.entries))
			}
			if good {
				if _, err := s.withManagementSnapshot(ctx, read); err != nil {
					t.Fatal(err)
				}
				want := int32(1)
				if mode == "unscoped" {
					want = 2
				}
				if snapshots.Load() != want {
					t.Fatal("admitted parts were not scoped to ordinary reads", mode, snapshots.Load())
				}
			}
		})
	}
}

func TestManagementGatewayPartsPageAndSelectionHaveIdenticalEvidence(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	var snapshots atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture := managementGatewayPartsFixture(controlSource{}, "r1", "c1", nil)
		switch r.URL.Path {
		case "/v1/channel-settings/management-snapshot":
			snapshots.Add(1)
			writeJSON(w, 200, managementGatewayPartsDocument(fixture))
		case "/v1/channel-controls":
			fixture.Controls["bootstrap_restore"] = map[string]any{"snapshot_id": "new-receipt"}
			writeJSON(w, 200, fixture.Controls)
		case "/v1/channel-settings/management-revision", "/v1/channel-controls/revision":
			writeJSON(w, 200, map[string]any{"format": "management-revision-v1", "revision": "r1", "config_revision": "c1"})
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if _, err := s.control.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: "source", Name: "Source", Base: server.URL}, Key: "observer", ConfigKey: "admin"}, false); err != nil {
		t.Fatal(err)
	}
	filter := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	ctx := context.Background()
	first, err := s.readManagementQuerySnapshot(ctx, "owner", filter, "", false)
	if err != nil || len(s.gatewayParts.entries) != 1 || snapshots.Load() != 1 {
		t.Fatal("initial complete query failed", err, len(s.gatewayParts.entries), snapshots.Load())
	}
	second, err := s.readManagementQuerySnapshot(ctx, "owner", filter, "", false)
	if err != nil || snapshots.Load() != 1 {
		t.Fatal("ordinary different computation reread the full gateway document", err, snapshots.Load())
	}
	full, err := s.readManagementQuerySnapshot(ctx, "owner", filter, "", true)
	if err != nil || snapshots.Load() != 2 {
		t.Fatal("complete selection reused private page snapshot instead of reading live", err, snapshots.Load())
	}
	for _, got := range []managementQuerySnapshot{first, second} {
		if got.Page.Revision != full.Page.Revision || !reflect.DeepEqual(got.Page.Rows, full.Page.Rows) || !reflect.DeepEqual(managementContextForPage(got.View, got.Page, filter.Model), managementContextForPage(full.View, full.Page, filter.Model)) {
			t.Fatal("gateway parts changed rows, context, filter revision or selection evidence")
		}
	}
	token := encodeManagementCursor(s.control.key, "owner", managementPageCursor{Version: 1, Offset: 25, Revision: second.Page.Revision, Filter: tokenHash(mustJSON(filter))})
	next, err := s.readManagementQuerySnapshot(ctx, "owner", filter, token, false)
	fullNext, fullErr := s.readManagementQuerySnapshot(ctx, "owner", filter, token, true)
	if err != nil || fullErr != nil || next.Page.Offset != 25 || next.Page.Revision != fullNext.Page.Revision || !reflect.DeepEqual(next.Page.Rows, fullNext.Page.Rows) {
		t.Fatal("parts reuse changed complete next-page evidence", err, fullErr)
	}
}

func TestManagementGatewayPartsByteEvictionAndRawIsolation(t *testing.T) {
	var store managementGatewayPartsStore
	for i := 0; i < 3; i++ {
		source := controlSource{sourceView: sourceView{ID: fmt.Sprint(i)}, Key: "private"}
		f := managementGatewayPartsFixture(source, "r1", "c1", nil)
		f.Providers[0].API = strings.Repeat("x", managementGatewayPartsBytes/2)
		ctx := context.WithValue(context.Background(), managementGatewayPartsScopeKey{}, &managementGatewayPartsScope{store: &store, owner: "owner"})
		rememberManagementGatewayParts(ctx, []managementGatewayFacts{f})
		f.Catalog[0].Model = "changed-caller"
		f.Export["caller"] = true
		if len(store.entries) != 1 || store.bytes > managementGatewayPartsBytes {
			t.Fatal("byte bound did not evict before count bound", store.bytes, len(store.entries))
		}
		var retained managementGatewayParts
		if json.Unmarshal(store.entries[0].raw, &retained) != nil || retained.Catalog[0].Model != "gpt" || retained.Export["caller"] != nil {
			t.Fatal("retained representation aliases caller-owned evidence")
		}
	}
}

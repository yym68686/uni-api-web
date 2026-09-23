package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Reproduce a rolling deployment: B boots from A:1, the draining A then
// accepts a confirmed edit A:2. B must catch up without overriding local edits.
func TestControlsCatchUpUnchangedBootstrapDuringRollout(t *testing.T) {
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
	id := "rollout-" + randomID()[:8]
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
	live := retainedLive{Instance: "A", Revision: "A:1", Atomic: true, Rules: []retainedRule{{KeyID: "k", Model: "m", Order: []string{"one", "two"}}}}
	boot := live
	var receipt map[string]any
	writes := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var in struct {
				Revision string           `json:"revision"`
				Snapshot retainedSnapshot `json:"snapshot"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.Revision != live.Revision {
				http.Error(w, "revision conflict", 409)
				return
			}
			writes++
			live.Rules, live.Channels = in.Snapshot.Rules, in.Snapshot.Channels
			live.Revision = fmt.Sprintf("%s:restored:%d", live.Instance, writes)
		}
		var out map[string]any
		if err := decodeMap(live, &out); err != nil {
			t.Error(err)
		}
		out["bootstrap_restore"] = receipt
		writeJSON(w, 200, out)
	}))
	defer up.Close()
	src := controlSource{sourceView: sourceView{ID: id, Name: "rollout", Base: up.URL}, Key: "fixture-secret"}
	if _, err = store.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	s := &Service{control: store}
	reconcile := func() error {
		unlock, err := store.lockControls(ctx, id)
		if err != nil {
			return err
		}
		defer unlock()
		_, err = s.reconcileControls(ctx, src)
		return err
	}
	if err = reconcile(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/v1/runtime-restore/"+id, nil)
	req.SetPathValue("id", id)
	req.Header.Set("Authorization", "Bearer fixture-secret")
	w := httptest.NewRecorder()
	s.bootstrapControls(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var bootstrap map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &bootstrap); err != nil {
		t.Fatal(err)
	}
	// Old A receives and persists another UI reorder after B has booted.
	live.Revision = "A:2"
	live.Rules = []retainedRule{{KeyID: "k", Model: "m", Order: []string{"two", "one"}}}
	if err = reconcile(); err != nil {
		t.Fatal(err)
	}
	want, err := store.retainedRecord(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	live = boot
	live.Instance = "B"
	live.Revision = "B:1"
	receipt = map[string]any{"snapshot_id": bootstrap["snapshot_id"], "applied_revision": "B:1", "unchanged": true}
	validID, ok := bootstrap["snapshot_id"].(string)
	if !ok || len(validID) != 64 {
		t.Fatal("missing durable bootstrap identity")
	}
	for _, tc := range []struct {
		name   string
		proof  map[string]any
		change bool
	}{
		{"legacy", nil, false},
		{"unknown", map[string]any{"snapshot_id": strings.Repeat("f", 64), "applied_revision": "B:1", "unchanged": true}, false},
		{"edited", map[string]any{"snapshot_id": validID, "applied_revision": "B:1", "unchanged": false}, false},
		{"revision_changed", map[string]any{"snapshot_id": validID, "applied_revision": "B:0", "unchanged": true}, false},
		{"content_changed", receipt, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt = tc.proof
			if tc.change {
				live.Rules = []retainedRule{{KeyID: "k", Model: "m", Order: []string{"other"}}}
			}
			if err = reconcile(); err == nil || writes != 0 {
				t.Fatal("unproven state overwritten")
			}
			after, e := store.retainedRecord(ctx, id)
			if e != nil || after.Encrypted != want.Encrypted || after.Instance != want.Instance {
				t.Fatal("saved intent modified", e)
			}
			live = boot
			live.Instance = "B"
			live.Revision = "B:1"
		})
	}
	receipt = map[string]any{"snapshot_id": validID, "applied_revision": "B:1", "unchanged": true}
	if err = reconcile(); err != nil {
		t.Fatalf("unchanged boot snapshot cannot catch up: %v", err)
	}
	if writes != 1 || live.Rules[0].Order[0] != "two" {
		t.Fatal("latest acknowledged intent not restored")
	}
	if err = reconcile(); err != nil || writes != 1 {
		t.Fatal("unchanged state rewritten", err)
	}
	// Subsequent UI reorders remain writable and retained on the new instance.
	live.Revision = "B:edit"
	live.Rules = []retainedRule{{KeyID: "k", Model: "m", Order: []string{"one", "two"}, Disabled: []string{"two"}}}
	if err = reconcile(); err != nil {
		t.Fatal("new instance edits locked out", err)
	}
	want, err = store.retainedRecord(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// A delayed old-instance observation must not roll durable intent back.
	live = boot
	if err = reconcile(); err == nil || writes != 1 {
		t.Fatal("retired instance accepted")
	}
	after, err := store.retainedRecord(ctx, id)
	if err != nil || after.Encrypted != want.Encrypted || after.Instance != "B" {
		t.Fatal("durable snapshot rolled back", err)
	}
}

func TestBootstrapProofIsScopedToSourceAndTarget(t *testing.T) {
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
	id := "proof-" + randomID()[:8]
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
	src := controlSource{sourceView: sourceView{ID: id, Name: "test", Base: "https://fixture.example"}, Key: "fixture-secret"}
	if _, err = store.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	enc, _ := store.encrypt(`{"version":1,"rules":[],"temporary_channels":[]}`)
	proof := tokenHash(enc)
	_, err = store.db.Exec(`INSERT INTO console_control_bootstraps(source_id,target_hash,snapshot_id,encrypted_snapshot,instance_id,revision) VALUES($1,$2,$3,$4,'A','A:1')`, id, controlTarget(src), proof, enc)
	if err != nil {
		t.Fatal(err)
	}
	live := retainedLive{Instance: "B", Revision: "B:1", Bootstrap: &bootstrapReceipt{SnapshotID: proof, AppliedRevision: "B:1", Unchanged: true}}
	if err = store.verifyBootstrap(ctx, src, live); err != nil {
		t.Fatal(err)
	}
	foreign := src
	foreign.ID = "other-source"
	if store.verifyBootstrap(ctx, foreign, live) == nil {
		t.Fatal("foreign source receipt accepted")
	}
	foreign = src
	foreign.Base = "https://other.example"
	if store.verifyBootstrap(ctx, foreign, live) == nil {
		t.Fatal("foreign target receipt accepted")
	}
	foreign = src
	foreign.Key = "different-admin"
	if store.verifyBootstrap(ctx, foreign, live) == nil {
		t.Fatal("foreign credential receipt accepted")
	}
}

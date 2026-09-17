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

func TestRetainedConfigurationRestoresAcrossRestartAndPreservesDeletes(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, e := newControlStore(dsn, strings.Repeat("m", 32))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	ctx := context.Background()
	id := "retention-" + randomID()[:8]
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
	account := "sub-" + randomID()[:8]
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, account)
	secret, _ := store.encrypt("private-route-key")
	_, e = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,'admin','site','https://site.example','a@b.c','')`, account)
	if e != nil {
		t.Fatal(e)
	}
	_, e = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_routing_key) VALUES($1,1,'group','openai',$2)`, account, secret)
	if e != nil {
		t.Fatal(e)
	}
	provider := subProviderName(account, 1, "key-a")
	live := retainedLive{Instance: "boot-1", Revision: "r1", Rules: []retainedRule{{KeyID: "key-a", Model: "m", Order: []string{provider, "existing"}, Disabled: []string{"existing"}}}, Channels: []retainedChannel{{Provider: provider, KeyID: "key-a", Models: []string{"m"}}}}
	mutations := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			if in["revision"] != live.Revision {
				http.Error(w, "conflict", 409)
				return
			}
			mutations++
			if r.URL.Path == "/v1/temporary-channels" {
				if in["api_key"] != "private-route-key" {
					t.Error("wrong restored secret")
				}
				live.Channels = []retainedChannel{{Provider: provider, KeyID: "key-a", Models: []string{"m"}}}
				live.Rules = []retainedRule{{KeyID: "key-a", Model: "m", Order: []string{provider, "existing"}}}
			} else {
				var rule retainedRule
				decodeMap(in, &rule)
				live.Rules = []retainedRule{rule}
			}
			live.Revision = fmt.Sprintf("boot2:%d", mutations)
		}
		writeJSON(w, 200, live)
	}))
	defer up.Close()
	src := controlSource{sourceView: sourceView{ID: id, Name: "test", Base: up.URL}, Key: "admin-secret"}
	if _, e = store.saveSource(ctx, src, false); e != nil {
		t.Fatal(e)
	}
	s := &Service{control: store}
	reconcile := func() error {
		unlock, e := store.lockControls(ctx, id)
		if e != nil {
			return e
		}
		defer unlock()
		_, e = s.reconcileControls(ctx, src)
		return e
	}
	if e = reconcile(); e != nil {
		t.Fatal(e)
	}
	record, _ := store.retainedRecord(ctx, id)
	if !record.Enabled || record.Encrypted == "" || strings.Contains(record.Encrypted, "private-route-key") {
		t.Fatal("snapshot not encrypted/default enabled")
	}
	saved, e := store.retainedSnapshot(record)
	if e != nil || saved.Channels[0].Key != "private-route-key" {
		t.Fatal("missing business credential", e)
	}
	// Another replica cannot concurrently mutate or restore this source.
	unlock, e := store.lockControls(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if release, e := store.lockControls(ctx, id); e == nil {
		release()
		t.Fatal("source lock not exclusive")
	}
	unlock()
	live = retainedLive{Instance: "boot-2", Revision: "boot2:0", Rules: []retainedRule{}, Channels: []retainedChannel{}}
	if e = reconcile(); e != nil {
		t.Fatal(e)
	}
	if !controlEquivalent(live, saved) || mutations != 2 {
		t.Fatal("full snapshot not restored", live, mutations)
	}
	if e = reconcile(); e != nil || mutations != 2 {
		t.Fatal("repeated restore mutated unchanged state", e)
	}
	// A newly started gateway with manual state must never be overwritten.
	live.Instance = "boot-3"
	live.Revision = "boot3:1"
	live.Rules[0].Disabled = nil
	if e = reconcile(); e == nil || mutations != 2 {
		t.Fatal("conflicting new state overwritten")
	}
	// Same-instance manual reset is authoritative and cannot resurrect on restart.
	live.Instance = "boot-2"
	live.Revision = "boot2:reset"
	live.Rules = nil
	live.Channels = nil
	if e = reconcile(); e != nil {
		t.Fatal(e)
	}
	live.Instance = "boot-4"
	live.Revision = "boot4:0"
	if e = reconcile(); e != nil || len(live.Channels) != 0 || mutations != 2 {
		t.Fatal("deleted channel resurrected", e)
	}
	// Disabling does not issue runtime writes or restore a stale snapshot.
	_, e = store.db.Exec(`UPDATE console_control_snapshots SET enabled=false WHERE source_id=$1`, id)
	if e != nil {
		t.Fatal(e)
	}
	live.Instance = "boot-5"
	live.Revision = "boot5:0"
	if e = reconcile(); e != nil || mutations != 2 {
		t.Fatal("disabled restoration still active", e)
	}
	// Bootstrap is source-authenticated, never an unauthenticated secret export.
	for _, auth := range []string{"", "Bearer other", "Bearer admin-secret"} {
		req := httptest.NewRequest("GET", "/v1/runtime-restore/"+id, nil)
		req.Header.Set("Authorization", auth)
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.bootstrapControls(w, req)
		want := 401
		if auth == "Bearer admin-secret" {
			want = 200
		}
		if w.Code != want || strings.Contains(w.Body.String(), "private-route-key") {
			t.Fatal("bootstrap authorization", w.Code)
		}
	}
}

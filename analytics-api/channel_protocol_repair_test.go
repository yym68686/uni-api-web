package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestProtocolRepairPreviewApplyStatusAndRollback(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("p", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	owner, account := "repair-"+randomID()[:10], "account-"+randomID()[:10]
	key := "key-" + strings.Repeat("a", 64)
	provider := subProviderName(account, 7, key)
	other := subProviderName(account, 7, "another")
	base := "https://site.example/gateway"
	oldDoc := map[string]any{"provider": provider, "engine": "gpt", "base_url": base + "/v1/responses", "api": []string{"private-business-key"}, "model": []any{"gpt-6-luna", "claude-fable-5"}, "preferences": map[string]any{"cooldown_period": 21}}
	manualDoc := map[string]any{"provider": other, "engine": "gpt", "base_url": base + "/v1/responses", "api": []string{"manual-key"}, "model": []any{"claude-fable-5"}}
	old := retainedChannel{Provider: provider, KeyID: key, Key: "private-business-key", Base: base + "/v1/responses", Models: []string{"gpt-6-luna", "claude-fable-5"}, Definition: json.RawMessage(mustJSON(oldDoc))}
	manual := retainedChannel{Provider: other, KeyID: "another", Key: "manual-key", Base: base + "/v1/responses", Models: []string{"claude-fable-5"}, Definition: json.RawMessage(mustJSON(manualDoc))}
	current := retainedSnapshot{Version: 2, Channels: []retainedChannel{old, manual}, Rules: []retainedRule{{KeyID: key, Order: []string{provider}, Disabled: []string{provider}}}, Settings: map[string]json.RawMessage{other: json.RawMessage(`{"set":{"/engine":"gpt"}}`)}}
	revision, restores := "boot:1", 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/channel-controls":
			definitions := map[string]json.RawMessage{}
			for _, channel := range current.Channels {
				definitions[channel.Provider] = channel.Definition
			}
			writeJSON(w, 200, map[string]any{"instance_id": "boot", "revision": revision, "temporary_channel_restore": true, "automatic_engine": true, "channel_settings": true, "channel_definitions": true, "channel_definitions_digest": tokenHash(canonicalSettings(definitions)), "channel_settings_digest": tokenHash(canonicalSettings(current.Settings)), "temporary_channels": current.Channels, "rules": current.Rules})
		case "/v1/channel-settings/export":
			definitions := map[string]json.RawMessage{}
			for _, channel := range current.Channels {
				definitions[channel.Provider] = channel.Definition
			}
			writeJSON(w, 200, map[string]any{"revision": revision, "channel_settings": current.Settings, "temporary_definitions": definitions})
		case "/v1/channel-controls/restore":
			var input struct {
				Revision string           `json:"revision"`
				Snapshot retainedSnapshot `json:"snapshot"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Revision != revision {
				http.Error(w, "stale", 409)
				return
			}
			restores++
			current = input.Snapshot
			revision = "boot:" + string(rune('1'+restores))
			definitions := map[string]json.RawMessage{}
			for _, channel := range current.Channels {
				definitions[channel.Provider] = channel.Definition
			}
			writeJSON(w, 200, map[string]any{"instance_id": "boot", "revision": revision, "temporary_channel_restore": true, "automatic_engine": true, "channel_settings": true, "channel_definitions": true, "channel_definitions_digest": tokenHash(canonicalSettings(definitions)), "channel_settings_digest": tokenHash(canonicalSettings(current.Settings)), "temporary_channels": current.Channels, "rules": current.Rules})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	src := controlSource{sourceView: sourceView{ID: "repair-source-" + randomID()[:8], Base: server.URL, Name: "Fixture"}, Key: "admin-secret"}
	if _, err = store.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	defer func() {
		store.db.Exec(`DELETE FROM console_channel_protocol_operations WHERE source_id=$1`, src.ID)
		store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, src.ID)
		store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, account)
	}()
	if _, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'Fixture',$3,'fixture@example.com','')`, account, owner, base); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key) VALUES($1,7,'Group','openai','')`, account); err != nil {
		t.Fatal(err)
	}
	for model, protocol := range map[string]string{"gpt-6-luna": "responses", "claude-fable-5": "messages"} {
		if _, err = store.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,7,$2,'done',$3)`, account, model, mustJSON(subResult{Availability: subProbe{Status: "success", Protocol: protocol}})); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.retainedRecord(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	service := &Service{control: store}
	session, _ := store.newSession(ctx, owner)
	call := func(body any) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/sources/"+src.ID+"/channel-protocol-repair", strings.NewReader(mustJSON(body)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		return w
	}
	preview := call(map[string]any{"action": "preview"})
	if preview.Code != 200 || restores != 0 || strings.Contains(preview.Body.String(), "private-business-key") {
		t.Fatal("preview changed gateway or exposed key", preview.Code, preview.Body.String())
	}
	var plan struct {
		Revision string `json:"revision"`
		Hash     string `json:"plan_hash"`
		Changes  []any  `json:"changes"`
		Skipped  []any  `json:"skipped"`
	}
	if json.Unmarshal(preview.Body.Bytes(), &plan) != nil || len(plan.Changes) != 1 || len(plan.Skipped) != 1 {
		t.Fatal(preview.Body.String())
	}
	operation := "operation-" + randomID()[:10]
	if w := call(map[string]any{"action": "apply", "revision": plan.Revision, "plan_hash": "stale", "operation_id": operation}); w.Code != 409 || restores != 0 {
		t.Fatal("invalid preview accepted", w.Code, w.Body.String())
	}
	applied := call(map[string]any{"action": "apply", "revision": plan.Revision, "plan_hash": plan.Hash, "operation_id": operation})
	if applied.Code != 200 || restores != 1 || len(current.Channels) != 3 {
		t.Fatal("atomic repair failed", applied.Code, applied.Body.String())
	}
	if current.Channels[2].Provider != provider+"--messages" || current.Channels[2].Base != base+"/v1/messages" || string(current.Channels[0].Definition) != mustJSON(manualDoc) {
		t.Fatal("protocol split or manual override lost", current.Channels)
	}
	if w := call(map[string]any{"action": "status", "operation_id": operation}); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"applied"`) {
		t.Fatal("operation not recoverable", w.Code, w.Body.String())
	}
	if w := call(map[string]any{"action": "rollback", "revision": plan.Revision, "operation_id": operation}); w.Code != 409 || restores != 1 {
		t.Fatal("stale rollback accepted", w.Code, w.Body.String())
	}
	rollback := call(map[string]any{"action": "rollback", "revision": revision, "operation_id": operation})
	if rollback.Code != 200 || restores != 2 || len(current.Channels) != 2 || current.Channels[0].Base != old.Base {
		t.Fatal("rollback failed", rollback.Code, rollback.Body.String())
	}
}

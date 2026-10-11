package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the actual add-channel HTTP handler, New API token provisioning,
// encrypted credential retention and final atomic gateway route application.
func TestNewAPIImportRecoversReadRateLimitAndAppliesExactlyOnce(t *testing.T) {
	s, account, owner := newAPITestService(t)
	ctx := context.Background()
	group, err := s.newAPIGroupID(ctx, account, "中转pro分组")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id) VALUES($1,$2,'中转pro分组','openai',42) ON CONFLICT(account_id,group_id) DO UPDATE SET active=true`, account, group); err != nil {
		t.Fatal(err)
	}
	models := []string{"gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-luna"}
	for _, model := range models {
		raw, _ := json.Marshal(subResult{Model: model, Availability: subProbe{Status: "success", Protocol: "responses"}})
		if _, err = s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,$2,$3,'done',$4)`, account, group, model, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	groupsRead, creates, reveals, applies := 0, 0, 0, 0
	routeName := "uni-console-route-" + account + "-" + fmt.Sprint(group)
	var created newAPIToken
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/user/self/groups":
			groupsRead++
			if groupsRead == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(429)
				return
			}
			newAPIOK(w, map[string]any{"中转pro分组": map[string]any{"ratio": 1}})
		case r.URL.Path == "/api/user/self":
			newAPIOK(w, map[string]any{"id": 7, "username": "fixture"})
		case r.URL.Path == "/api/token/" && r.Method == "GET":
			tokens := []newAPIToken{{ID: 42, UserID: 7, Name: subKeyName(account, group), Group: "中转pro分组", Status: 1, Key: "sk-****"}}
			if created.ID > 0 {
				tokens = append(tokens, created)
			}
			newAPIOK(w, map[string]any{"items": tokens, "total": len(tokens)})
		case r.URL.Path == "/api/token/" && r.Method == "POST":
			creates++
			if creates > 1 {
				t.Error("duplicated token creation")
			}
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Error(err)
			}
			if created.Name != routeName || created.Group != "中转pro分组" || !created.Unlimited {
				t.Error("wrong business key scope")
			}
			created.ID, created.UserID, created.Status, created.Key = 99, 7, 1, "sk-****"
			newAPIOK(w, nil)
		case r.URL.Path == "/api/token/99/key":
			reveals++
			if reveals == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(429)
				return
			}
			newAPIOK(w, map[string]string{"key": "dedicated-business-secret"})
		default:
			t.Error("unexpected upstream operation", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	var applied map[string]any
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/channel-controls":
			writeJSON(w, 200, map[string]any{"revision": "boot:1", "instance_id": "boot", "temporary_channel_import": true, "temporary_channel_restore": true, "channel_settings": true, "automatic_engine": true})
		case "/v1/channel-settings/export":
			writeJSON(w, 200, map[string]any{"revision": "boot:1", "channel_settings": map[string]any{}, "temporary_definitions": map[string]any{}})
		case "/v1/model-channels":
			writeJSON(w, 200, map[string]any{"data": []any{}})
		case "/v1/channel-controls/restore":
			applies++
			json.NewDecoder(r.Body).Decode(&applied)
			writeJSON(w, 200, map[string]any{"revision": "boot:2", "instance_id": "boot"})
		default:
			t.Error("unexpected gateway operation", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	sourceID := "rate-import-" + randomID()[:8]
	t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_sources WHERE id=$1`, sourceID) })
	if _, err = s.control.saveSource(ctx, controlSource{sourceView: sourceView{ID: sourceID, Name: "Fixture", Base: gateway.URL}, Key: "gateway-secret"}, false); err != nil {
		t.Fatal(err)
	}
	session, err := s.control.newSession(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(subImportInput{AccountID: account, GroupID: group, SourceID: sourceID, KeyID: sourceID + "::key-" + strings.Repeat("a", 64), Revision: "boot:1", Models: models, Position: 1})
	r := httptest.NewRequest("POST", "/v1/sub2api/channels", strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if groupsRead != 2 || creates != 1 || reveals != 2 || applies != 1 {
		t.Fatalf("groups=%d creates=%d reveals=%d applies=%d", groupsRead, creates, reveals, applies)
	}
	var encrypted string
	var key int64
	if err = s.control.db.QueryRow(`SELECT routing_key_id,encrypted_routing_key FROM console_sub_targets WHERE account_id=$1 AND group_id=$2`, account, group).Scan(&key, &encrypted); err != nil {
		t.Fatal(err)
	}
	secret, err := s.control.decrypt(encrypted)
	if err != nil || key != 99 || secret != "sk-dedicated-business-secret" || encrypted == secret {
		t.Fatal("business key not retained encrypted")
	}
	if applied["revision"] != "boot:1" {
		t.Fatal("lost concurrency fence")
	}
	snapshot, _ := json.Marshal(applied["snapshot"])
	for _, model := range models {
		if !strings.Contains(string(snapshot), model) {
			t.Fatal("missing routed model", model)
		}
	}
	var state string
	s.control.db.QueryRow(`SELECT state FROM console_sub_accounts WHERE id=$1`, account).Scan(&state)
	if state != "idle" {
		t.Fatal("account lease not released")
	}
}

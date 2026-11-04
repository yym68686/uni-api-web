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

func TestUnknown400MutationValidationAndGatewayCapability(t *testing.T) {
	for _, value := range []any{nil, true, "1", -1, 4, 1.5, map[string]any{}} {
		in := channelSettingMutation{Changes: []channelSettingChange{{Provider: unknown400Scope, Set: map[string]any{unknown400Path: value}}}}
		if _, err := unknown400Mutation(in); err == nil {
			t.Fatalf("accepted invalid policy %v", value)
		}
	}
	for n := 0; n <= 3; n++ {
		in := channelSettingMutation{Changes: []channelSettingChange{{Provider: unknown400Scope, Set: map[string]any{unknown400Path: n}}}}
		required, err := unknown400Mutation(in)
		if err != nil || required != (n > 0) {
			t.Fatal(n, required, err)
		}
		in.Changes[0].Provider = "ordinary-provider"
		if _, err = unknown400Mutation(in); err == nil {
			t.Fatal("accepted non-global policy")
		}
	}
	for _, supported := range []bool{false, true} {
		for _, method := range []string{"POST", "PATCH"} {
			writes := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && r.URL.Path == "/v1/channel-settings/schema" {
					writeJSON(w, 200, map[string]any{"unknown_upstream_400_retries": true, "all_upstream_400_retries": supported})
					return
				}
				writes++
				writeJSON(w, 200, map[string]any{"status": "validated"})
			}))
			svc := &Service{}
			path := "/v1/channel-settings"
			if method == "POST" {
				path += "/validate"
			}
			in := channelSettingMutation{Changes: []channelSettingChange{{Provider: unknown400Scope, Set: map[string]any{unknown400Path: 2}}}}
			_, code, err := svc.settingsGateway(context.Background(), controlSource{sourceView: sourceView{Base: up.URL}}, method, path, in)
			up.Close()
			if supported && (err != nil || code != 200 || writes != 1) {
				t.Fatal(code, err, writes)
			}
			if !supported && (err == nil || code != 409 || writes != 0) {
				t.Fatal("unsupported write escaped", code, err, writes)
			}
		}
	}
}

func TestUnknown400RestoreGateIgnoresUnrelatedFieldsAndPreservesZero(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{}`, false},
		{`{"provider":{"set":{"/preferences/unknown_upstream_400_retries":2}}}`, false},
		{`{"__uni_console_global_preferences__":{"remove":["/preferences/unknown_upstream_400_retries"]}}`, false},
		{`{"__uni_console_global_preferences__":{"set":{"/preferences/unknown_upstream_400_retries":0}}}`, false},
		{`{"__uni_console_global_preferences__":{"set":{"/preferences/unknown_upstream_400_retries":3}}}`, true},
		{`{"__uni_console_global_preferences__":{"set":{"/preferences/unknown_upstream_400_retries":null}}}`, true},
		{`{"__uni_console_global_preferences__":{"set":{"/preferences/unknown_upstream_400_retries":"1"}}}`, true},
		{`{"__uni_console_global_preferences__":{"set":{"/preferences/headers":{"echo":{"unknown_upstream_400_retries":2}}}}}`, false},
	} {
		var saved retainedSnapshot
		if err := json.Unmarshal([]byte(tc.raw), &saved.Settings); err != nil {
			t.Fatal(err)
		}
		if requiresUnknown400Retry(saved) != tc.want {
			t.Fatal(tc.raw)
		}
	}
}

func TestUnknown400BootstrapRequiresCapabilityAndKeepsSavedSnapshot(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("u", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "unknown400-" + randomID()[:8]
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id)
	src := controlSource{sourceView: sourceView{ID: id, Name: "isolated fixture", Base: "http://127.0.0.1:1"}, Key: "fixture-admin"}
	if _, err = store.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	if _, err = store.retainedRecord(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	svc := &Service{control: store}
	for _, n := range []int{0, 2} {
		saved := retainedSnapshot{Version: 2, Settings: map[string]json.RawMessage{
			unknown400Scope: json.RawMessage(mustJSON(map[string]any{"set": map[string]any{unknown400Path: n}, "remove": []string{}})),
		}}
		encrypted, err := store.encrypt(mustJSON(saved))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.Exec(`UPDATE console_control_snapshots SET enabled=true,status='saved',encrypted_snapshot=$2,target_hash=$3 WHERE source_id=$1`, id, encrypted, controlTarget(src)); err != nil {
			t.Fatal(err)
		}
		for _, capability := range []string{"", "0", "1", "all"} {
			req := httptest.NewRequest("GET", "/v1/runtime-restore/"+id, nil)
			req.Header.Set("Authorization", "Bearer fixture-admin")
			req.Header.Set("X-Uni-Channel-Settings-Version", "1")
			if capability != "all" {
				req.Header.Set("X-Uni-Unknown-Upstream-400-Retries", capability)
			}
			if capability == "all" {
				req.Header.Set("X-Uni-All-Upstream-400-Retries", "1")
			}
			req.SetPathValue("id", id)
			w := httptest.NewRecorder()
			svc.bootstrapControls(w, req)
			want := 200
			if n > 0 && capability != "all" && capability != "1" {
				want = 409
			}
			if w.Code != want {
				t.Fatal(n, capability, w.Code, w.Body.String())
			}
			record, err := store.retainedRecord(context.Background(), id)
			if err != nil || record.Encrypted != encrypted {
				t.Fatal("saved intent rewritten", err)
			}
		}
	}
}

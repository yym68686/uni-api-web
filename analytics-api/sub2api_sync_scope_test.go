package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Exercise HTTP queue -> durable claim -> complete sync using local fixtures,
// including site-discovered models and successful historical results.
func TestSubSyncAllSelectedModelsReachEveryProbeStage(t *testing.T) {
	for _, selected := range [][]string{{"gemini-4-argon"}, {"gpt-6.1-sol", "gemini-4-argon", "extra-selected"}} {
		t.Run(strings.Join(selected, "+"), func(t *testing.T) {
			s, account, owner := newAPITestService(t)
			ctx := context.Background()
			group, err := s.newAPIGroupID(ctx, account, "default")
			if err != nil {
				t.Fatal(err)
			}
			oldTools := subCapabilityResult{Status: "supported", Model: "gpt-5.6-terra", CheckedAt: 123, Attempts: []subProbe{}}
			oldResult := subResult{Model: "gpt-5.6-terra", CheckedAt: 123, Availability: subProbe{Status: "success"}}
			if _, err = s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,state,compaction_state,compaction,tool_use_state,tool_use) VALUES($1,$2,'default','openai','done','done',$3,'done',$3)`, account, group, mustJSON(oldTools)); err != nil {
				t.Fatal(err)
			}
			for _, model := range []string{"gpt-5.6-terra", "gpt-6-astra", "extra-unselected"} {
				saved := oldResult
				saved.Model = model
				if _, err = s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,$2,$3,'done',$4)`, account, group, model, mustJSON(saved)); err != nil {
					t.Fatal(err)
				}
			}
			key := newAPIToken{ID: 51, UserID: 7, Name: subKeyName(account, group), Group: "default", Key: "sk-local-fixture", Status: 1, Unlimited: true}
			var mu sync.Mutex
			calls := map[string]int{}
			withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/user/self/groups":
					newAPIOK(w, map[string]any{"default": map[string]any{"ratio": 0.25}})
				case "/api/user/models":
					newAPIOK(w, []string{"extra-selected", "extra-unselected", "gpt-5.6-terra"})
				case "/api/token/":
					if r.Method != "GET" {
						t.Error("unexpected token creation")
					}
					newAPIOK(w, map[string]any{"items": []newAPIToken{key}, "total": 1})
				default:
					if r.Method != "POST" || !(strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v1beta/models/")) {
						t.Error("unexpected request", r.Method, r.URL.Path)
						http.NotFound(w, r)
						return
					}
					compact := subFixtureCompaction(r)
					model := subFixtureProbeModel(r)
					mu.Lock()
					calls[model]++
					mu.Unlock()
					if !slices.Contains(selected, model) {
						t.Error("unchecked model was probed", model)
					}
					if compact {
						compactSSE(w, model, `[{"type":"compaction","encrypted_content":"opaque"}]`)
					} else if r.URL.Path == "/v1/responses" {
						subSSE(w, "test", model)
					} else {
						subNativeSSE(w, model, "test")
					}
				}
			}))
			token, _ := s.control.newSession(ctx, owner)
			r := httptest.NewRequest("POST", "/v1/sub2api/accounts/sync-selected", strings.NewReader(mustJSON(map[string]any{"models": selected})))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != 202 {
				t.Fatalf("queue %d: %s", w.Code, w.Body.String())
			}
			// A newly created worker instance must recover the submitted selection.
			worker := &Service{control: s.control, newAPILogGap: s.newAPILogGap}
			claimed, err := worker.subClaimJob(ctx)
			if err != nil || claimed.id != account {
				t.Fatalf("claim: %s %v", claimed.id, err)
			}
			worker.subRunJob(ctx, claimed)
			var state, message string
			if err = s.control.db.QueryRow(`SELECT state,message FROM console_sub_accounts WHERE id=$1`, account).Scan(&state, &message); err != nil || state != "idle" {
				t.Fatalf("worker: %s %s %v", state, message, err)
			}
			mu.Lock()
			gotCalls := map[string]int{}
			for k, v := range calls {
				gotCalls[k] = v
			}
			mu.Unlock()
			if gotCalls["gemini-4-argon"] != 1 {
				t.Fatal("missing Gemini availability", gotCalls)
			}
			if len(selected) > 1 && (gotCalls["gpt-6.1-sol"] != 3 || gotCalls["extra-selected"] != 1) {
				t.Fatal("availability/compression/tools not scoped", gotCalls)
			}
			if len(gotCalls) != len(selected) {
				t.Fatal("unexpected probe model", gotCalls)
			}
			rows, err := s.control.db.Query(`SELECT model FROM console_model_fingerprints WHERE account_id=$1 ORDER BY model`, account)
			if err != nil {
				t.Fatal(err)
			}
			fingerprints := []string{}
			for rows.Next() {
				var m string
				if err = rows.Scan(&m); err != nil {
					t.Fatal(err)
				}
				fingerprints = append(fingerprints, m)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"gemini-4-argon"}
			if len(selected) > 1 {
				want = append(want, "gpt-6.1-sol")
			}
			sort.Strings(want)
			if !reflect.DeepEqual(fingerprints, want) {
				t.Fatal("fingerprint scope", fingerprints, want)
			}
			var historical subResult
			var raw []byte
			if err = s.control.db.QueryRow(`SELECT result FROM console_sub_models WHERE account_id=$1 AND model='gpt-5.6-terra'`, account).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &historical); err != nil || historical.CheckedAt != 123 {
				t.Fatal("unchecked history changed", historical, err)
			}
			if err = s.control.db.QueryRow(`SELECT tool_use FROM console_sub_targets WHERE account_id=$1 AND group_id=$2`, account, group).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var tools subCapabilityResult
			if err = json.Unmarshal(raw, &tools); err != nil {
				t.Fatal(err)
			}
			if history := toolUseModelResults(&tools)["gpt-5.6-terra"]; history == nil || history.CheckedAt != 123 {
				t.Fatal("unchecked tool history lost", tools)
			}
			if len(selected) == 1 {
				if err = s.control.db.QueryRow(`SELECT compaction FROM console_sub_targets WHERE account_id=$1 AND group_id=$2`, account, group).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var compact subCapabilityResult
				json.Unmarshal(raw, &compact)
				if compact.CheckedAt != 123 {
					t.Fatal("Gemini-only sync launched GPT capability probe", compact)
				}
			}
		})
	}
}

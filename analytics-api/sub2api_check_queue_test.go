package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubChecksQueueBehindActiveWorkWithoutReplacingLease(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop=%v", stop), func(t *testing.T) {
			store, err := newControlStore(isolatedControlTestDSN(t), strings.Repeat("q", 32))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			id := "check-queue-" + randomID()[:12]
			defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE owner=$1`, id)
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var calls, fastCalls, ultrafastCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if call == 1 {
					close(started)
					<-release
				}
				model, _ := body["model"].(string)
				raw := mustJSON(body)
				if tier, ok := body["service_tier"].(string); ok {
					if model != "gpt-6.2-sol" || r.URL.Path != "/v1/responses" {
						t.Errorf("unexpected speed probe: %s %s", r.URL.Path, model)
					}
					switch tier {
					case "fast":
						fastCalls.Add(1)
						tier = "" // The upstream completes without confirming the requested tier.
					case "ultrafast":
						ultrafastCalls.Add(1)
					default:
						t.Errorf("unexpected speed tier: %s", tier)
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, mustJSON(map[string]any{"id": "resp-fixture", "model": model, "status": "completed", "service_tier": tier, "output": []any{}}))
				} else if strings.Contains(raw, "compaction_trigger") {
					compactSSE(w, model, `[{"type":"compaction","encrypted_content":"opaque"}]`)
				} else if strings.Contains(raw, "additional_tools") {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, toolUseSSE([]toolUseItem{validToolUseItem()}, nil))
				} else {
					subSSE(w, "test", model)
				}
			}))
			defer server.Close()
			defer unblock()
			previous := subHTTP
			subHTTP = server.Client()
			defer func() { subHTTP = previous }()
			key, _ := store.encrypt("fixture-key")
			for _, account := range []string{id, id + "-other"} {
				_, err = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,$1,$3,$1,'')`, account, id, server.URL)
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key,remote_key_id,state) SELECT $1,n,'Group','openai',$2,42,'done' FROM generate_series(1,3) n`, account, key)
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) SELECT $1,n,'gpt-5.6-sol','done',$2 FROM generate_series(1,3) n`, account, mustJSON(subResult{Model: "gpt-5.6-sol", Availability: subProbe{Status: "success"}}))
				if err != nil {
					t.Fatal(err)
				}
			}
			// Only the new selection may drive capabilities on group 3; leave its
			// previously successful gpt-5.6-sol in the DB as a regression fixture.
			if _, err = store.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,3,'gpt-6.2-sol','done',$2)`,
				id, mustJSON(subResult{Model: "gpt-6.2-sol", ServiceTiers: map[string]subTierProbe{"fast": {Status: "supported", CheckedAt: 1}}})); err != nil {
				t.Fatal(err)
			}
			s := &Service{control: store}
			token, _ := store.newSession(context.Background(), id)
			request := func(path, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				return w
			}
			queue := func(kind, account string, group int, code int) {
				t.Helper()
				path := "/v1/sub2api/" + kind + "-checks"
				if kind == "check" {
					path = "/v1/sub2api/checks"
				}
				model := "gpt-5.6-sol"
				if kind == "check" {
					model = "gpt-6.2-sol"
				}
				body := fmt.Sprintf(`{"targets":[{"account_id":%q,"group_id":%d,"models":[%q]}]}`, account, group, model)
				if w := request(path, body); w.Code != code {
					t.Fatalf("%s: %d %s", kind, w.Code, w.Body.String())
				}
			}
			// An earlier failed worker left capability phases queued on another
			// group. Starting this request must not revive those old phases.
			if _, err = store.db.Exec(`UPDATE console_sub_targets SET compaction_state='queued',tool_use_state='running' WHERE account_id=$1 AND group_id=3`, id); err != nil {
				t.Fatal(err)
			}
			queue("compaction", id, 1, 202)
			done := make(chan bool, 1)
			go func() { done <- s.subWorkOne(context.Background()) }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not start")
			}
			var lease string
			store.db.QueryRow(`SELECT job_id FROM console_sub_accounts WHERE id=$1`, id).Scan(&lease)
			queue("compaction", id, 1, 409)
			queue("tool-use", id, 1, 202)
			queue("tool-use", id, 1, 409)
			queue("compaction", id, 2, 202)
			queue("check", id, 3, 202)
			// Invalid batches roll back their earlier valid selection as well.
			if w := request("/v1/sub2api/tool-use-checks", fmt.Sprintf(`{"targets":[{"account_id":%q,"group_id":2},{"account_id":%q,"group_id":999}]}`, id, id)); w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
			var state, kind, afterLease string
			store.db.QueryRow(`SELECT state,job_kind,job_id FROM console_sub_accounts WHERE id=$1`, id).Scan(&state, &kind, &afterLease)
			if state != "running" || kind != "compaction" || afterLease != lease {
				t.Fatal("active job overwritten", state, kind, afterLease)
			}
			var waiting int
			store.db.QueryRow(`SELECT count(*) FROM console_sub_check_queue WHERE account_id=$1`, id).Scan(&waiting)
			if waiting != 3 {
				t.Fatal("queued work missing or invalid batch leaked", waiting)
			}
			// A fresh page receives persisted waiting states, not just local spinners.
			r := httptest.NewRequest("GET", "/v1/sub2api/accounts", nil)
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			var payload struct {
				Data []subAccount `json:"data"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &payload) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, a := range payload.Data {
				if a.ID == id {
					selectedQueued := false
					for _, model := range a.Targets[2].Models {
						selectedQueued = selectedQueued || model.Model == "gpt-6.2-sol" && model.State == "queued"
					}
					if a.Targets[0].ToolUseState != "queued" || a.Targets[1].CompactionState != "queued" || !selectedQueued {
						t.Fatal("waiting tasks absent from page", a.Targets)
					}
				}
			}
			// Another account still runs while the first probe is blocked.
			queue("tool-use", id+"-other", 1, 202)
			if !s.subWorkOne(context.Background()) || calls.Load() != 2 {
				t.Fatal("unrelated account blocked", calls.Load())
			}
			if stop {
				if w := request("/v1/sub2api/accounts/"+id+"/stop", "{}"); w.Code != 200 {
					t.Fatal(w.Code)
				}
			}
			unblock()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not finish")
			}
			// A replacement service can drain durable work after a restart.
			replacement := &Service{control: store}
			for i := 0; i < 4; i++ {
				if !replacement.subWorkOne(context.Background()) {
					break
				}
			}
			store.db.QueryRow(`SELECT count(*) FROM console_sub_check_queue WHERE account_id=$1`, id).Scan(&waiting)
			if waiting != 0 {
				t.Fatal("queue never drained", waiting)
			}
			want, wantTiers := int32(9), int32(1) // initial + other account + tool + compact + availability/fast/ultrafast/compact/tool
			if stop {
				want, wantTiers = 2, 0
			}
			if calls.Load() != want {
				t.Fatal("duplicate, lost, or stopped work executed", calls.Load(), want)
			}
			if fastCalls.Load() != wantTiers || ultrafastCalls.Load() != wantTiers {
				t.Fatal("expected independent speed probes for just the selected model", fastCalls.Load(), ultrafastCalls.Load())
			}
			if !stop {
				var fast, ultrafast string
				var at int64
				if err := store.db.QueryRow(`SELECT result#>>'{service_tiers,fast,status}',result#>>'{service_tiers,ultrafast,status}',(result#>>'{service_tiers,fast,checked_at}')::bigint FROM console_sub_models WHERE account_id=$1 AND group_id=3 AND model='gpt-6.2-sol'`, id).Scan(&fast, &ultrafast, &at); err != nil || fast != "supported" || ultrafast != "supported" || at != 1 {
					t.Fatal("did not preserve Fast evidence and save the new Ultrafast result", fast, ultrafast, at, err)
				}
				r := httptest.NewRequest("GET", "/v1/sub2api/accounts/"+id+"/groups/3/details?include_rates=0", nil)
				r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
				r.Header.Set("X-Console-View", "summary")
				w := httptest.NewRecorder()
				replacement.Handler().ServeHTTP(w, r)
				var detail struct {
					Data []struct {
						Value struct {
							AccountID string `json:"account_id"`
							Target    *subTarget
						}
					}
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil {
					t.Fatal(w.Code, w.Body.String())
				}
				var group *subTarget
				for _, row := range detail.Data {
					if row.Value.AccountID == id && row.Value.Target != nil && row.Value.Target.GroupID == 3 {
						group = row.Value.Target
					}
				}
				if group == nil {
					t.Fatal("per-row read omitted the selected group")
				}
				var result *subResult
				for _, model := range group.Models {
					if model.Model == "gpt-6.2-sol" {
						result = model.Result
					}
				}
				if result == nil || result.ServiceTiers["fast"].Status != "supported" || result.ServiceTiers["ultrafast"].Status != "supported" {
					t.Fatal("per-row read dropped tier results", w.Body.String())
				}
				if group.Compaction == nil || len(group.Compaction.Models) != 1 || group.Compaction.Models[0].Model != "gpt-6.2-sol" ||
					group.ToolUse == nil || len(group.ToolUse.Models) != 1 || group.ToolUse.Models[0].Model != "gpt-6.2-sol" {
					t.Fatal("compaction and Tool use must both target only the selected model", w.Body.String())
				}
				if _, err := store.db.Exec(`UPDATE console_sub_targets SET remote_key_id=43 WHERE account_id=$1 AND group_id=3`, id); err != nil {
					t.Fatal(err)
				}
				var hasStaleTier bool
				if err := store.db.QueryRow(`SELECT result ? 'service_tiers' FROM console_sub_models WHERE account_id=$1 AND group_id=3 AND model='gpt-6.2-sol'`, id).Scan(&hasStaleTier); err != nil || hasStaleTier {
					t.Fatal("site credential rotation must invalidate its tier evidence", hasStaleTier, err)
				}
			}
			store.db.QueryRow(`SELECT state FROM console_sub_accounts WHERE id=$1`, id).Scan(&state)
			if (!stop && state != "idle") || (stop && state != "stopped") {
				t.Fatal(state)
			}
		})
	}
}

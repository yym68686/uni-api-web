package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubChecksQueueBehindActiveWorkWithoutReplacingLease(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop=%v", stop), func(t *testing.T) {
			dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
			if dsn == "" {
				t.Skip("PostgreSQL required")
			}
			store, err := newControlStore(dsn, strings.Repeat("q", 32))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			id := "check-queue-" + randomID()[:12]
			defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE owner=$1`, id)
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var calls atomic.Int32
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
				if strings.Contains(raw, "compaction_trigger") {
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
				body := fmt.Sprintf(`{"targets":[{"account_id":%q,"group_id":%d,"models":["gpt-5.6-sol"]}]}`, account, group)
				if w := request(path, body); w.Code != code {
					t.Fatalf("%s: %d %s", kind, w.Code, w.Body.String())
				}
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
					if a.Targets[0].ToolUseState != "queued" || a.Targets[1].CompactionState != "queued" || a.Targets[2].Models[0].State != "queued" {
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
			want := int32(7) // initial + other account + tool + compact + say/compact/tool
			if stop {
				want = 2
			}
			if calls.Load() != want {
				t.Fatal("duplicate, lost, or stopped work executed", calls.Load(), want)
			}
			store.db.QueryRow(`SELECT state FROM console_sub_accounts WHERE id=$1`, id).Scan(&state)
			if (!stop && state != "idle") || (stop && state != "stopped") {
				t.Fatal(state)
			}
		})
	}
}

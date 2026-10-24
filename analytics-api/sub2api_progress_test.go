package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubProgressReportsSelectedModelsBeforeWholeAccountFinishes(t *testing.T) {
	db := migrationTestDB(t)
	if err := migrateControlSchema(context.Background(), db, controlMigrations()); err != nil {
		t.Fatal(err)
	}
	s := &Service{control: &controlStore{db: db, key: []byte(strings.Repeat("s", 32))}}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, owner := range []string{"owner", "foreign"} {
		exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth,state,job_kind,sync_models) VALUES($1,$1,$1,'https://fixture.test',$1,'','running','sync','["one","two"]')`, owner)
		for _, g := range []int{1, 2} {
			exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,state) VALUES($1,$2,'group','openai','queued')`, owner, g)
			exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,$2,'one','done','{"availability":{"text":"private body"}}'),($1,$2,'two','queued',NULL),($1,$2,'unchecked','running',NULL)`, owner, g)
		}
	}
	token, err := s.control.newSession(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	get := func() subAccount {
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts/progress", nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private body") || strings.Contains(w.Body.String(), "foreign") {
			t.Fatal("scope/detail leaked")
		}
		var out struct {
			Data   []subAccount `json:"data"`
			ReadAt int64        `json:"read_at_ms"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Data) != 1 || out.ReadAt <= 0 {
			t.Fatal(err, w.Body.String())
		}
		return out.Data[0]
	}
	a := get()
	if len(a.Progress.RunningModels) != 0 {
		t.Fatal("unchecked task leaked", mustJSON(a.Progress))
	}
	if a.Progress.Total != 4 || a.Progress.Completed != 2 || a.Progress.Queued != 2 || *a.CompletedGroups != 0 || len(a.SyncModels) != 2 {
		t.Fatal(mustJSON(a))
	}
	exec(`UPDATE console_sub_models SET state='done' WHERE account_id='owner' AND group_id=1 AND model='two'`)
	a = get()
	if a.Progress.Completed != 3 || a.Progress.Queued != 1 || *a.CompletedGroups != 1 || *a.ActiveGroups != 2 {
		t.Fatal(mustJSON(a))
	}
	exec(`UPDATE console_sub_models SET state='running' WHERE account_id='owner' AND group_id=2 AND model='two'`)
	a = get()
	if a.Progress.Running != 1 || len(a.Progress.RunningModels) != 1 || a.Progress.RunningModels[0] != "two" {
		t.Fatal(mustJSON(a))
	}
	// A durable new selection replaces the displayed job scope atomically.
	exec(`UPDATE console_sub_accounts SET sync_models='["unchecked"]' WHERE id='owner'`)
	a = get()
	if a.Progress.Total != 2 || a.Progress.Running != 2 || len(a.SyncModels) != 1 {
		t.Fatal(mustJSON(a))
	}
}

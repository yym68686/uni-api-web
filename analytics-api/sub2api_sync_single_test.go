package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSubSyncSingle36Of291ModelsAcross21Groups(t *testing.T) {
	db := migrationTestDB(t)
	ctx := context.Background()
	if err := migrateControlSchema(ctx, db, controlMigrations()); err != nil {
		t.Fatal(err)
	}
	s := &Service{control: &controlStore{db: db, key: []byte(strings.Repeat("s", 32))}}
	selected := append([]string{}, subModels[:36]...)
	catalog := append([]string{}, subModels...)
	for len(catalog) < 291 {
		catalog = append(catalog, fmt.Sprintf("site-extra-%03d", len(catalog)))
	}
	if len(catalog) != 291 {
		t.Fatal("fixture requires a 291-model catalog")
	}
	if _, err := db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('okai','owner','okai','https://fixture.test','fixture','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,state) SELECT 'okai',g,'group','openai','done' FROM generate_series(1,21) g`); err != nil {
		t.Fatal(err)
	}
	// Twenty groups have historical results; the last group is newly discovered.
	if _, err := db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,message)
 SELECT 'okai',g,m,'done','preserved history' FROM generate_series(1,20) g CROSS JOIN jsonb_array_elements_text($1::jsonb) m`, mustJSON(catalog)); err != nil {
		t.Fatal(err)
	}
	token, err := s.control.newSession(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/sub2api/accounts/okai/sync-selected", strings.NewReader(mustJSON(map[string]any{"models": selected})))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	// Recover the scope through the same durable claim used by another worker.
	job, err := s.subClaimJob(ctx)
	if err != nil || job.id != "okai" {
		t.Fatal("claim", job.id, err)
	}
	var captured []string
	if err := json.Unmarshal(job.syncModels, &captured); err != nil || !reflect.DeepEqual(captured, selected) {
		t.Fatal("worker did not recover the submitted 36 models", captured, err)
	}
	groups, discovered := []subRemoteGroup{}, map[int64][]string{}
	for g := int64(1); g <= 21; g++ {
		groups = append(groups, subRemoteGroup{ID: g})
		discovered[g] = catalog
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := queueSubSyncModels(withSubSyncModels(ctx, captured), tx, job.id, groups, discovered); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var total, queued, preserved, idle int
	if err := db.QueryRow(`SELECT count(*),count(*) FILTER(WHERE state='queued'),
 count(*) FILTER(WHERE state='done' AND message='preserved history'),count(*) FILTER(WHERE state='idle')
 FROM console_sub_models WHERE account_id='okai'`).Scan(&total, &queued, &preserved, &idle); err != nil {
		t.Fatal(err)
	}
	if total != 291*21 || queued != 36*21 || preserved != 255*20 || idle != 255 {
		t.Fatal("unexpected discovery, probe or history scope", total, queued, preserved, idle)
	}
	r = httptest.NewRequest("GET", "/v1/sub2api/accounts/progress", nil)
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var out struct {
		Data []subAccount `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Data) != 1 || out.Data[0].Progress == nil {
		t.Fatal("progress", w.Code, w.Body.String())
	}
	p := out.Data[0].Progress
	if p.Total != 36*21 || p.Queued != 36*21 || p.Completed != 0 || len(p.Groups) != 21 {
		t.Fatal("historical/unselected models counted as current work", mustJSON(p))
	}
	for _, g := range p.Groups {
		if g.Total != 36 || g.Queued != 36 || g.Completed != 0 {
			t.Fatal("per-group scope", g)
		}
	}
}

func TestSubSyncSingleRequiresSelectionAndPreservesOtherJobs(t *testing.T) {
	db := migrationTestDB(t)
	if err := migrateControlSchema(context.Background(), db, controlMigrations()); err != nil {
		t.Fatal(err)
	}
	s := &Service{control: &controlStore{db: db, key: []byte(strings.Repeat("s", 32))}}
	for _, row := range []struct{ id, owner, state string }{
		{"mine", "owner", "idle"}, {"another", "owner", "idle"},
		{"busy", "owner", "running"}, {"queued", "owner", "queued"}, {"foreign", "other", "idle"},
	} {
		if _, err := db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth,state,job_kind,job_id,message,lease_until,sync_models) VALUES($1,$2,$1,'https://fixture.test',$1,'',$3,'quality','existing','keep',now()+interval '1 hour','["original"]')`, row.id, row.owner, row.state); err != nil {
			t.Fatal(err)
		}
	}
	token, err := s.control.newSession(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	post := func(id, suffix, body, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/sub2api/accounts/"+id+"/"+suffix, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	valid := `{"models":["gemini-4-argon","extra-selected","gemini-4-argon"]}`
	for _, suffix := range []string{"sync", "sync-selected"} {
		if w := post("mine", suffix, valid, ""); w.Code != 401 {
			t.Fatal("unauthenticated", w.Code)
		}
		tooMany := make([]string, 257)
		for i := range tooMany {
			tooMany[i] = "gpt-6.1-sol"
		}
		for _, body := range []string{"", `{}`, `{"models":null}`, `{"models":[]}`, `{"models":[""]}`, `{"models":[" invalid"]}`, `{"models":"gpt-6.1-sol"}`, mustJSON(map[string]any{"models": tooMany})} {
			if w := post("mine", suffix, body, token); w.Code != 400 {
				t.Fatalf("%s accepted invalid scope %s: %d", suffix, body, w.Code)
			}
		}
		for _, id := range []string{"foreign", "missing", "busy", "queued"} {
			if w := post(id, suffix, valid, token); w.Code != 409 {
				t.Fatal("foreign/busy/missing job changed", suffix, id, w.Code)
			}
		}
	}
	// Concurrent clicks may queue this account only once, with the selection
	// persisted in the same statement as the job state.
	responses := make(chan int, 2)
	for range 2 {
		go func() { responses <- post("mine", "sync-selected", valid, token).Code }()
	}
	codes := map[int]int{}
	for range 2 {
		codes[<-responses]++
	}
	if codes[202] != 1 || codes[409] != 1 {
		t.Fatal("duplicate submission", codes)
	}
	for _, id := range []string{"mine", "another", "busy", "queued", "foreign"} {
		var state, kind, job, message string
		var leased bool
		var raw []byte
		if err := db.QueryRow(`SELECT state,job_kind,job_id,message,lease_until IS NOT NULL,sync_models FROM console_sub_accounts WHERE id=$1`, id).Scan(&state, &kind, &job, &message, &leased, &raw); err != nil {
			t.Fatal(err)
		}
		var models []string
		if err := json.Unmarshal(raw, &models); err != nil {
			t.Fatal(err)
		}
		if id == "mine" {
			if state != "queued" || kind != "sync" || job != "" || message != "" || leased || !reflect.DeepEqual(models, []string{"gemini-4-argon", "extra-selected"}) {
				t.Fatal("selected scope not queued atomically", state, kind, job, message, models)
			}
		} else if kind != "quality" || job != "existing" || message != "keep" || !leased || !reflect.DeepEqual(models, []string{"original"}) || (id == "busy" && state != "running") || (id == "queued" && state != "queued") || ((id == "another" || id == "foreign") && state != "idle") {
			t.Fatal("unrelated job changed", id, state, kind, job, models)
		}
	}
	// A legacy client with an explicit scope also works, but never detect-all.
	if w := post("another", "sync", valid, token); w.Code != 202 {
		t.Fatal("explicit legacy scope rejected", w.Code, w.Body.String())
	}
}

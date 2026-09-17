package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestSubImportUsesBusinessKeyAndFencesModelsOwnerAndRevision(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, e := newControlStore(dsn, strings.Repeat("m", 32))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	owner := "import-owner-" + randomID()[:8]
	account := "sub_" + randomID()[:16]
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, account)
	auth, _ := store.encrypt(`{"access_token":"panel-secret"}`)
	testkey, _ := store.encrypt("test-secret")
	_, e = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'account','https://site.example','user@example.com',$3)`, account, owner, auth)
	if e != nil {
		t.Fatal(e)
	}
	store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,encrypted_key) VALUES($1,7,'group','openai',1,$2)`, account, testkey)
	raw, _ := json.Marshal(subResult{Model: checkModel, Availability: subProbe{Status: "success"}})
	store.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,7,$2,'done',$3)`, account, checkModel, string(raw))
	remoteKey := subRemoteKey{}
	created := 0
	imports := 0
	revision := "boot:1"
	var last map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		success := func(data any) { writeJSON(w, 200, map[string]any{"code": 0, "data": data}) }
		switch r.URL.Path {
		case "/api/v1/groups/available":
			success([]subRemoteGroup{{ID: 7, Name: "group", Platform: "openai"}})
		case "/api/v1/keys":
			if r.Method == "GET" {
				success(map[string]any{"items": []any{}, "pages": 1})
				return
			}
			created++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body["quota"]; ok {
				t.Fatal("business key retained test budget")
			}
			remoteKey = subRemoteKey{ID: 99, Name: body["name"].(string), GroupID: 7, Key: "business-secret", Status: "active"}
			success(remoteKey)
		case "/api/v1/keys/99":
			success(remoteKey)
		default:
			t.Error("unexpected panel path", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Error("wrong gateway auth")
		}
		switch r.URL.Path {
		case "/v1/channel-controls":
			writeJSON(w, 200, map[string]any{"revision": revision, "temporary_channel_import": true})
		case "/v1/temporary-channels":
			imports++
			json.NewDecoder(r.Body).Decode(&last)
			if last["revision"] != revision {
				http.Error(w, "conflict", 409)
				return
			}
			revision = "boot:2"
			writeJSON(w, 200, map[string]any{"revision": revision})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	sourceID := "import-source-" + randomID()[:8]
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, sourceID)
	store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: sourceID, Name: "gateway", Base: gateway.URL}, Key: "admin-secret"}, false)
	previous := subHTTP
	u, _ := url.Parse(upstream.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}}
	defer func() { subHTTP = previous }()
	s := &Service{control: store}
	session, _ := store.newSession(context.Background(), owner)
	foreign, _ := store.newSession(context.Background(), owner+"-foreign")
	request := func(models []string, rev, cookie string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(subImportInput{AccountID: account, GroupID: 7, SourceID: sourceID, KeyID: sourceID + "::key-" + strings.Repeat("a", 64), Revision: rev, Models: models, Position: 1})
		r := httptest.NewRequest("POST", "/v1/sub2api/channels", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request([]string{checkModel}, "boot:1", foreign); w.Code != 409 {
		t.Fatal("foreign import allowed", w.Code)
	}
	if w := request([]string{"gpt-5.6-sol"}, "boot:1", session); w.Code != 400 {
		t.Fatal("untested model allowed", w.Code)
	}
	if created != 0 || imports != 0 {
		t.Fatal("rejected import changed upstream")
	}
	w := request([]string{checkModel}, "boot:1", session)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if imports != 1 || created != 1 || last["api_key"] != "business-secret" || last["position"] != float64(1) || last["base_url"] != "https://site.example/v1/responses" {
		t.Fatal("wrong business route", imports, created)
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("credential leak")
	}
	if w = request([]string{checkModel}, "boot:1", session); w.Code != 409 {
		t.Fatal("stale revision accepted")
	}
	if imports != 1 {
		t.Fatal("stale request reached mutation")
	}
	w = request([]string{checkModel}, "boot:2", session)
	if w.Code != 200 || created != 1 || imports != 2 {
		t.Fatal("duplicate key created", w.Code, w.Body.String())
	}
	var encrypted string
	store.db.QueryRow(`SELECT encrypted_routing_key FROM console_sub_targets WHERE account_id=$1 AND group_id=7`, account).Scan(&encrypted)
	if encrypted == "" || strings.Contains(encrypted, "business-secret") {
		t.Fatal("business key not encrypted")
	}
}

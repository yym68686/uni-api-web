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

func TestSubInstalledChannelsManagementUsesLiveOwnedBindings(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, e := newControlStore(dsn, strings.Repeat("m", 32))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	owner := "management-" + randomID()[:8]
	account := "sub_" + randomID()[:16]
	source := "source-" + randomID()[:8]
	key := "key-" + strings.Repeat("b", 64)
	other := "key-" + strings.Repeat("c", 64)
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, account)
	defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, source)
	routeSecret, _ := store.encrypt("routing-secret")
	auth, _ := store.encrypt(`{"access_token":"panel-secret"}`)
	_, e = store.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$2,'My Site','https://example.com','test@example.com',$3)`, account, owner, auth)
	if e != nil {
		t.Fatal(e)
	}
	_, e = store.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,billing,encrypted_routing_key) VALUES($1,7,'Group','openai','{"rate":0.18}',$2)`, account, routeSecret)
	if e != nil {
		t.Fatal(e)
	}
	provider := subProviderName(account, 7, key)
	otherProvider := subProviderName(account, 7, other)
	revision := "boot:1"
	models := []string{checkModel, "gpt-5.6-sol"}
	deleted := false
	writes := 0
	var last map[string]any
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Error("wrong admin auth")
		}
		switch r.URL.Path {
		case "/v1/api-keys":
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"key_id": key, "prefix": "masked-one", "position": 1}, map[string]any{"key_id": other, "prefix": "masked-two", "position": 2}}})
		case "/v1/channel-controls":
			temporary := []any{map[string]any{"provider": otherProvider, "api_key_id": other, "models": []string{checkModel}}}
			if !deleted {
				temporary = append(temporary, map[string]any{"provider": provider, "api_key_id": key, "models": models})
			}
			writeJSON(w, 200, map[string]any{"instance_id": "boot", "revision": revision, "temporary_channel_management": true, "temporary_channels": temporary, "rules": []any{map[string]any{"api_key_id": key, "model": checkModel, "order": []string{"existing", provider}}}})
		case "/v1/temporary-channels":
			writes++
			json.NewDecoder(r.Body).Decode(&last)
			if last["revision"] != revision {
				http.Error(w, "conflict", 409)
				return
			}
			if last["action"] == "delete" {
				deleted = true
			} else {
				raw, _ := json.Marshal(last["models"])
				json.Unmarshal(raw, &models)
			}
			revision = "boot:" + string(rune('1'+writes))
			writeJSON(w, 200, map[string]any{"instance_id": "boot", "revision": revision})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	if _, e = store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: source, Name: "Gateway", Base: gateway.URL}, Key: "admin-secret"}, false); e != nil {
		t.Fatal(e)
	}
	s := &Service{control: store}
	session, _ := store.newSession(context.Background(), owner)
	foreign, _ := store.newSession(context.Background(), owner+"-foreign")
	request := func(method, cookie string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/v1/sub2api/channels", strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "", nil); w.Code != 401 {
		t.Fatal("anonymous allowed", w.Code)
	}
	w := request("GET", session, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var listing struct {
		Data   []subInstalledChannel        `json:"data"`
		Labels map[string]map[string]string `json:"labels"`
	}
	json.Unmarshal(w.Body.Bytes(), &listing)
	if len(listing.Data) != 2 || listing.Labels[source][provider] != "My Site-0.18" {
		t.Fatal("missing live association", w.Body.String())
	}
	for _, v := range listing.Data {
		if v.Provider == provider && (v.Positions[checkModel] != 2 || v.KeyPrefix != "masked-one" || !v.Manageable) {
			t.Fatal("wrong key/position", v)
		}
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("credentials exposed")
	}
	w = request("GET", foreign, nil)
	json.Unmarshal(w.Body.Bytes(), &listing)
	if len(listing.Data) != 0 {
		t.Fatal("foreign owner bindings exposed")
	}
	readInfo := func(cookie, reveal string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/sources/"+source+"/channel-info?provider="+provider+"&reveal="+reveal, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := readInfo("", "true"); w.Code != 401 {
		t.Fatal("anonymous secret read", w.Code)
	}
	if w := readInfo(foreign, "true"); w.Code != 404 {
		t.Fatal("foreign secret read", w.Code)
	}
	if w := readInfo(session, "false"); w.Code != 200 || strings.Contains(w.Body.String(), "routing-secret") || !strings.Contains(w.Body.String(), "https://example.com/dashboard") {
		t.Fatal("unrequested key exposed or bad URL", w.Code)
	}
	if w := readInfo(session, "true"); w.Code != 200 || !strings.Contains(w.Body.String(), "routing-secret") || strings.Contains(w.Body.String(), "panel-secret") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("wrong scoped credential", w.Code)
	}
	in := subImportInput{Action: "replace", AccountID: account, GroupID: 7, SourceID: source, KeyID: key, Revision: "boot:1", Models: []string{checkModel}, Position: 1}
	if w = request("PATCH", foreign, in); w.Code != 404 {
		t.Fatal("foreign mutation allowed", w.Code)
	}
	in.Models = []string{"gpt-5.5"}
	if w = request("PATCH", session, in); w.Code != 400 {
		t.Fatal("untested new model allowed", w.Code)
	}
	in.Models = []string{checkModel}
	if w = request("PATCH", session, in); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(models) != 1 || last["provider"] != provider || last["action"] != "replace" {
		t.Fatal("wrong replacement", last)
	}
	if w = request("PATCH", session, in); w.Code != 409 {
		t.Fatal("stale edit accepted", w.Code)
	}
	if writes != 1 {
		t.Fatal("rejected request mutated gateway", writes)
	}
	in.Action = "delete"
	in.Revision = revision
	in.Models = nil
	if w = request("PATCH", session, in); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := readInfo(session, "true"); w.Code != 404 {
		t.Fatal("deleted channel still exposes credential", w.Code)
	}
	w = request("GET", session, nil)
	json.Unmarshal(w.Body.Bytes(), &listing)
	if len(listing.Data) != 1 || listing.Data[0].Provider != otherProvider {
		t.Fatal("other binding lost", w.Body.String())
	}
	if _, exists := listing.Labels[source][provider]; exists {
		t.Fatal("deleted channel unnecessarily expanded label response")
	}
	if w = request("PATCH", session, in); w.Code != 409 {
		t.Fatal("old delete revision accepted")
	}
	in.Revision = revision
	if w = request("PATCH", session, in); w.Code != 404 {
		t.Fatal("deleted binding still found")
	}
}

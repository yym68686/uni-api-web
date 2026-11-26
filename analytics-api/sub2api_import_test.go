package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Hold a real PostgreSQL source lock across each HTTP path. A request's
// shorter deadline exercises the wait timeout without making CI wait 15s.
func runContendedControlOperation(t *testing.T, store *controlStore, source, action string, call func(context.Context) *httptest.ResponseRecorder, checkUnchanged func()) *httptest.ResponseRecorder {
	t.Helper()
	unlock, err := store.lockControls(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	release := sync.OnceFunc(unlock)
	defer release()

	short, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	w := call(short)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "等待超时；尚未提交"+action) {
		t.Fatalf("contended operation did not wait until deadline: %d %s", w.Code, w.Body.String())
	}
	checkUnchanged()

	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- call(context.Background()) }()
	select {
	case w = <-finished:
		t.Fatalf("operation completed before the prior writer released the lock: %d %s", w.Code, w.Body.String())
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case w = <-finished:
		return w
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not resume after the source lock was released")
		return nil
	}
}

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
	now := time.Now().Unix()
	raw, _ := json.Marshal(subResult{Model: checkModel, Availability: subProbe{Status: "success", Protocol: "responses"},
		ServiceTiers: map[string]subTierProbe{
			"fast":      {Status: "supported", Observed: "priority", CheckedAt: now},
			"ultrafast": {Status: "supported", Observed: "ultrafast", CheckedAt: now - serviceTierTTL},
		}})
	store.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,7,$2,'done',$3)`, account, checkModel, string(raw))
	remoteKey := subRemoteKey{}
	groupMissing := false
	created := 0
	imports := 0
	revision := "boot:1"
	callerKey := "key-" + strings.Repeat("a", 64)
	provider := subProviderName(account, 7, callerKey)
	rawScope := strings.Repeat("c", 64)
	startSyncOnRead := false
	changedIdentity := false
	exposeInstalled := false
	var last map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		success := func(data any) { writeJSON(w, 200, map[string]any{"code": 0, "data": data}) }
		switch r.URL.Path {
		case "/api/v1/groups/available":
			if groupMissing {
				success([]subRemoteGroup{})
				return
			}
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
		case "/api/v1/keys/1":
			success(map[string]any{"group_id": 7, "group": map[string]any{"id": 7, "status": "inactive"}})
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
		case serviceTierProbePath:
			var input struct {
				Verify    bool   `json:"verify_only"`
				Provider  string `json:"provider"`
				Model     string `json:"model"`
				Candidate *struct {
					APIKey string `json:"api_key"`
				} `json:"candidate"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || !input.Verify || input.Provider != provider {
				t.Error("expected local verification for exact channel", err)
				http.Error(w, "unexpected probe", 400)
				return
			}
			if input.Candidate == nil {
				if input.Model != checkModel {
					t.Errorf("unexpected public model %q", input.Model)
				}
				writeJSON(w, 200, serviceTierResult{Status: "ready", Scope: rawScope, Binding: strings.Repeat("b", 64), UpstreamModel: checkModel})
				return
			}
			if input.Candidate.APIKey != "business-secret" {
				t.Error("expected local binding for the business key")
			}
			writeJSON(w, 200, map[string]any{"models": map[string]serviceTierImportBinding{
				checkModel: {Binding: strings.Repeat("b", 64)},
			}})
		case "/v1/channel-settings/providers":
			writeJSON(w, 200, map[string]any{"providers": []configuredProvider{{
				Provider: provider, Base: "https://site.example/v1/responses", Temporary: true,
				IdentityChanged: changedIdentity, ServiceTierScope: rawScope,
			}}})
		case "/v1/channel-controls":
			if startSyncOnRead {
				startSyncOnRead = false
				if _, err := store.db.Exec(`UPDATE console_sub_accounts SET state='running',job_kind='sync',job_id='racing-sync',lease_until=now()+interval '30 seconds' WHERE id=$1`, account); err != nil {
					t.Error(err)
				}
			}
			temporary := []any{}
			if exposeInstalled {
				temporary = append(temporary, map[string]any{"provider": provider, "api_key_id": callerKey, "models": []string{checkModel}})
			}
			writeJSON(w, 200, map[string]any{"instance_id": "boot", "revision": revision, "temporary_channel_import": true, "temporary_channel_restore": true, "automatic_engine": true, "channel_settings": true, "temporary_channels": temporary})
		case "/v1/channel-settings/export":
			writeJSON(w, 200, map[string]any{"revision": revision, "channel_settings": map[string]any{}, "temporary_definitions": map[string]any{}})
		case "/v1/model-channels":
			writeJSON(w, 200, map[string]any{"data": []any{}})
		case "/v1/channel-controls/restore":
			imports++
			json.NewDecoder(r.Body).Decode(&last)
			if last["revision"] != revision {
				http.Error(w, "conflict", 409)
				return
			}
			revision = "boot:2"
			writeJSON(w, 200, map[string]any{"instance_id": "boot", "revision": revision})
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
	requestContext := func(ctx context.Context, models []string, rev, cookie string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(subImportInput{AccountID: account, GroupID: 7, SourceID: sourceID, KeyID: sourceID + "::key-" + strings.Repeat("a", 64), Revision: rev, Models: models, Position: 1,
			ServiceTierSiteChoices: map[string]bool{"fast": true}})
		r := httptest.NewRequest("POST", "/v1/sub2api/channels", strings.NewReader(string(body))).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	request := func(models []string, rev, cookie string) *httptest.ResponseRecorder {
		return requestContext(context.Background(), models, rev, cookie)
	}
	previewBody := mustJSON(subImportInput{AccountID: account, GroupID: 7, SourceID: sourceID,
		KeyID: sourceID + "::key-" + strings.Repeat("a", 64), Models: []string{checkModel}, Protocols: map[string]string{checkModel: "responses"}})
	preview := httptest.NewRequest("POST", "/v1/sub2api/channel-tier-options", strings.NewReader(previewBody))
	preview.Header.Set("Content-Type", "application/json")
	preview.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
	previewResponse := httptest.NewRecorder()
	s.Handler().ServeHTTP(previewResponse, preview)
	var options struct {
		Verified      map[string]int
		SiteSupported map[string]int `json:"site_supported"`
		Total         int
	}
	if previewResponse.Code != 200 || json.Unmarshal(previewResponse.Body.Bytes(), &options) != nil ||
		options.Total != 1 || options.SiteSupported["fast"] != 1 || len(options.SiteSupported) != 1 ||
		len(options.Verified) != 0 || created != 0 || imports != 0 {
		t.Fatal("first import preview should recommend Fast from the group without creating a key", previewResponse.Code, previewResponse.Body.String())
	}
	if w := request([]string{checkModel}, "boot:1", foreign); w.Code != 404 {
		t.Fatal("foreign import allowed", w.Code)
	}
	if w := request([]string{"gpt-5.6-sol"}, "boot:1", session); w.Code != 400 {
		t.Fatal("untested model allowed", w.Code)
	}
	if created != 0 || imports != 0 {
		t.Fatal("rejected import changed upstream")
	}
	statusRequest := func(cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts/"+account+"/import-status", nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := statusRequest(foreign); w.Code != 404 {
		t.Fatal("foreign status exposed", w.Code)
	}
	for _, state := range []string{"queued", "running"} {
		if _, err := store.db.Exec(`UPDATE console_sub_accounts SET state=$2,job_kind='sync',job_id='sync-job',lease_until=now()+interval '30 seconds' WHERE id=$1`, account, state); err != nil {
			t.Fatal(err)
		}
		w := statusRequest(session)
		var status subImportAccountState
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.CanImport || status.State != state || status.JobKind != "sync" || !strings.Contains(status.Message, "同步检测") {
			t.Fatal("incorrect busy status", w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("status may be cached")
		}
		w = request([]string{checkModel}, "boot:1", session)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "账号正在同步检测") || strings.Contains(w.Body.String(), "不存在") {
			t.Fatal("incorrect busy rejection", w.Code, w.Body.String())
		}
		var afterState, afterJob string
		if err := store.db.QueryRow(`SELECT state,job_id FROM console_sub_accounts WHERE id=$1`, account).Scan(&afterState, &afterJob); err != nil {
			t.Fatal(err)
		}
		if afterState != state || afterJob != "sync-job" || created != 0 || imports != 0 {
			t.Fatal("busy import changed task/key/route", afterState, afterJob, created, imports)
		}
	}
	if _, err := store.db.Exec(`UPDATE console_sub_accounts SET state='idle',job_kind='',job_id='',lease_until=NULL WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	w := statusRequest(session)
	var status subImportAccountState
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || !status.CanImport || status.Message != "" {
		t.Fatal("idle did not recover", w.Code, w.Body.String())
	}
	startSyncOnRead = true
	w = request([]string{checkModel}, "boot:1", session)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "账号正在同步检测") || created != 0 || imports != 0 {
		t.Fatal("concurrent sync not fenced", w.Code, w.Body.String(), created, imports)
	}
	if _, err := store.db.Exec(`UPDATE console_sub_accounts SET state='idle',job_kind='',job_id='',lease_until=NULL WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	w = runContendedControlOperation(t, store, sourceID, "添加", func(ctx context.Context) *httptest.ResponseRecorder {
		return requestContext(ctx, []string{checkModel}, "boot:1", session)
	}, func() {
		var state, job string
		err := store.db.QueryRow(`SELECT state,job_id FROM console_sub_accounts WHERE id=$1`, account).Scan(&state, &job)
		if err != nil || state != "idle" || job != "" || created != 0 || imports != 0 {
			t.Fatal("waiting import modified account, key, or route", err, state, job, created, imports)
		}
	})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var saved retainedSnapshot
	if err := decodeMap(last["snapshot"], &saved); err != nil {
		t.Fatal(err)
	}
	var definition map[string]any
	if len(saved.Channels) != 1 || json.Unmarshal(saved.Channels[0].Definition, &definition) != nil {
		t.Fatal("missing definition")
	}
	if imports != 1 || created != 1 || saved.Channels[0].Key != "business-secret" || definition["engine"] != nil || saved.Channels[0].Base != "https://site.example/v1/responses" {
		t.Fatal("wrong business route", imports, created)
	}
	var preferences struct{ Set map[string]any }
	if err := json.Unmarshal(saved.Settings[saved.Channels[0].Provider], &preferences); err != nil ||
		len(preferences.Set) != 3 || preferences.Set["/preferences/service_tier_disabled/fast"] != false ||
		preferences.Set["/preferences/service_tier_disabled/ultrafast"] != true ||
		preferences.Set["/preferences/service_tier_evidence/"+strings.Repeat("b", 64)+"/fast"] != float64(now) {
		t.Fatal("expected group Fast evidence with only Fast enabled", preferences.Set, err)
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("credential leak")
	}
	exposeInstalled = true
	targetRequest := func(cookie string) (int, map[string]subTierProbe) {
		q := url.Values{"provider": {provider}, "model": {checkModel}}
		r := httptest.NewRequest("GET", "/v1/sources/"+sourceID+"/service-tier-target?"+q.Encode(), nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		var result struct {
			Applicable   bool                    `json:"applicable"`
			Scope        string                  `json:"scope"`
			GroupResults map[string]subTierProbe `json:"group_results"`
		}
		if w.Code == 200 && (json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Applicable || !serviceTierDigest(result.Scope) || result.Scope == rawScope) {
			t.Fatalf("invalid live target: %s", w.Body.String())
		}
		return w.Code, result.GroupResults
	}
	if code, results := targetRequest(session); code != 200 || results["fast"].Status != "supported" || results["fast"].CheckedAt != now {
		t.Fatal("imported channel could not read the result from its own group", code, results)
	}
	if _, results := targetRequest(foreign); len(results) != 0 {
		t.Fatal("foreign group results exposed", results)
	}
	changedIdentity = true
	if code, results := targetRequest(session); code != 200 || len(results) != 0 {
		t.Fatal("new identity must not inherit results from its former group", code, results)
	}
	changedIdentity = false
	exposeInstalled = false
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
	groupMissing = true
	if w = request([]string{checkModel}, "boot:2", session); w.Code != 409 || !strings.Contains(w.Body.String(), "站点已停用") {
		t.Fatal(w.Code, w.Body.String())
	}
	if created != 1 || imports != 2 {
		t.Fatal("unavailable group mutated gateway or key", created, imports)
	}
	var active bool
	store.db.QueryRow(`SELECT active FROM console_sub_targets WHERE account_id=$1 AND group_id=7`, account).Scan(&active)
	if active {
		t.Fatal("unavailable group stayed active")
	}
	var encrypted string
	store.db.QueryRow(`SELECT encrypted_routing_key FROM console_sub_targets WHERE account_id=$1 AND group_id=7`, account).Scan(&encrypted)
	if encrypted == "" || strings.Contains(encrypted, "business-secret") {
		t.Fatal("business key not encrypted")
	}
}

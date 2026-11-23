package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestConfiguredCheckProgressReturnsOnlyScopedActiveIdentities(t *testing.T) {
	s, account, source := bindingFixture(t, "https://fixture.example")
	disabled := source
	disabled.ID += "-disabled"
	if _, err := s.control.saveSource(context.Background(), disabled, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_sources WHERE id=$1`, disabled.ID) })
	if _, err := s.control.db.Exec(`UPDATE console_sources SET enabled=false WHERE id=$1`, disabled.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_configured_checks
 (source_id,provider,kind,model,source_target,state,deadline,fingerprint,result)
 VALUES
 ($1,'native','model','argon','fixture','queued',NULL,'','{"private":"history"}'),
 ($1,'native','tool-use','sol','fixture','running',now()+interval '10 minutes','rev',NULL),
 ($1,'native','model','carbon','fixture','running',now()-interval '10 minutes','',NULL),
 ($1,'native','model','neon','fixture','done',NULL,'',NULL),
 ($1,'another','model','argon','fixture','queued',NULL,'',NULL),
 ($2,'native','model','argon','fixture','queued',NULL,'',NULL)`, source.ID, disabled.ID); err != nil {
		t.Fatal(err)
	}
	token, err := s.control.newSession(context.Background(), account.Owner)
	if err != nil {
		t.Fatal(err)
	}
	request := func(raw string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/checks/progress?"+url.Values{"targets": {raw}}.Encode(), nil)
		if authenticated {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	native := configuredCheckScope{Source: source.ID, Provider: "native"}
	scope := mustJSON([]configuredCheckScope{native, native, {Source: disabled.ID, Provider: "native"}})
	if w := request(scope, false); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("[]", true); w.Code != http.StatusBadRequest {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request(scope, true)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "private, no-store" || strings.Contains(w.Body.String(), "history") {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct{ Data []configuredActiveCheck }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	got := map[string]configuredActiveCheck{}
	for _, check := range result.Data {
		got[check.Model] = check
	}
	want := map[string]configuredActiveCheck{
		"argon": {configuredCheckScope: native, Kind: "model", Model: "argon"},
		"sol":   {configuredCheckScope: native, Kind: "tool-use", Model: "sol", Fingerprint: "rev"},
	}
	if len(result.Data) != len(want) || !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected active checks: %+v", result.Data)
	}
	if _, err := s.control.db.Exec(`UPDATE console_configured_checks SET state='done' WHERE source_id=$1 AND provider='native'`, source.ID); err != nil {
		t.Fatal(err)
	}
	if w := request(scope, true); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"data":[]}` {
		t.Fatal(w.Code, w.Body.String())
	}
}

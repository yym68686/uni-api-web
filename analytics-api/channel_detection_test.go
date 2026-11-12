package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func detectionRequest(t *testing.T, s *Service, owner, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(mustJSON(body)))
	r.Header.Set("Content-Type", "application/json")
	if owner != "" {
		token, err := s.control.newSession(context.Background(), owner)
		if err != nil {
			t.Fatal(err)
		}
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func detectionGroupFixture(t *testing.T, s *Service, account string) {
	t.Helper()
	key, err := s.control.encrypt("fixture-key")
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range []int{1, 2} {
		if _, err = s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,active,state,encrypted_key) VALUES($1,$2,'same-name','openai',true,'done',$3)`, account, group, key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChannelDetectionSettingsArePersistentOwnerScopedAndAtomic(t *testing.T) {
	s, a, src := bindingFixture(t, "https://fixture.test")
	detectionGroupFixture(t, s, a.ID)
	ctx := context.Background()
	if err := s.saveManagementSnapshot(ctx, src.ID, managementSnapshot{Providers: map[string]managementProvider{"native": {}}, CheckedAt: 1}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.control.db.Exec(`DELETE FROM console_channel_detection_settings WHERE owner IN ($1,$2)`, a.Owner, "foreign-"+a.Owner)
	})
	path := "/v1/channel-management/detection-settings"
	group := detectionTarget{Account: a.ID, Group: 1}
	native := detectionTarget{Source: src.ID, Provider: "native"}
	patch := func(owner string, enabled bool, targets ...detectionTarget) *httptest.ResponseRecorder {
		return detectionRequest(t, s, owner, "PATCH", path, map[string]any{"enabled": enabled, "targets": targets})
	}
	if w := detectionRequest(t, s, "", "GET", path, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := detectionRequest(t, s, a.Owner, "GET", path, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"disabled":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := patch("foreign-"+a.Owner, false, group); w.Code != 404 {
		t.Fatal("foreign group accepted", w.Code)
	}
	if w := patch(a.Owner, false, group, detectionTarget{Account: a.ID, Group: 99}); w.Code != 404 {
		t.Fatal(w.Code)
	}
	disabled, _, err := readDisabledDetection(ctx, s.control.db, a.Owner)
	if err != nil || len(disabled) != 0 {
		t.Fatal("partial settings committed", disabled, err)
	}
	if w := patch(a.Owner, false, group, native); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	fresh := &Service{control: s.control}
	w := detectionRequest(t, fresh, a.Owner, "GET", path, nil)
	var out struct {
		Disabled []detectionTarget `json:"disabled"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); w.Code != 200 || err != nil || len(out.Disabled) != 2 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	disabled, _, err = readDisabledDetection(ctx, s.control.db, a.Owner)
	if err != nil || !disabled[group.scope()] || !disabled[native.scope()] || disabled[(detectionTarget{Account: a.ID, Group: 2}).scope()] {
		t.Fatal(disabled, err)
	}
	foreign, _, err := readDisabledDetection(ctx, s.control.db, "foreign-"+a.Owner)
	if err != nil || len(foreign) != 0 {
		t.Fatal("owner isolation", foreign, err)
	}
	if w = patch(a.Owner, true, group); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	disabled, _, err = readDisabledDetection(ctx, s.control.db, a.Owner)
	if err != nil || disabled[group.scope()] || !disabled[native.scope()] {
		t.Fatal(disabled, err)
	}
	// Configured channels with identical names in another source are independent.
	if disabled[(detectionTarget{Source: "other", Provider: "native"}).scope()] {
		t.Fatal("source identity collision")
	}
	for _, body := range []any{map[string]any{"targets": []detectionTarget{group}}, map[string]any{"enabled": false, "targets": []detectionTarget{{Account: a.ID, Group: 1, Provider: "mixed"}}}} {
		if w = detectionRequest(t, s, a.Owner, "PATCH", path, body); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestChannelDetectionAllBatchKindsSkipDisabledAndManualOverrides(t *testing.T) {
	for _, kind := range []string{"check", "quality", "compaction", "tool_use"} {
		t.Run(kind, func(t *testing.T) {
			s, account, owner := subUsageTestService(t)
			detectionGroupFixture(t, s, account)
			t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_channel_detection_settings WHERE owner=$1`, owner) })
			off := detectionTarget{Account: account, Group: 1}
			if w := detectionRequest(t, s, owner, "PATCH", "/v1/channel-management/detection-settings", map[string]any{"enabled": false, "targets": []detectionTarget{off}}); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			// Retain an active lease so admission can be inspected without executing work.
			if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET state='running',job_kind='check',job_id='existing',lease_until=now()+interval '1 minute' WHERE id=$1`, account); err != nil {
				t.Fatal(err)
			}
			route := map[string]string{"check": "checks", "quality": "quality-checks", "compaction": "compaction-checks", "tool_use": "tool-use-checks"}[kind]
			targets := []subSelection{{AccountID: account, GroupID: 1, Models: []string{checkModel}}, {AccountID: account, GroupID: 2, Models: []string{checkModel}}}
			w := detectionRequest(t, s, owner, "POST", "/v1/sub2api/"+route, map[string]any{"targets": targets})
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			var count int
			var group int64
			if err := s.control.db.QueryRow(`SELECT count(*),min(group_id) FROM console_sub_check_queue WHERE account_id=$1`, account).Scan(&count, &group); err != nil || count != 1 || group != 2 {
				t.Fatal("batch admitted disabled channel", count, group, err)
			}
			w = detectionRequest(t, s, owner, "POST", "/v1/sub2api/"+route, map[string]any{"manual": true, "targets": targets[:1]})
			if w.Code != 202 {
				t.Fatal("manual override refused", w.Code, w.Body.String())
			}
			if err := s.control.db.QueryRow(`SELECT count(*) FROM console_sub_check_queue WHERE account_id=$1`, account).Scan(&count); err != nil || count != 2 {
				t.Fatal("manual not queued", count, err)
			}
			// Toggling does not cancel previously submitted tasks or modify routing.
			if w = detectionRequest(t, s, owner, "PATCH", "/v1/channel-management/detection-settings", map[string]any{"enabled": true, "targets": []detectionTarget{off}}); w.Code != 200 {
				t.Fatal(w.Code)
			}
			if err := s.control.db.QueryRow(`SELECT count(*) FROM console_sub_check_queue WHERE account_id=$1`, account).Scan(&count); err != nil || count != 2 {
				t.Fatal("setting canceled tasks", count, err)
			}
		})
	}
}

func TestConfiguredDetectionBatchSkipsDisabledButAllowsExplicitManual(t *testing.T) {
	for _, kind := range []string{"check", "quality", "compaction", "tool-use"} {
		t.Run(kind, func(t *testing.T) {
			s, a, src := bindingFixture(t, "https://fixture.test")
			t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_channel_detection_settings WHERE owner=$1`, a.Owner) })
			if err := s.saveManagementSnapshot(context.Background(), src.ID, managementSnapshot{Providers: map[string]managementProvider{"off": {}, "on": {}}, CheckedAt: 1}); err != nil {
				t.Fatal(err)
			}
			if w := detectionRequest(t, s, a.Owner, "PATCH", "/v1/channel-management/detection-settings", map[string]any{"enabled": false, "targets": []detectionTarget{{Source: src.ID, Provider: "off"}}}); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			targets := []any{map[string]any{"source_id": src.ID, "provider": "off", "models": []string{checkModel}}, map[string]any{"source_id": src.ID, "provider": "on", "models": []string{checkModel}}}
			w := detectionRequest(t, s, a.Owner, "POST", "/v1/channel-management/checks", map[string]any{"kind": kind, "targets": targets})
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			var count int
			var provider string
			if err := s.control.db.QueryRow(`SELECT count(*),min(provider) FROM console_configured_checks WHERE source_id=$1`, src.ID).Scan(&count, &provider); err != nil || count != 1 || provider != "on" {
				t.Fatal(count, provider, err)
			}
			w = detectionRequest(t, s, a.Owner, "POST", "/v1/channel-management/checks", map[string]any{"kind": kind, "manual": true, "targets": targets[:1]})
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			if err := s.control.db.QueryRow(`SELECT count(*) FROM console_configured_checks WHERE source_id=$1`, src.ID).Scan(&count); err != nil || count != 2 {
				t.Fatal(count, err)
			}
		})
	}
}

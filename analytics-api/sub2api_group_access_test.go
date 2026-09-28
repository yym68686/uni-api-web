package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGroupAccessRequiresDirectoryAndExplainsInactiveGroup(t *testing.T) {
	for _, tc := range []struct {
		name, detail, status string
		groups               []subRemoteGroup
		wantErr              bool
	}{
		{"available", "", "available", []subRemoteGroup{{ID: 49}}, false},
		{"disabled", `{"group_id":49,"group":{"id":49,"status":"inactive"}}`, "disabled", []subRemoteGroup{}, false},
		{"active key is not permission", `{"group_id":49,"status":"active","group":{"id":49,"status":"active"}}`, "unavailable", []subRemoteGroup{}, false},
		{"different group", `{"group_id":50,"group":{"id":50,"status":"inactive"}}`, "unavailable", []subRemoteGroup{}, false},
		{"key unavailable", "", "unavailable", []subRemoteGroup{}, false},
		{"malformed directory", "", "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readSubGroupAccess(tc.groups, 49, 41369, func(path string, out any) error {
				if path != "/api/v1/keys/41369" {
					t.Fatal(path)
				}
				if tc.detail == "" {
					return errors.New("unavailable")
				}
				return json.Unmarshal([]byte(tc.detail), out)
			})
			if (err != nil) != tc.wantErr || got.Status != tc.status || got.Available != (tc.status == "available") {
				t.Fatal(got, err)
			}
		})
	}
}

func TestGroupAccessRefreshesOnlyMetadataAndProtectsOwnership(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	ctx := context.Background()
	_, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,encrypted_key) VALUES($1,49,'CCMAX','anthropic',41369,'fixture')`, account)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,49,'claude-opus-5','done','{"availability":{"status":"success"}}')`, account)
	if err != nil {
		t.Fatal(err)
	}
	available, failed, calls := false, false, 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			t.Error("unexpected upstream mutation")
		}
		if failed {
			http.Error(w, "offline", 503)
			return
		}
		var data any
		switch r.URL.Path {
		case "/api/v1/groups/available":
			groups := []subRemoteGroup{}
			if available {
				groups = append(groups, subRemoteGroup{ID: 49, Status: "active"})
			}
			data = groups
		case "/api/v1/keys/41369":
			data = map[string]any{"group_id": 49, "group": map[string]any{"id": 49, "status": "inactive"}}
		default:
			t.Error("unexpected read", r.URL.Path)
		}
		writeJSON(w, 200, map[string]any{"code": 0, "data": data})
	}))
	defer up.Close()
	old := subHTTP
	u, _ := url.Parse(up.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}}
	defer func() { subHTTP = old }()
	session, _ := s.control.newSession(ctx, owner)
	foreign, _ := s.control.newSession(ctx, owner+"-foreign")
	request := func(cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/sub2api/accounts/"+account+"/groups/49/access", nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request(foreign); w.Code != 404 || calls != 0 {
		t.Fatal("foreign access", w.Code, calls)
	}
	if w := request(session); w.Code != 200 || !strings.Contains(w.Body.String(), "站点已停用") {
		t.Fatal(w.Code, w.Body.String())
	}
	var active bool
	var result string
	s.control.db.QueryRow(`SELECT active FROM console_sub_targets WHERE account_id=$1 AND group_id=49`, account).Scan(&active)
	s.control.db.QueryRow(`SELECT result->'availability'->>'status' FROM console_sub_models WHERE account_id=$1 AND group_id=49`, account).Scan(&result)
	if active || result != "success" {
		t.Fatal("discovery or history changed incorrectly", active, result)
	}
	available = true
	if w := request(session); w.Code != 200 || !strings.Contains(w.Body.String(), `"available":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	failed = true
	if w := request(session); w.Code != 502 {
		t.Fatal(w.Code)
	}
	s.control.db.QueryRow(`SELECT active FROM console_sub_targets WHERE account_id=$1 AND group_id=49`, account).Scan(&active)
	if !active {
		t.Fatal("network failure removed group")
	}
	// A late response cannot undo a newer account synchronization.
	s.control.db.Exec(`UPDATE console_sub_accounts SET synced_at=2 WHERE id=$1`, account)
	if err := s.saveSubGroupAccess(ctx, account, 49, "", 0, subGroupAccess{Status: "disabled", Message: subGroupDisabledMessage}); err != nil {
		t.Fatal(err)
	}
	s.control.db.QueryRow(`SELECT active FROM console_sub_targets WHERE account_id=$1 AND group_id=49`, account).Scan(&active)
	if !active {
		t.Fatal("stale refresh overwrote newer sync")
	}
}

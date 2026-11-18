package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func subAddressLogin(t *testing.T, s *Service, account, owner string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	token, err := s.control.newSession(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/v1/sub2api/accounts/"+account+"/login", strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestSubAddressChangeVerifiesThenInvalidatesCurrentEvidence(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	for _, statement := range []string{
		`UPDATE console_sub_accounts SET synced_at=123,balance='{"status":"ok"}',encrypted_login_password='old-cipher',job_id='old-job' WHERE id=$1`,
		`INSERT INTO console_sub_targets(account_id,group_id,name,platform,remote_key_id,encrypted_key,encrypted_routing_key,routing_key_id,state,result,billing,compaction,tool_use)
SELECT $1,g,'old group','openai',42,'old-key','route-key',43,'done','{}','{}','{}','{}' FROM generate_series(1,32) g`,
		// Match the size of the account that timed out. Both the summary and
		// signal projections must be removed, not regenerated for old models.
		`INSERT INTO console_sub_models(account_id,group_id,model,state,result)
SELECT $1,1+(i-1)%32,'old-model-'||i,'done',CASE WHEN i<=5582 THEN '{"checked_at":123,"availability":{"status":"success"}}'::jsonb END
FROM generate_series(1,6475) i`,
		`INSERT INTO console_quality_history(check_id,account_id,group_id,checked_at,verdict) VALUES($1,$1,7,123,'pass')`,
		`INSERT INTO console_model_fingerprints(scope,model,account_id,group_id,state,result,sample_state,run_id,revision) VALUES($1,'gpt-6-astra',$1,7,'running','{}','{}','old-run','old-revision')`,
		`INSERT INTO console_sub_key_scans(account_id,revision) VALUES($1,'old')`,
		`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES($1,'hash',42,7)`,
		`INSERT INTO console_recharge_settings(account_id,revision,config) VALUES($1,'old-revision','{"enabled":true,"target_base":"https://usage.example"}')`,
	} {
		if _, err := s.control.db.Exec(statement, account); err != nil {
			t.Fatal(err)
		}
	}
	var calls int
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Host != "new.example" {
			t.Error("login reached the old site", r.Host)
		}
		switch r.URL.Path {
		case "/api/v1/auth/login":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["password"] != "fresh-password" || in["email"] != "fixture@example.com" {
				t.Error("incorrect destination credentials")
			}
			writeJSON(w, 200, map[string]any{"code": 0, "data": subAuth{Access: "new-access", Refresh: "new-refresh"}})
		case "/api/v1/auth/me":
			writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]string{"email": "fixture@example.com"}})
		default:
			t.Error("unexpected sync/probe", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	w := subAddressLogin(t, s, account, owner, map[string]any{"previous_base": "https://usage.example", "base": "https://new.example/v1/", "password": "fresh-password"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"address_changed":true`) || calls != 2 {
		t.Fatal(w.Code, w.Body.String(), calls)
	}
	var base, encrypted, password, job string
	var synced int64
	var balanceMissing bool
	if err := s.control.db.QueryRow(`SELECT base,encrypted_auth,encrypted_login_password,job_id,synced_at,balance IS NULL FROM console_sub_accounts WHERE id=$1`, account).Scan(&base, &encrypted, &password, &job, &synced, &balanceMissing); err != nil {
		t.Fatal(err)
	}
	plain, err := s.control.decrypt(encrypted)
	if err != nil || !strings.Contains(plain, "new-access") || base != "https://new.example" || synced != 0 || !balanceMissing || job == "old-job" {
		t.Fatal("new identity was not atomically saved")
	}
	plain, err = s.control.decrypt(password)
	if err != nil || plain != "fresh-password" {
		t.Fatal("destination password not retained")
	}
	var valid bool
	if err = s.control.db.QueryRow(`SELECT count(*)=32 AND bool_and(NOT active AND encrypted_key='' AND remote_key_id=0 AND encrypted_routing_key='' AND routing_key_id=0 AND result IS NULL AND billing IS NULL AND compaction IS NULL AND tool_use IS NULL) FROM console_sub_targets WHERE account_id=$1`, account).Scan(&valid); err != nil || !valid {
		t.Fatal("stale target evidence retained", err)
	}
	if err = s.control.db.QueryRow(`SELECT state='interrupted' AND result IS NULL AND sample_state IS NULL AND revision='' AND run_id='' FROM console_model_fingerprints WHERE account_id=$1`, account).Scan(&valid); err != nil || !valid {
		t.Fatal("stale fingerprint evidence retained", err)
	}
	if err = s.control.db.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM console_sub_models WHERE account_id=$1)
AND NOT EXISTS(SELECT 1 FROM console_sub_model_summaries WHERE account_id=$1)
AND NOT EXISTS(SELECT 1 FROM console_management_model_signal WHERE account_id=$1)`, account).Scan(&valid); err != nil || !valid {
		t.Fatal("stale model result retained", err)
	}
	if err = s.control.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM console_quality_history WHERE account_id=$1) AND NOT EXISTS(SELECT 1 FROM console_sub_key_index WHERE account_id=$1) AND NOT EXISTS(SELECT 1 FROM console_sub_key_scans WHERE account_id=$1) AND EXISTS(SELECT 1 FROM console_recharge_settings WHERE account_id=$1 AND revision<>'old-revision')`, account).Scan(&valid); err != nil || !valid {
		t.Fatal("history, key cache or quote invalidation incorrect", err)
	}
	if _, err = s.subUsageAuth(context.Background(), account, "https://usage.example", ""); err == nil {
		t.Fatal("old site could read destination credentials")
	}
	// Synchronization can repopulate models for a reused group ID without
	// reviving a stale result. History remains attached to the retained group.
	ctx := withSubSyncModels(context.Background(), []string{"gpt-6-astra"})
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = queueSubSyncModels(ctx, tx, account, []subRemoteGroup{{ID: 7}}, nil); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.control.db.QueryRow(`SELECT state='queued' AND result IS NULL FROM console_sub_models WHERE account_id=$1 AND group_id=7 AND model='gpt-6-astra'`, account).Scan(&valid); err != nil || !valid {
		t.Fatal("sync could not rebuild the new site's model catalog", err)
	}
}

func TestSubAddressChangeRejectsStaleUnsafeMissingAndFailedCredentials(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	password, _ := s.control.encrypt("must-not-be-forwarded")
	_, err := s.control.db.Exec(`UPDATE console_sub_accounts SET encrypted_login_password=$2 WHERE id=$1`, account, password)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(401) }))
	for _, tc := range []struct {
		previous, base, password, owner string
		status                          int
	}{
		{"https://stale.example", "https://new.example", "test", owner, 409},
		{"https://usage.example", "https://127.0.0.1", "test", owner, 400},
		{"https://usage.example", "https://new.example", "", owner, 400},
		{"https://usage.example", "https://new.example", "test", owner + "-foreign", 404},
	} {
		w := subAddressLogin(t, s, account, tc.owner, map[string]any{"previous_base": tc.previous, "base": tc.base, "password": tc.password})
		if w.Code != tc.status || calls != 0 {
			t.Fatal("invalid edit reached upstream", w.Code, w.Body.String(), calls)
		}
	}
	w := subAddressLogin(t, s, account, owner, map[string]any{"previous_base": "https://usage.example", "base": "https://new.example", "password": "bad"})
	if w.Code != 400 || calls != 1 {
		t.Fatal(w.Code, w.Body.String(), calls)
	}
	var unchanged bool
	if err = s.control.db.QueryRow(`SELECT base='https://usage.example' AND encrypted_login_password=$2 FROM console_sub_accounts WHERE id=$1`, account, password).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("failed login modified the saved account", err)
	}
}

func TestSubAddressChangeTOTPRemainsBoundToOriginalAccountBase(t *testing.T) {
	s, account, owner := subUsageTestService(t)
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			writeJSON(w, 200, map[string]any{"code": 0, "data": subAuth{Requires2FA: true, Temp: "temporary"}})
		case "/api/v1/auth/login/2fa":
			writeJSON(w, 200, map[string]any{"code": 0, "data": subAuth{Access: "verified", Refresh: "refresh"}})
		case "/api/v1/auth/me":
			writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]string{"email": "fixture@example.com"}})
		default:
			t.Error("unexpected call", r.URL.Path)
		}
	}))
	w := subAddressLogin(t, s, account, owner, map[string]any{"previous_base": "https://usage.example", "base": "https://new.example", "password": "test"})
	var response struct {
		Challenge string `json:"challenge"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Challenge == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	// A concurrent edit must make this outstanding challenge unusable.
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET base='https://concurrent.example' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	w = subAddressLogin(t, s, account, owner, map[string]any{"challenge": response.Challenge, "totp_code": "123456"})
	if w.Code != 400 {
		t.Fatal("stale challenge accepted", w.Code)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET base='https://usage.example' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	w = subAddressLogin(t, s, account, owner, map[string]any{"challenge": response.Challenge, "totp_code": "123456"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"base":"https://new.example"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestNewAPIBrowserAddressChangeVerifiesDestination(t *testing.T) {
	s, account, owner := newAPITestService(t)
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "new.example" {
			t.Error("browser verification reached the old site", r.Host)
		}
		switch r.URL.Path {
		case "/api/user/self":
			newAPIOK(w, map[string]any{"id": 7, "username": "fixture", "quota": 1000})
		case "/api/status":
			newAPIOK(w, map[string]any{"quota_per_unit": 500000})
		default:
			t.Error("unexpected call", r.URL.Path)
		}
	}))
	w := subAddressLogin(t, s, account, owner, map[string]any{"previous_base": "https://usage.example", "base": "https://new.example", "browser_kind": "newapi", "browser_user_id": 7, "browser_username": "fixture", "browser_cookies": map[string]string{"session": "fresh-session"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"address_changed":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, _, err := s.newAPIAuthAt(context.Background(), account, "https://usage.example"); err == nil {
		t.Fatal("stale New API request read destination auth")
	}
}

func TestSubAddressChangeConflictsPreserveOriginalAccount(t *testing.T) {
	for _, conflict := range []string{"duplicate", "payment", "concurrent"} {
		t.Run(conflict, func(t *testing.T) {
			s, account, owner := subUsageTestService(t)
			if _, err := s.control.db.Exec(`INSERT INTO console_recharge_settings(account_id,revision,config) VALUES($1,'original','{}')`, account); err != nil {
				t.Fatal(err)
			}
			switch conflict {
			case "duplicate":
				other := account + "-other"
				if _, err := s.control.db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) SELECT $2,owner,name,'https://new.example',email,encrypted_auth FROM console_sub_accounts WHERE id=$1`, account, other); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, other) })
			case "payment":
				if _, err := s.control.db.Exec(`INSERT INTO console_recharge_jobs(id,owner,account_id,quote_id,idempotency_key,state,public,encrypted_context) VALUES($1,$2,$1,$1,$1,'review','{}','fixture')`, account, owner); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_recharge_jobs WHERE account_id=$1`, account) })
			}
			withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/login":
					writeJSON(w, 200, map[string]any{"code": 0, "data": subAuth{Access: "fresh"}})
				case "/api/v1/auth/me":
					if conflict == "concurrent" {
						if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET base='https://concurrent.example' WHERE id=$1`, account); err != nil {
							t.Error(err)
						}
					}
					writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]string{"email": "fixture@example.com"}})
				default:
					t.Error("unexpected call", r.URL.Path)
				}
			}))
			w := subAddressLogin(t, s, account, owner, map[string]any{"previous_base": "https://usage.example", "base": "https://new.example", "password": "test"})
			if w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
			wantBase := "https://usage.example"
			if conflict == "concurrent" {
				wantBase = "https://concurrent.example"
			}
			var valid bool
			if err := s.control.db.QueryRow(`SELECT a.base=$2 AND c.revision='original' FROM console_sub_accounts a JOIN console_recharge_settings c ON c.account_id=a.id WHERE a.id=$1`, account, wantBase).Scan(&valid); err != nil || !valid {
				t.Fatal("conflict did not roll back address/quote changes", err)
			}
		})
	}
}

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newAPITestService(t *testing.T) (*Service, string, string) {
	t.Helper()
	s, account, owner := subUsageTestService(t)
	raw, _ := json.Marshal(subAuth{Kind: "newapi", UserID: 7, Username: "fixture", Access: "expired-access", Cookies: map[string]string{"new_api_refresh": "refresh-fixture"}, QuotaPerUnit: 500000})
	enc, _ := s.control.encrypt(string(raw))
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET provider_kind='newapi',login_name='fixture',email='fixture',encrypted_auth=$2 WHERE id=$1`, account, enc); err != nil {
		t.Fatal(err)
	}
	return s, account, owner
}
func newAPIOK(w http.ResponseWriter, v any) {
	writeJSON(w, 200, map[string]any{"success": true, "data": v})
}
func TestNewAPIAutoLoginAndEncryptedPassword(t *testing.T) {
	s, _, owner := newAPITestService(t)
	ctx := context.Background()
	token, _ := s.control.newSession(ctx, owner)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	public := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	var gotLogin atomic.Int32
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			if r.Header.Get("Authorization") != "" {
				t.Error("credentials sent to detection")
			}
			newAPIOK(w, map[string]any{"version": "fixture", "quota_per_unit": 500000})
		case "/api/user/login/encryption-key":
			newAPIOK(w, map[string]any{"enabled": true, "kid": "key1", "public_key": public})
		case "/api/user/login":
			gotLogin.Add(1)
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["username"] != "fixture-user" || body["password"] != "" || body["encryption_key_id"] != "key1" {
				t.Error("wrong login payload")
			}
			raw, e := base64.StdEncoding.DecodeString(body["password_encrypted"])
			if e != nil {
				t.Error(e)
			}
			plain, e := rsa.DecryptOAEP(sha256.New(), rand.Reader, key, raw, nil)
			if e != nil || string(plain) != "test-pass" {
				t.Error("password encryption failed")
			}
			http.SetCookie(w, &http.Cookie{Name: "new_api_refresh", Value: "refresh-secret", HttpOnly: true, Path: "/api/user/auth"})
			newAPIOK(w, map[string]any{"access_token": "new-access", "access_expires_at": time.Now().Add(time.Hour).Unix(), "user": map[string]any{"id": 42, "username": "fixture-user"}, "session": map[string]string{"sid": "sid"}})
		case "/api/user/self":
			if r.Header.Get("Authorization") != "Bearer new-access" || r.Header.Get("New-Api-User") != "42" {
				t.Error("wrong session identity")
			}
			newAPIOK(w, map[string]any{"id": 42, "username": "fixture-user", "quota": 10000})
		default:
			t.Error("wrong endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	req := httptest.NewRequest("POST", "/v1/sub2api/accounts", strings.NewReader(`{"base":"https://newapi.example","username":"fixture-user","password":"test-pass"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_sub_accounts WHERE id=$1`, response.ID) })
	var kind, username, encrypted string
	s.control.db.QueryRow(`SELECT provider_kind,login_name,encrypted_auth FROM console_sub_accounts WHERE id=$1`, response.ID).Scan(&kind, &username, &encrypted)
	if kind != "newapi" || username != "fixture-user" || strings.Contains(encrypted, "test-pass") || strings.Contains(w.Body.String(), "secret") || gotLogin.Load() != 1 {
		t.Fatal("bad account persistence")
	}
	auth, _, e := s.newAPIAuth(ctx, response.ID)
	if e != nil || auth.Cookies["new_api_refresh"] != "refresh-secret" || auth.UserID != 42 {
		t.Fatal("auth not retained", e)
	}
}
func TestNewAPIRefreshIsAccountScopedAndOnlyOnce(t *testing.T) {
	s, account, _ := newAPITestService(t)
	var refreshes atomic.Int32
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/auth/refresh" {
			refreshes.Add(1)
			c, e := r.Cookie("new_api_refresh")
			if e != nil || c.Value != "refresh-fixture" || r.Header.Get("Origin") != "https://usage.example" {
				t.Error("bad refresh cookie/origin")
			}
			http.SetCookie(w, &http.Cookie{Name: "new_api_refresh", Value: "rotated"})
			newAPIOK(w, map[string]any{"access_token": "fresh", "user": map[string]any{"id": 7, "username": "fixture"}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(401)
			return
		}
		newAPIOK(w, map[string]any{"id": 7, "username": "fixture"})
	}))
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var profile newAPIUser
			if err := s.newAPICall(context.Background(), account, "https://usage.example", "GET", "/api/user/self", nil, &profile); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatal("refresh raced", refreshes.Load())
	}
	auth, _, _ := s.newAPIAuth(context.Background(), account)
	if auth.Cookies["new_api_refresh"] != "rotated" {
		t.Fatal("rotation not persisted")
	}
}
func TestNewAPIKeysUnlimitedStableGroupsAndInterruptedCreation(t *testing.T) {
	s, account, _ := newAPITestService(t)
	ctx := context.Background()
	var created atomic.Int32
	var remoteKey newAPIToken
	var mu sync.Mutex
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/api/user/self/groups":
			newAPIOK(w, map[string]any{"default": map[string]any{"ratio": 0.25}, "vip name": map[string]any{"ratio": 2}, "auto": map[string]any{"ratio": "自动"}})
		case r.URL.Path == "/api/user/self":
			newAPIOK(w, map[string]any{"id": 7, "username": "fixture"})
		case r.URL.Path == "/api/token/search":
			items := []newAPIToken{}
			if remoteKey.ID > 0 {
				items = append(items, remoteKey)
			}
			newAPIOK(w, map[string]any{"items": items, "total": len(items)})
		case r.URL.Path == "/api/token/" && r.Method == "POST":
			created.Add(1)
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["unlimited_quota"] != true || in["expired_time"] != float64(-1) || in["group"] != "default" || in["model_limits_enabled"] != false {
				t.Error("wrong quota/group")
			}
			remoteKey = newAPIToken{ID: 51, UserID: 7, Name: in["name"].(string), Group: "default", Key: "sk-****", Status: 1, Unlimited: true, Expires: -1}
			w.WriteHeader(502) // mutation applied, reply lost
		case r.URL.Path == "/api/token/51/key":
			newAPIOK(w, map[string]string{"key": "full-fixture-key"})
		default:
			t.Error("unexpected", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	groups, err := s.newAPIGroups(ctx, account, "https://usage.example")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, g := range groups {
		if g.Name == "default" {
			id = g.ID
		}
	}
	again, err := s.newAPIGroups(ctx, account, "https://usage.example")
	if err != nil || mustJSON(groups) != mustJSON(again) {
		t.Fatal("unstable group IDs")
	}
	key, err := s.newAPIEnsureKey(ctx, account, "https://usage.example", subKeyName(account, id), id)
	if err != nil || key.Key != "sk-full-fixture-key" || key.Quota != nil {
		t.Fatal(key, err)
	}
	if _, err = s.newAPIEnsureKey(ctx, account, "https://usage.example", subKeyName(account, id), id); err != nil || created.Load() != 1 {
		t.Fatal("duplicated key", err, created.Load())
	}
	// A missing result after prior intent must not blindly POST again.
	mu.Lock()
	remoteKey = newAPIToken{}
	mu.Unlock()
	if _, err = s.newAPIEnsureKey(ctx, account, "https://usage.example", subKeyName(account, id), id); err == nil || created.Load() != 1 {
		t.Fatal("recreated uncertain key")
	}
}
func TestNewAPIUsageUsesStableRequestIdentityAndExpression(t *testing.T) {
	s, account, _ := newAPITestService(t)
	var display atomic.Int64
	display.Store(1)
	expr := `len <= 272000 ? tier("standard", p * 10 + c * 50 + cr * 1 + cc * 12.5) : tier("long_context", p * 20 + c * 75 + cr * 2 + cc * 25)`
	other := mustJSON(map[string]any{"billing_source": "wallet", "billing_mode": "tiered_expr", "expr_b64": base64.StdEncoding.EncodeToString([]byte(expr)), "matched_tier": "standard", "group_ratio": 0.25})
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") == "6" {
			newAPIOK(w, map[string]any{"items": []any{}, "total": 0})
			return
		}
		if r.URL.Path != "/api/log/self" || r.URL.Query().Get("type") != "2" || r.URL.Query().Get("page_size") != "100" {
			t.Error("wrong log query", r.URL.String())
		}
		newAPIOK(w, map[string]any{"total": 1, "items": []newAPILog{{ID: display.Load(), Type: 2, Key: 51, Group: "default", Request: "stable-id", Model: "gpt-6-astra", At: time.Now().Unix(), Quota: "1875", Input: 1000, Output: 100, Other: other}}})
	}))
	var one, two struct {
		Items []subUsageLog `json:"items"`
	}
	q := mapValues("page", "1")
	if err := s.newAPIUsage(context.Background(), account, "https://usage.example", q, &one); err != nil {
		t.Fatal(err)
	}
	display.Store(999)
	if err := s.newAPIUsage(context.Background(), account, "https://usage.example", q, &two); err != nil {
		t.Fatal(err)
	}
	if one.Items[0].ID != two.Items[0].ID || one.Items[0].RequestID != "newapi:stable-id" {
		t.Fatal("display ID used as identity")
	}
	receipt := subUsageReceipt(one.Items[0])
	if receipt.InputPrice == nil || *receipt.InputPrice != 10 || receipt.OutputPrice == nil || *receipt.OutputPrice != 50 || *receipt.ActualCost != 0.00375 {
		t.Fatal(receipt)
	}
}
func TestNewAPIRejectsUncertainPricesAndSubscriptionCost(t *testing.T) {
	for _, expr := range []string{`tier("standard", p * 10 + c * 50) * 2`, `tier("standard", p * 10 + c * 50 + evil())`, `tier("standard", p * 10 + p * 50)`} {
		if _, ok := newAPIExpressionPrices(base64.StdEncoding.EncodeToString([]byte(expr)), "standard"); ok {
			t.Fatal("unsafe expression accepted")
		}
	}
	log := newAPILog{Type: 2, Key: 51, Group: "default", Request: "id", At: 1, Quota: "1875", Input: 1000, Output: 100, Other: `{"billing_mode":"tiered_expr","model_ratio":37.5,"completion_ratio":2,"group_ratio":0.25}`}
	receipt, _, err := newAPIReceipt(log, 500000)
	if err != nil || receipt.InputCost != nil || receipt.ActualCost == nil {
		t.Fatal("ratio leaked into expression pricing")
	}
	log.Other = `{"billing_source":"subscription"}`
	if _, _, err = newAPIReceipt(log, 500000); err == nil {
		t.Fatal("subscription counted as wallet")
	}
}
func TestNewAPIRejectsBooleanFailureAndDoesNotLeakSecrets(t *testing.T) {
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":false,"message":"secret-password"}`)
	}))
	err := newAPIJSON(context.Background(), "https://usage.example", "GET", "/api/user/self", nil, nil, nil)
	if err == nil || strings.Contains(err.Error(), "secret-password") {
		t.Fatal(err)
	}
}
func mapValues(key, value string) map[string][]string { return map[string][]string{key: {value}} }

func TestNewAPILegacyCookieAndSecondFactor(t *testing.T) {
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/login/encryption-key":
			http.NotFound(w, r)
		case "/api/user/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "legacy-session", HttpOnly: true})
			newAPIOK(w, map[string]any{"require_2fa": true})
		case "/api/user/login/2fa":
			c, e := r.Cookie("session")
			if e != nil || c.Value != "legacy-session" {
				t.Error("challenge cookie missing")
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["code"] != "123456" {
				t.Error("missing code")
			}
			newAPIOK(w, map[string]any{"id": 8, "username": "legacy"})
		case "/api/user/self":
			if r.Header.Get("New-Api-User") != "8" {
				t.Error("missing legacy user ID")
			}
			newAPIOK(w, map[string]any{"id": 8, "username": "legacy"})
		default:
			t.Error(r.URL.Path)
		}
	}))
	auth, login, err := newAPILoginSession(context.Background(), "https://usage.example", "legacy", "password", "", "")
	if err != nil || !login.TwoFA {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(auth)
	auth, _, err = newAPILoginSession(context.Background(), "https://usage.example", "legacy", "", "cookie:"+base64.RawURLEncoding.EncodeToString(raw), "123456")
	if err != nil || auth.UserID != 8 || auth.Cookies["session"] != "legacy-session" {
		t.Fatal("legacy verification failed", err)
	}
	var profile newAPIUser
	if err = newAPIJSON(context.Background(), "https://usage.example", "GET", "/api/user/self", &auth, nil, &profile); err != nil {
		t.Fatal(err)
	}
}

func TestNewAPIProbeAndSpendUseTheSameNamespacedReceipt(t *testing.T) {
	s, account, _ := newAPITestService(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses":
			w.Header().Set("X-Oneapi-Request-Id", "new-bill")
			subSSE(w, "test", "gpt-6-sol")
		case "/api/log/self":
			if r.URL.Query().Get("type") == "6" {
				newAPIOK(w, map[string]any{"items": []any{}, "total": 0})
				return
			}
			newAPIOK(w, map[string]any{"total": 1, "items": []newAPILog{{ID: 999, Type: 2, Key: 51, Group: "default", Request: "new-bill", Model: "gpt-6-sol", At: at.Unix(), Quota: "150", Input: 100, Output: 10, Other: `{"model_ratio":1,"completion_ratio":5,"group_ratio":1,"billing_source":"wallet"}`}}})
		default:
			t.Error(r.URL.Path)
		}
	}))
	probe := subProbeStream(ctx, subHTTP, "https://usage.example", "sk-fixture", "say test", "gpt-6-sol")
	group, err := s.newAPIGroupID(ctx, account, "default")
	if err != nil {
		t.Fatal(err)
	}
	var page subSpendPage
	if err = s.newAPIUsage(ctx, account, "https://usage.example", mapValues("page", "1"), &page); err != nil {
		t.Fatal(err)
	}
	var logs struct {
		Items []subUsageLog `json:"items"`
	}
	if err = s.newAPIUsage(ctx, account, "https://usage.example", mapValues("page", "1"), &logs); err != nil {
		t.Fatal(err)
	}
	usage := subMatchUsage(subUsageTask{key: 51, group: group, ids: probe.RequestIDs}, logs.Items)
	if usage == nil || usage.RequestID != "newapi:new-bill" || usage.InputPrice == nil || *usage.InputPrice != 2 || usage.OutputPrice == nil || *usage.OutputPrice != 10 {
		t.Fatal(usage)
	}
	if page.Items[0].RequestID != usage.RequestID || page.Items[0].Cost.String() != "0.0003000000" {
		t.Fatal(page)
	}
}

func TestNewAPIExpressionRequestRulesRequireExactReceiptEvidence(t *testing.T) {
	expr := `(len <= 272000 ? tier("standard", p * 2 + c * 10 + cr * 0.2 + cc * 2.5) : tier("long_context", p * 4 + c * 15 + cr * 0.4 + cc * 5)) * (param("service_tier") == "flex" ? 0.5 : 1) * (param("service_tier") == "priority" ? 2 : 1)`
	meta := map[string]any{"billing_mode": "tiered_expr", "billing_source": "wallet", "expr_b64": base64.StdEncoding.EncodeToString([]byte(expr)), "matched_tier": "standard", "group_ratio": 0.25, "cache_tokens": 0, "request_rules": []any{map[string]any{"cond": `param("service_tier") == "flex"`, "matched": false, "multiplier": 0.5}, map[string]any{"cond": `param("service_tier") == "priority"`, "matched": false, "multiplier": 2}}}
	log := newAPILog{Input: 304, Output: 5, Quota: "82", Other: mustJSON(meta)}
	receipt, _, e := newAPIReceipt(log, 500000)
	if e != nil || receipt.BillingMode != "token" {
		t.Fatal(receipt, e)
	}
	result := subUsageReceipt(receipt)
	if result.InputPrice == nil || *result.InputPrice != 2 || result.OutputPrice == nil || *result.OutputPrice != 10 {
		t.Fatal(result)
	}
	meta["request_rules"] = []any{map[string]any{"cond": `param("service_tier") == "flex"`, "matched": false, "multiplier": 0.5}}
	log.Other = mustJSON(meta)
	receipt, _, _ = newAPIReceipt(log, 500000)
	if receipt.BillingMode == "token" {
		t.Fatal("missing rule was guessed")
	}
}

func TestNewAPISynchronizationReusesExistingDetectionQueue(t *testing.T) {
	s, account, _ := newAPITestService(t)
	ctx := context.Background()
	_, err := s.control.db.Exec(`UPDATE console_sub_accounts SET state='running',job_id='sync-fixture' WHERE id=$1`, account)
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.newAPIGroupID(ctx, account, "default")
	if err != nil {
		t.Fatal(err)
	}
	key := newAPIToken{ID: 51, UserID: 7, Name: subKeyName(account, group), Group: "default", Key: "sk-probe-fixture", Status: 1, Unlimited: true}
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self/groups":
			newAPIOK(w, map[string]any{"default": map[string]any{"ratio": 0.25}})
		case "/api/user/models":
			if r.URL.Query().Get("group") != "default" {
				t.Error("missing group scope")
			}
			newAPIOK(w, []string{"gpt-6-sol", "extra-model"})
		case "/api/token/search":
			newAPIOK(w, map[string]any{"items": []newAPIToken{key}, "total": 1})
		default:
			t.Error("unexpected mutation or sub2api call", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	if err = s.subSynchronize(ctx, account, "https://usage.example", "sync-fixture", ""); err != nil {
		t.Fatal(err)
	}
	var active bool
	var remote int64
	var encrypted string
	var rate float64
	if err = s.control.db.QueryRow(`SELECT active,remote_key_id,encrypted_key,(billing->>'rate')::float FROM console_sub_targets WHERE account_id=$1 AND group_id=$2`, account, group).Scan(&active, &remote, &encrypted, &rate); err != nil {
		t.Fatal(err)
	}
	plain, _ := s.control.decrypt(encrypted)
	if !active || remote != 51 || plain != "sk-probe-fixture" || rate != 0.25 {
		t.Fatal("bad normalized target")
	}
	var count int
	_ = s.control.db.QueryRow(`SELECT count(*) FROM console_sub_models WHERE account_id=$1 AND group_id=$2 AND state='queued'`, account, group).Scan(&count)
	if count != len(subModels)+1 {
		t.Fatal("detector models not unified", count)
	}
}

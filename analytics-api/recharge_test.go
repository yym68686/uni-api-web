package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRechargeMoneyContextAndURLBoundaries(t *testing.T) {
	for raw, want := range map[string]int64{`"1.03"`: 103, `1`: 100, `0.01`: 1, `"1000"`: 100000} {
		var amount rechargeMoney
		if err := json.Unmarshal([]byte(raw), &amount); err != nil || int64(amount) != want {
			t.Fatal(raw, amount, err)
		}
		c := rechargeContext{Total: amount, Password: "private", Code: "private-code"}
		data, err := c.marshal()
		decoded, e := decodeRechargeContext(string(data))
		if err != nil || e != nil || decoded.Total != amount || decoded.Password != c.Password {
			t.Fatal("context fen changed", string(data), err, e)
		}
	}
	for _, raw := range []string{`-1`, `1.001`, `null`, `1e2`, `"NaN"`, `"1\""`} {
		var amount rechargeMoney
		if json.Unmarshal([]byte(raw), &amount) == nil {
			t.Fatal("accepted invalid money", raw)
		}
	}
	s, canonical, err := parseRechargeShop("https://wzyp.cn/shop/SHOP123?token=private-site-jwt&user_id=123#private")
	if err != nil || canonical != "https://wzyp.cn/shop/SHOP123" || s.Token != "SHOP123" {
		t.Fatal(canonical, err)
	}
	for _, raw := range []string{"http://wzyp.cn/shop/SHOP123", "https://user:pass@wzyp.cn/shop/SHOP123", "https://wzyp.cn.evil/shop/SHOP123", "https://127.0.0.1/shop/SHOP123", "https://wzyp.cn:443/shop/SHOP123", "https://wzyp.cn/other"} {
		if _, _, err := parseRechargeShop(raw); err == nil {
			t.Fatal("unsafe shop", raw)
		}
	}
	for _, raw := range []string{"https://alipay.com.evil/", "https://qr.alipay.com:443/id", "https://qr.alipay.com/id#fragment", "http://cashier.alipay.com/", "https://user@aipay.alipay.com/"} {
		if rechargeOfficialAction(raw) {
			t.Fatal("unsafe official action", raw)
		}
	}
	if !rechargeAlipayURL("https://qr.alipay.com/abc?private=1") || rechargeAction([]byte("https://evil.example/")) != "" {
		t.Fatal("official link boundary")
	}
	for _, tt := range []struct{ raw, state string }{
		{`{"code":200,"message":"已开启支付宝支付功能"}`, "bound"},
		{`prefix\n{"code":200,"status":"applied_unbound"}`, "pending"},
		{`{"code":500,"message":"未开通"}`, "unbound"},
		{`{"code":200,"message":"success"}`, "unknown"},
		{`{"code":200,"message":"已开启支付宝支付功能"} broken`, "unknown"},
	} {
		if got := rechargeWalletJSON([]byte(tt.raw)); got.Status != tt.state {
			t.Fatal(tt.raw, got)
		}
	}
}

func TestRechargeLegacyHistoryAndIsolatedCLIRuntime(t *testing.T) {
	for _, raw := range []string{`[{"code":"EXACT-CODE-123","status":"used","type":"balance","used_by":42,"value":2}]`, `{"items":[{"code":"EXACT-CODE-123","status":"used","type":"balance","used_by":42,"value":2}],"total":1}`} {
		var h rechargeHistory
		if err := json.Unmarshal([]byte(raw), &h); err != nil || len(h.Items) != 1 || h.Total != 1 || *h.Items[0].UsedBy != 42 {
			t.Fatal(h, err)
		}
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "payment-fixture")
	// This is a local fake CLI, never the official executable or a real wallet.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n[ -z \"$PRIVATE_PLATFORM_FIXTURE\" ] || exit 1\n[ \"$HOME\" = \""+home+"\" ] || exit 2\n[ \"$1\" = \"check-wallet\" ] || exit 3\nprintf '%s' '{\"code\":200,\"message\":\"已开启支付宝支付功能\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRIVATE_PLATFORM_FIXTURE", "must-not-be-inherited")
	p := alipayRechargePayment{binary: binary, owner: "buyer", session: "runtime-provided", home: home}
	if p.Configured("stranger") {
		t.Fatal("wallet shared across owners")
	}
	wallet, err := p.Wallet(context.Background(), "buyer", "status", "")
	if err != nil || wallet.Status != "bound" {
		t.Fatal(wallet, err)
	}
	if err := os.Chmod(home, 0755); err != nil {
		t.Fatal(err)
	}
	if p.Configured("buyer") {
		t.Fatal("world-readable wallet directory accepted")
	}
}

type rechargeFakePayment struct {
	submits    atomic.Int32
	submitErr  bool
	configured bool
}

func (p *rechargeFakePayment) Configured(owner string) bool { return p.configured && owner == "buyer" }
func (p *rechargeFakePayment) Wallet(context.Context, string, string, string) (rechargeWallet, error) {
	return rechargeWallet{Status: "bound"}, nil
}
func (p *rechargeFakePayment) Submit(_ context.Context, owner, link, intent string) (string, error) {
	p.submits.Add(1)
	if owner != "buyer" || !rechargeAlipayURL(link) || !strings.Contains(intent, "1.03") {
		return "", errors.New("invalid payment arguments")
	}
	if p.submitErr {
		return "", errors.New("simulated unknown payment outcome")
	}
	return "https://aipay.alipay.com/confirm", nil
}

type rechargeFixture struct {
	t                           *testing.T
	s                           *Service
	account, cookie, foreign    string
	pay                         *rechargeFakePayment
	orders, redeems             atomic.Int32
	paid, redeemed              atomic.Bool
	price, userID, usedBy       atomic.Int64
	orderUnknown, redeemUnknown atomic.Bool
}

func newRechargeFixture(t *testing.T) *rechargeFixture {
	t.Helper()
	db := migrationTestDB(t)
	if err := migrateControlSchema(context.Background(), db, controlMigrations()); err != nil {
		t.Fatal(err)
	}
	store := &controlStore{db: db, key: []byte(strings.Repeat("r", 32))}
	f := &rechargeFixture{t: t, s: &Service{control: store}, account: "account", pay: &rechargeFakePayment{configured: true}}
	f.s.rechargePayment = f.pay
	f.price.Store(103)
	f.userID.Store(42)
	f.usedBy.Store(42)
	auth, _ := store.encrypt(`{"access_token":"private-site-token","expires_at":9999999999}`)
	if _, err := db.Exec(`INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES('account','buyer','Site','https://site.example','me@example.com',$1)`, auth); err != nil {
		t.Fatal(err)
	}
	f.cookie, _ = store.newSession(context.Background(), "buyer")
	f.foreign, _ = store.newSession(context.Background(), "stranger")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		merchant := strings.HasPrefix(r.URL.Path, "/shopApi/") || r.URL.Path == "/cashier"
		if merchant && strings.Contains(r.Header.Get("Authorization"), "private") {
			t.Error("site JWT sent to shop")
		}
		out := func(data any) {
			code := 0
			if merchant {
				code = 1
			}
			writeJSON(w, 200, map[string]any{"code": code, "data": data})
		}
		goods := map[string]any{"goods_key": "PRODUCT1", "name": "1元充值", "goods_type": "card", "status": 1, "price": "1.00", "user": map[string]string{"token": "SHOP123", "nickname": "fixture"}, "extend": map[string]int{"limit_count": 1}}
		switch r.URL.Path {
		case "/api/v1/auth/me":
			out(map[string]any{"id": f.userID.Load(), "email": "me@example.com"})
		case "/shopApi/Shop/goodsList":
			out(map[string]any{"total": 1, "list": []any{goods}})
		case "/shopApi/Shop/goodsInfo":
			out(goods)
		case "/shopApi/Shop/getUserChannel":
			out([]any{map[string]any{"id": 1, "code": "AlipayPc", "status": 1}})
		case "/shopApi/Shop/getGoodsPrice":
			out(map[string]string{"total_amount": rechargeMoney(f.price.Load()).String(), "fee": "0.03"})
		case "/shopApi/common/buyerBlackIframe":
			w.Write([]byte(`const juuid = 'BUYER123';`))
		case "/shopApi/Pay/order":
			f.orders.Add(1)
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["quantity"] != float64(1) || body["goods_key"] != "PRODUCT1" || body["query_password"] == "" {
				t.Error("wrong order body")
			}
			if f.orderUnknown.Load() {
				http.Error(w, "private error", 502)
				return
			}
			out(map[string]any{"trade_no": "ORDER123", "total_amount": "1.03", "payurl": "https://wzyp.cn/cashier"})
		case "/cashier":
			w.Write([]byte(`<a href="https://qr.alipay.com/PAY123">pay</a>`))
		case "/shopApi/Order/info":
			status := 0
			cards := []string{}
			if f.paid.Load() {
				status = 1
				cards = []string{"EXACT-CODE-123"}
			}
			out(map[string]any{"trade_no": "ORDER123", "total_amount": "1.03", "quantity": 1, "status": status, "goods": goods, "response": map[string]any{"cards": cards}})
		case "/api/v1/redeem":
			f.redeems.Add(1)
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["code"] != "EXACT-CODE-123" || r.Header.Get("Authorization") != "Bearer private-site-token" {
				t.Error("wrong redemption")
			}
			f.redeemed.Store(true)
			if f.redeemUnknown.Load() {
				http.Error(w, "unknown", 502)
				return
			}
			out(map[string]string{"code": "EXACT-CODE-123"})
		case "/api/v1/redeem/history":
			items := []any{}
			if f.redeemed.Load() {
				items = append(items, map[string]any{"code": "EXACT-CODE-123", "status": "used", "type": "balance", "value": 2, "used_by": f.usedBy.Load()})
			}
			out(map[string]any{"items": items, "total": len(items)})
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	u, _ := url.Parse(up.URL)
	old := subHTTP
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}, Timeout: time.Second * 5, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(func() { subHTTP = old; up.Close() })
	return f
}
func (f *rechargeFixture) request(method, path string, body any, cookie string) *httptest.ResponseRecorder {
	f.t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "/v1/sub2api/accounts/"+f.account+"/recharge"+path, strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	return w
}
func (f *rechargeFixture) configure() {
	f.t.Helper()
	w := f.request("PUT", "/settings", rechargeConfig{Enabled: true, ShopURL: "https://wzyp.cn/shop/SHOP123?token=private-jwt", Products: []string{"PRODUCT1"}, Contact: "buyer-contact", MaxCNY: "10", TargetBase: "https://site.example", TargetEmail: "me@example.com"}, f.cookie)
	if w.Code != 200 {
		f.t.Fatal(w.Code, w.Body.String())
	}
}
func (f *rechargeFixture) quote() rechargeQuote {
	f.t.Helper()
	w := f.request("POST", "/quotes", map[string]string{"product": "PRODUCT1"}, f.cookie)
	var q rechargeQuote
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &q) != nil {
		f.t.Fatal(w.Code, w.Body.String())
	}
	return q
}
func (f *rechargeFixture) submit(q rechargeQuote, key string) *httptest.ResponseRecorder {
	return f.request("POST", "/jobs", map[string]any{"quote_id": q.ID, "confirmed": true, "total_cny": q.Total, "idempotency_key": key}, f.cookie)
}
func (f *rechargeFixture) work(state string) {
	f.t.Helper()
	if _, err := f.s.control.db.Exec(`UPDATE console_recharge_jobs SET next_attempt=now()`); err != nil {
		f.t.Fatal(err)
	}
	if !f.s.rechargeWorkOne(context.Background()) {
		f.t.Fatal("no job claimed")
	}
	var got string
	if err := f.s.control.db.QueryRow(`SELECT state FROM console_recharge_jobs LIMIT 1`).Scan(&got); err != nil || got != state {
		f.t.Fatal("state", got, "want", state, err)
	}
}

func TestRechargeFullIsolatedFlowNoDuplicateWritesAndPrivateMaterial(t *testing.T) {
	f := newRechargeFixture(t)
	f.configure()
	w := f.request("GET", "/settings", nil, f.cookie)
	if strings.Contains(w.Body.String(), "private-jwt") || !strings.Contains(w.Body.String(), "https://wzyp.cn/shop/SHOP123") {
		t.Fatal("URL not sanitized", w.Body.String())
	}
	q := f.quote()
	if q.Total != "1.03" || q.Fee != "0.03" || f.orders.Load() != 0 {
		t.Fatal(q, "quote created order")
	}
	w = f.submit(q, "once-key")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var first map[string]string
	json.Unmarshal(w.Body.Bytes(), &first)
	w = f.submit(q, "once-key")
	if w.Code != 200 || !strings.Contains(w.Body.String(), first["id"]) {
		t.Fatal("not idempotent", w.Code, w.Body.String())
	}
	f.work("awaiting_payment")
	f.work("awaiting_payment") // unpaid: no resubmit
	f.paid.Store(true)
	f.work("verifying")
	f.work("done")
	if f.orders.Load() != 1 || f.pay.submits.Load() != 1 || f.redeems.Load() != 1 {
		t.Fatal("duplicate write", f.orders.Load(), f.pay.submits.Load(), f.redeems.Load())
	}
	w = f.request("GET", "/jobs", nil, f.cookie)
	for _, secret := range []string{"EXACT-CODE-123", "private-site-token", "buyer-contact", "query_password", "payment_url"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("secret in jobs", secret)
		}
	}
	if !strings.Contains(w.Body.String(), `"credited_amount":2`) {
		t.Fatal(w.Body.String())
	}
	w = f.request("GET", "/jobs/"+first["id"]+"/recovery", nil, f.cookie)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "EXACT-CODE-123") {
		t.Fatal("explicit recovery unavailable", w.Code)
	}
	for _, path := range []string{"/settings", "/jobs", "/jobs/" + first["id"] + "/recovery"} {
		if w := f.request("GET", path, nil, f.foreign); w.Code != 404 {
			t.Fatal("foreign access", path, w.Code)
		}
	}
}

func TestRechargeUnknownWritesNeverRetried(t *testing.T) {
	for _, phase := range []string{"order", "payment", "redeem"} {
		t.Run(phase, func(t *testing.T) {
			f := newRechargeFixture(t)
			f.configure()
			q := f.quote()
			w := f.submit(q, "unknown-once")
			if w.Code != 202 {
				t.Fatal(w.Code)
			}
			if phase == "order" {
				f.orderUnknown.Store(true)
				f.work("review")
				if f.pay.submits.Load() != 0 {
					t.Fatal("paid unknown order")
				}
				return
			}
			f.pay.submitErr = phase == "payment"
			f.redeemUnknown.Store(phase == "redeem")
			f.work("awaiting_payment")
			f.paid.Store(true)
			f.work("verifying")
			f.work("done")
			if f.orders.Load() != 1 || f.pay.submits.Load() != 1 || f.redeems.Load() != 1 {
				t.Fatal("unknown result repeated a write")
			}
		})
	}
}
func TestRechargeChangedPriceIdentityAndHistoryAreRejected(t *testing.T) {
	for _, change := range []string{"price", "identity", "settings", "expiry", "history"} {
		t.Run(change, func(t *testing.T) {
			f := newRechargeFixture(t)
			f.configure()
			q := f.quote()
			if w := f.submit(q, "change-once"); w.Code != 202 {
				t.Fatal(w.Code)
			}
			switch change {
			case "price":
				f.price.Store(104)
			case "identity":
				f.userID.Store(43)
			case "settings":
				f.configure()
			case "expiry":
				f.s.control.db.Exec(`UPDATE console_recharge_quotes SET expires_at=now()-interval '1 second'`)
			case "history":
				f.work("awaiting_payment")
				f.paid.Store(true)
				f.work("verifying")
				f.usedBy.Store(43)
				f.work("review")
				return
			}
			f.work("failed")
			if f.orders.Load() != 0 || f.pay.submits.Load() != 0 || f.redeems.Load() != 0 {
				t.Fatal("changed evidence still caused payment")
			}
		})
	}
}
func TestRechargeCrashBoundariesAndConcurrentClaims(t *testing.T) {
	for _, state := range []string{"creating", "paying", "redeeming"} {
		t.Run(state, func(t *testing.T) {
			f := newRechargeFixture(t)
			f.configure()
			q := f.quote()
			f.submit(q, "crash-once")
			f.s.control.db.Exec(`UPDATE console_recharge_jobs SET state=$1`, state)
			want := map[string]string{"creating": "review", "paying": "awaiting_payment", "redeeming": "verifying"}[state]
			f.work(want)
			if f.orders.Load() != 0 || f.pay.submits.Load() != 0 || f.redeems.Load() != 0 {
				t.Fatal("replayed uncertain write")
			}
		})
	}
	t.Run("claim", func(t *testing.T) {
		f := newRechargeFixture(t)
		f.configure()
		q := f.quote()
		f.submit(q, "claim-once")
		var wg sync.WaitGroup
		for range 5 {
			wg.Add(1)
			go func() { defer wg.Done(); f.s.rechargeWorkOne(context.Background()) }()
		}
		wg.Wait()
		if f.orders.Load() != 1 || f.pay.submits.Load() != 1 {
			t.Fatal("duplicate workers", f.orders.Load(), f.pay.submits.Load())
		}
	})
}
func TestRechargeSubmissionValidationAndWalletLock(t *testing.T) {
	f := newRechargeFixture(t)
	f.configure()
	q := f.quote()
	for _, in := range []map[string]any{
		{"quote_id": q.ID, "confirmed": false, "total_cny": q.Total, "idempotency_key": "test-key"},
		{"quote_id": q.ID, "confirmed": true, "total_cny": "999.00", "idempotency_key": "test-key"},
	} {
		if w := f.request("POST", "/jobs", in, f.cookie); w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := f.submit(q, "valid-key"); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	q2 := f.quote()
	if w := f.submit(q2, "another-key"); w.Code != 409 {
		t.Fatal("second active recharge accepted", w.Code)
	}
	unlock, err := f.s.rechargeWalletLock(context.Background(), "buyer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.rechargeWalletLock(context.Background(), "buyer"); err == nil {
		t.Fatal("overlapping wallet operations")
	}
	unlock()
	unlock, err = f.s.rechargeWalletLock(context.Background(), "buyer")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	f.pay.configured = false
	if w := f.request("GET", "/wallet", nil, f.cookie); w.Code != 200 || !strings.Contains(w.Body.String(), "unconfigured") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestRechargeUnreadableCiphertextSurvivesRecoveryAndAccountCannotBeDeleted(t *testing.T) {
	f := newRechargeFixture(t)
	f.configure()
	q := f.quote()
	if w := f.submit(q, "preserve-once"); w.Code != 202 {
		t.Fatal(w.Code)
	}
	var original string
	if err := f.s.control.db.QueryRow(`SELECT encrypted_context FROM console_recharge_jobs LIMIT 1`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	key := f.s.control.key
	f.s.control.key = []byte(strings.Repeat("x", 32))
	f.work("review")
	f.s.control.key = key
	var after string
	if err := f.s.control.db.QueryRow(`SELECT encrypted_context FROM console_recharge_jobs LIMIT 1`).Scan(&after); err != nil || after != original {
		t.Fatal("lost recovery ciphertext", err)
	}
	plain, err := f.s.control.decrypt(after)
	if err != nil || !strings.Contains(plain, "password") {
		t.Fatal("recovery no longer possible", err)
	}
	r := httptest.NewRequest("DELETE", "/v1/sub2api/accounts/"+f.account, nil)
	r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: f.cookie})
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "充值账目") {
		t.Fatal("unexplained deletion failure", w.Code, w.Body.String())
	}
}

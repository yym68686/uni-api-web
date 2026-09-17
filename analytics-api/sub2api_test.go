package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func subSSE(w http.ResponseWriter, answer string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
	w.(http.Flusher).Flush()
	fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", answer)
	fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}]}}\n\n", answer)
}
func TestSubProbeStreamMeasuresTextAndRequiresCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, mode, status string
		ttft               bool
	}{
		{"success", "success", "success", true}, {"reasoning is not first text", "reasoning", "success", true},
		{"truncated", "truncated", "error", true}, {"failed after text", "failed", "error", true},
		{"JSON instead of SSE", "json", "error", false}, {"missing delta", "no_delta", "success", false},
		{"no message", "no_message", "error", false}, {"HTTP error", "http", "error", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("wrong key")
				}
				var body struct {
					Model  string `json:"model"`
					Stream bool   `json:"stream"`
					Input  []struct {
						Content string `json:"content"`
					} `json:"input"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				if r.URL.Path != "/v1/responses" || body.Model != checkModel || !body.Stream || len(body.Input) != 1 || body.Input[0].Content != "say test" {
					t.Error("wrong probe payload")
				}
				if tc.mode == "http" {
					http.Error(w, "test-key", 429)
					return
				}
				if tc.mode == "json" {
					writeJSON(w, 200, map[string]string{"text": "test"})
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.created\"}\n\n")
				w.(http.Flusher).Flush()
				if tc.mode == "reasoning" {
					fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"secret reasoning\"}\n\n")
					w.(http.Flusher).Flush()
					time.Sleep(35 * time.Millisecond)
				}
				if tc.mode == "no_message" {
					fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
					return
				}
				if tc.mode != "no_delta" {
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"test\"}\n\n")
				}
				if tc.mode == "truncated" {
					fmt.Fprint(w, "data: [DONE]\n\n")
					return
				}
				if tc.mode == "failed" {
					fmt.Fprint(w, "data: {\"type\":\"response.failed\"}\n\n")
					return
				}
				fmt.Fprint(w, "event: response.completed\ndata: {\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"test\"}]}]}}\n\n")
			}))
			defer upstream.Close()
			result := subProbeStream(context.Background(), upstream.Client(), upstream.URL, "test-key", "say test")
			if result.Status != tc.status || (result.TTFT != nil) != tc.ttft {
				t.Fatalf("result %+v", result)
			}
			if tc.mode == "reasoning" && *result.TTFT < 30 {
				t.Fatal("reasoning was counted as first text")
			}
			if strings.Contains(result.Message, "test-key") {
				t.Fatal("secret leaked")
			}
		})
	}
}
func TestSubProbeQualityAndFailureSeparation(t *testing.T) {
	for _, answer := range []string{"未知", "2024-06", "未知或2024-06", "2025-01"} {
		var calls int
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			var b struct {
				Input []struct {
					Content string `json:"content"`
				} `json:"input"`
			}
			json.NewDecoder(r.Body).Decode(&b)
			if b.Input[0].Content == "say test" {
				subSSE(w, "test")
			} else if b.Input[0].Content == checkPrompt {
				subSSE(w, answer)
			} else {
				t.Error("wrong quality prompt")
			}
		}))
		out := subRunProbes(context.Background(), upstream.Client(), upstream.URL, "key")
		upstream.Close()
		if calls != 2 || out.Verdict != checkVerdict(answer) || out.Availability.Status != "success" {
			t.Fatalf("%+v", out)
		}
	}
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unavailable", 503) }))
	defer upstream.Close()
	out := subRunProbes(context.Background(), upstream.Client(), upstream.URL, "key")
	if calls != 1 || out.Quality.Status != "skipped" || out.Verdict != "error" {
		t.Fatal(out, calls)
	}
}
func TestSubPublicEndpointPolicy(t *testing.T) {
	for _, raw := range []string{"http://public.example", "https://u:p@public.example", "https://127.0.0.1", "https://10.0.0.1", "https://169.254.169.254", "https://[::1]", "https://100.64.0.1", "https://public.example/?secret=a"} {
		if _, err := subBase(raw); err == nil {
			t.Error("accepted", raw)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.2", "169.254.169.254", "100.100.100.200", "::1", "fc00::1"} {
		if subPublicIP(net.ParseIP(ip)) {
			t.Error("accepted private address", ip)
		}
	}
	if base, e := subBase(" https://example.com/api/v1/ "); e != nil || base != "https://example.com" {
		t.Fatal(base, e)
	}
	client := newSubHTTP()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://localhost/", nil)
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("resolved loopback accepted")
	}
	if e := client.CheckRedirect(nil, nil); e != http.ErrUseLastResponse {
		t.Fatal("redirects allowed")
	}
}

type subTestTransport struct {
	base     *url.URL
	delegate http.RoundTripper
}

func (t subTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = t.base.Scheme
	u.Host = t.base.Host
	copy.URL = &u
	return t.delegate.RoundTrip(copy)
}

func TestSubAccountLifecycleIsolationAndIdempotency(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := "sub-test-" + randomID()[:8]
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE owner=$1 OR owner=$2`, owner, owner+"-other")
	service := &Service{control: store}
	token, _ := store.newSession(context.Background(), owner)
	foreign, _ := store.newSession(context.Background(), owner+"-other")
	var mu sync.Mutex
	keys := map[string]subRemoteKey{}
	var created, probes, refreshed atomic.Int32
	var groupCount atomic.Int32
	groupCount.Store(3)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		success := func(v any) { writeJSON(w, 200, map[string]any{"code": 0, "data": v}) }
		if r.URL.Path == "/api/v1/auth/login" {
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["password"] != "secret-password" {
				t.Error("missing password")
			}
			success(map[string]any{"access_token": "access-secret", "refresh_token": "refresh-secret", "expires_in": 60})
			return
		}
		if r.URL.Path == "/api/v1/auth/me" {
			success(map[string]string{"email": "test@example.com"})
			return
		}
		if r.URL.Path == "/api/v1/auth/refresh" {
			refreshed.Add(1)
			success(map[string]any{"access_token": "renewed-secret", "refresh_token": "rotated-secret", "expires_in": 600})
			return
		}
		if r.URL.Path == "/v1/sub2api/billing" {
			writeJSON(w, 200, map[string]any{"object": "sub2api.key_billing", "billing_scope": "token", "effective_rate_multiplier": 0.01})
			return
		}
		if r.URL.Path == "/v1/responses" {
			probes.Add(1)
			var in struct {
				Input []struct {
					Content string `json:"content"`
				} `json:"input"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			answer := "test"
			if in.Input[0].Content == checkPrompt {
				answer = "未知"
			}
			subSSE(w, answer)
			return
		}
		if r.Header.Get("Authorization") == "Bearer access-secret" {
			http.Error(w, "expired", 401)
			return
		}
		if r.Header.Get("Authorization") != "Bearer renewed-secret" {
			t.Error("wrong panel token")
		}
		switch r.URL.Path {
		case "/api/v1/groups/available":
			groups := []subRemoteGroup{}
			for i := 1; i <= int(groupCount.Load()); i++ {
				groups = append(groups, subRemoteGroup{ID: int64(i), Name: fmt.Sprintf("Group %d", i), Platform: "openai", Rate: 1})
			}
			success(groups)
		case "/api/v1/groups/rates":
			success(map[string]float64{"1": 0.02})
		case "/api/v1/channels/available":
			success([]any{})
		case "/api/v1/keys":
			mu.Lock()
			defer mu.Unlock()
			if r.Method == "GET" {
				items := []subRemoteKey{}
				for _, k := range keys {
					items = append(items, k)
				}
				success(map[string]any{"items": items, "total": len(items), "pages": 1})
				return
			}
			var in struct {
				Name  string  `json:"name"`
				Group int64   `json:"group_id"`
				Quota float64 `json:"quota"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			if in.Quota != 1 || r.Header.Get("Idempotency-Key") == "" {
				t.Error("missing budget/idempotency")
			}
			if _, exists := keys[in.Name]; exists {
				t.Error("duplicate creation")
			}
			k := subRemoteKey{ID: in.Group + 10, Key: fmt.Sprintf("key-secret-%d", in.Group), Name: in.Name, GroupID: in.Group, Status: "active"}
			keys[in.Name] = k
			created.Add(1)
			success(k)
		default:
			t.Error("unexpected path", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	previous := subHTTP
	u, _ := url.Parse(upstream.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}, Timeout: time.Second * 5}
	defer func() { subHTTP = previous }()
	request := func(method, path, body, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/v1/sub2api/accounts", "", ""); w.Code != 401 {
		t.Fatal("unauthenticated", w.Code)
	}
	w := request("POST", "/v1/sub2api/accounts", `{"name":"test","base":"https://upstream.example","email":"test@example.com","password":"secret-password"}`, token)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var added struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &added)
	if w = request("POST", "/v1/sub2api/accounts/"+added.ID+"/sync", "{}", foreign); w.Code != 409 {
		t.Fatal("foreign mutation", w.Code)
	}
	if !service.subWorkOne(context.Background()) {
		t.Fatal("no queued job")
	}
	if created.Load() != 3 || probes.Load() != 6 || refreshed.Load() != 1 {
		t.Fatal("wrong initial work", created.Load(), probes.Load(), refreshed.Load())
	}
	w = request("GET", "/v1/sub2api/accounts", "", token)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var listed struct {
		Data []subAccount `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &listed)
	if len(listed.Data) != 1 || len(listed.Data[0].Targets) != 3 || listed.Data[0].State != "idle" {
		t.Fatal(w.Body.String())
	}
	for _, target := range listed.Data[0].Targets {
		if target.Billing == nil || target.Billing.Rate == nil || *target.Billing.Rate != 0.01 {
			t.Fatal("wrong effective multiplier", target.Billing)
		}
		if target.Result == nil || target.Result.Verdict != "pass" || target.State != "done" {
			t.Fatal(target)
		}
	}
	for _, secret := range []string{"access-secret", "refresh-secret", "rotated-secret", "key-secret", "secret-password"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("secret exposed")
		}
	}
	var authEnc, keyEnc string
	store.db.QueryRow(`SELECT encrypted_auth FROM console_sub_accounts WHERE id=$1`, added.ID).Scan(&authEnc)
	store.db.QueryRow(`SELECT encrypted_key FROM console_sub_targets WHERE account_id=$1 LIMIT 1`, added.ID).Scan(&keyEnc)
	if strings.Contains(authEnc, "secret") || strings.Contains(keyEnc, "secret") {
		t.Fatal("plaintext credentials")
	}
	if w = request("GET", "/v1/sub2api/accounts", "", foreign); !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatal("foreign account leak", w.Body.String())
	}
	service = &Service{control: store}
	groupCount.Store(2)
	if w = request("POST", "/v1/sub2api/accounts/"+added.ID+"/sync", "{}", token); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	service.subWorkOne(context.Background())
	if created.Load() != 3 || probes.Load() != 10 || refreshed.Load() != 1 {
		t.Fatal("sync duplicated keys or refreshed a rotated token", created.Load(), probes.Load(), refreshed.Load())
	}
	var active bool
	store.db.QueryRow(`SELECT active FROM console_sub_targets WHERE account_id=$1 AND group_id=3`, added.ID).Scan(&active)
	if active {
		t.Fatal("removed group still active")
	}
	w = request("POST", "/v1/sub2api/checks", fmt.Sprintf(`{"targets":[{"account_id":%q,"group_id":1},{"account_id":%q,"group_id":999}]}`, added.ID, added.ID), token)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	var state string
	store.db.QueryRow(`SELECT state FROM console_sub_accounts WHERE id=$1`, added.ID).Scan(&state)
	if state != "idle" {
		t.Fatal("partial batch committed")
	}
	w = request("POST", "/v1/sub2api/checks", fmt.Sprintf(`{"targets":[{"account_id":%q,"group_id":1}]}`, added.ID), token)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	service.subWorkOne(context.Background())
	if probes.Load() != 12 {
		t.Fatal("filter ignored", probes.Load())
	}
	request("POST", "/v1/sub2api/accounts/"+added.ID+"/sync", "{}", token)
	request("POST", "/v1/sub2api/accounts/"+added.ID+"/stop", "{}", token)
	if service.subWorkOne(context.Background()) || probes.Load() != 12 {
		t.Fatal("stopped work executed")
	}
	store.db.Exec(`UPDATE console_sub_accounts SET state='running',job_id='crashed',lease_until=now()-interval '1 minute' WHERE id=$1`, added.ID)
	store.db.Exec(`UPDATE console_sub_targets SET state='running' WHERE account_id=$1 AND group_id=1`, added.ID)
	service.subWorkOne(context.Background())
	store.db.QueryRow(`SELECT state FROM console_sub_accounts WHERE id=$1`, added.ID).Scan(&state)
	if state != "interrupted" || probes.Load() != 12 {
		t.Fatal("interrupted job replayed", state)
	}
	if w = request("DELETE", "/v1/sub2api/accounts/"+added.ID, "", token); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestSubBillingKeepsUpstreamEffectiveRate(t *testing.T) {
	fallbackRate := 0.02
	fallback := subBilling{Rate: &fallbackRate, Source: "panel", CheckedAt: 123}
	for _, tc := range []struct {
		body   string
		want   float64
		source string
	}{
		{`{"object":"sub2api.key_billing","billing_scope":"token","effective_rate_multiplier":0.01}`, 0.01, "key"},
		{`{"object":"sub2api.key_billing","billing_scope":"token","effective_rate_multiplier":0}`, 0, "key"},
		{`{"object":"sub2api.key_billing","billing_scope":"token","effective_rate_multiplier":-1}`, 0.02, "panel"},
		{`{"error":"unsupported"}`, 0.02, "panel"},
	} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/sub2api/billing" || r.Header.Get("Authorization") != "Bearer own-key" {
				t.Error("wrong billing request")
			}
			fmt.Fprint(w, tc.body)
		}))
		got := subKeyBilling(context.Background(), upstream.Client(), upstream.URL, "own-key", fallback)
		upstream.Close()
		if got.Rate == nil || *got.Rate != tc.want || got.Source != tc.source {
			t.Fatalf("unexpected conversion/fallback %+v", got)
		}
	}
}
func TestSubTOTPChallengeAndDuplicateClaims(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("m", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := "sub-totp-" + randomID()[:8]
	defer store.db.Exec(`DELETE FROM console_sub_accounts WHERE owner=$1`, owner)
	service := &Service{control: store}
	token, _ := store.newSession(context.Background(), owner)
	foreign, _ := store.newSession(context.Background(), owner+"-other")
	var secondFactor atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		success := func(v any) { writeJSON(w, 200, map[string]any{"code": 0, "data": v}) }
		switch r.URL.Path {
		case "/api/v1/auth/login":
			success(map[string]any{"requires_2fa": true, "temp_token": "upstream-temp-secret"})
		case "/api/v1/auth/login/2fa":
			secondFactor.Add(1)
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["totp_code"] != "123456" || in["temp_token"] != "upstream-temp-secret" {
				t.Error("wrong 2fa proof")
			}
			success(map[string]string{"access_token": "access", "refresh_token": "refresh"})
		case "/api/v1/auth/me":
			success(map[string]string{"email": "me@example.com"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	previous := subHTTP
	u, _ := url.Parse(upstream.URL)
	subHTTP = &http.Client{Transport: subTestTransport{u, http.DefaultTransport}}
	defer func() { subHTTP = previous }()
	request := func(body, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/sub2api/accounts", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: cookie})
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, r)
		return w
	}
	w := request(`{"base":"https://site.example","email":"me@example.com","password":"password"}`, token)
	var result struct {
		Challenge string `json:"challenge"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Challenge == "" || strings.Contains(w.Body.String(), "upstream-temp-secret") {
		t.Fatal("unsafe challenge", w.Body.String())
	}
	body := fmt.Sprintf(`{"challenge":%q,"totp_code":"123456"}`, result.Challenge)
	if w = request(body, foreign); w.Code != 400 || secondFactor.Load() != 0 {
		t.Fatal("cross-account challenge used")
	}
	if w = request(body, token); w.Code != 202 || secondFactor.Load() != 1 {
		t.Fatal("2fa failed", w.Code, w.Body.String())
	}
	var id string
	store.db.QueryRow(`SELECT id FROM console_sub_accounts WHERE owner=$1`, owner).Scan(&id)
	// Two concurrent claim attempts cannot both own the account.
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var claimed string
			e := store.db.QueryRow(`UPDATE console_sub_accounts SET state='running',job_id=$2,lease_until=now()+interval '30 seconds' WHERE id=(SELECT id FROM console_sub_accounts WHERE id=$1 AND state='queued' FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id`, id, randomID()).Scan(&claimed)
			if e == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("duplicate workers claimed an account", successes.Load())
	}
}

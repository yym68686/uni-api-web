package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fingerprintRequest(t *testing.T, s *Service, owner, method string, targets []fingerprintTarget) *httptest.ResponseRecorder {
	t.Helper()
	token, err := s.control.newSession(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "/v1/model-fingerprints", strings.NewReader(mustJSON(map[string]any{"targets": targets})))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

func TestModelFingerprintListIncludesUnfinishedAndFailedJobs(t *testing.T) {
	s, a, src := bindingFixture(t, "https://site.example")
	ctx := context.Background()
	// These states legitimately have SQL NULL results. A single unfinished job
	// must not make the entire list, including completed results, unreadable.
	states := []string{"queued", "running", "error", "interrupted", "done"}
	for _, state := range states {
		target := fingerprintTarget{Source: src.ID, Provider: state, Model: "gpt-6-astra"}
		if err := enqueueFingerprint(ctx, s.control.db, target); err != nil {
			t.Fatal(err)
		}
		var result any
		if state == "done" {
			result = `{"status":"match","probability":1}`
		}
		if _, err := s.control.db.Exec(`UPDATE console_model_fingerprints SET state=$3,result=$4,deadline=now()+interval '30 seconds' WHERE scope=$1 AND model=$2`, target.scope(), target.Model, state, result); err != nil {
			t.Fatal(err)
		}
	}
	w := fingerprintRequest(t, s, a.Owner, "GET", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("mixed fingerprint list: HTTP %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Data []fingerprintRecord `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, record := range out.Data {
		if record.Source != src.ID {
			continue
		}
		seen[record.State] = true
		if record.State == "done" {
			var result struct {
				Status      string  `json:"status"`
				Probability float64 `json:"probability"`
			}
			if err := json.Unmarshal(record.Result, &result); err != nil || result.Status != "match" || result.Probability != 1 {
				t.Fatalf("completed result missing: %s", record.Result)
			}
		} else if string(record.Result) != "null" {
			t.Fatalf("%s must return result:null, got %s", record.State, record.Result)
		}
	}
	if len(seen) != len(states) {
		t.Fatalf("missing states: %v", seen)
	}
}

func TestModelFingerprintQueueScopesLeasesCancelAndExpiry(t *testing.T) {
	s, a, src := bindingFixture(t, "https://site.example")
	key, _ := s.control.encrypt("test-secret")
	_, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key,active) VALUES($1,1,'one','openai',$2,true)`, a.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	target := fingerprintTarget{Account: a.ID, Group: 1, Model: "gpt-6.1-sol"}
	for range 2 {
		w := fingerprintRequest(t, s, a.Owner, "POST", []fingerprintTarget{target})
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var count int
	s.control.db.QueryRow(`SELECT count(*) FROM console_model_fingerprints WHERE account_id=$1`, a.ID).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	if w := fingerprintRequest(t, s, "other-user", "POST", []fingerprintTarget{target}); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	invalid := target
	invalid.Model = "gpt-5.5"
	if w := fingerprintRequest(t, s, a.Owner, "POST", []fingerprintTarget{invalid}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	invalid = target
	invalid.Source = src.ID
	if w := fingerprintRequest(t, s, a.Owner, "POST", []fingerprintTarget{invalid}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	claimed, run, err := s.claimFingerprint(context.Background())
	if err != nil || claimed.scope() != target.scope() {
		t.Fatal(claimed, err)
	}
	t.Cleanup(func() {
		s.control.db.Exec(`DELETE FROM console_fingerprint_site_leases`)
		s.control.db.Exec(`UPDATE console_fingerprint_worker SET deadline=NULL WHERE id=true`)
	})
	native := fingerprintTarget{Source: src.ID, Provider: "native", Model: "gpt-6-astra"}
	if err = s.saveConfiguredInventory(context.Background(), src, []configuredProvider{{Provider: "native", Base: "https://SITE.example:443/v1/responses", API: "test"}}); err != nil {
		t.Fatal(err)
	}
	if err = enqueueFingerprint(context.Background(), s.control.db, native); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.claimFingerprint(context.Background()); err == nil {
		t.Fatal("same-site channels ran concurrently")
	}
	if w := fingerprintRequest(t, s, a.Owner, "DELETE", []fingerprintTarget{target}); w.Code != 202 {
		t.Fatal(w.Code)
	}
	res, err := s.control.db.Exec(`UPDATE console_model_fingerprints SET state='done' WHERE scope=$1 AND model=$2 AND run_id=$3 AND state='running'`, target.scope(), target.Model, run)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatal("late result resurrected cancelled request")
	}
	if err = s.renewFingerprint(context.Background(), target, run); err == nil {
		t.Fatal("cancelled job renewed its lease")
	}
	if _, _, err = s.claimFingerprint(context.Background()); err == nil {
		t.Fatal("cancel released site before the running process stopped")
	}
	if err = s.finishFingerprint(context.Background(), target, run, "done", "", "", nil); err != nil {
		t.Fatal(err)
	}
	second, run2, err := s.claimFingerprint(context.Background())
	if err != nil || second.scope() != native.scope() {
		t.Fatal(second, err)
	}
	s.control.db.Exec(`UPDATE console_model_fingerprints SET deadline=now()-interval '1 second' WHERE scope=$1`, native.scope())
	s.control.db.Exec(`UPDATE console_fingerprint_site_leases SET deadline=now()-interval '1 second' WHERE run_id=$1`, run2)
	if _, _, err = s.claimFingerprint(context.Background()); err == nil {
		t.Fatal("expired paid request was replayed")
	}
	var state string
	s.control.db.QueryRow(`SELECT state FROM console_model_fingerprints WHERE scope=$1`, native.scope()).Scan(&state)
	if state != "interrupted" {
		t.Fatal(state)
	}
}
func TestModelFingerprintEndToEndTargetedThreeCallsAndIndependentQuality(t *testing.T) {
	for _, requested := range []string{"gpt-6.1-sol", "gemini-3.1-pro", "gemini-3.8-flash", "gemini-4-argon"} {
		t.Run(requested, func(t *testing.T) {
			raw, err := os.ReadFile("fingerprint/reference-fixture.json")
			if err != nil {
				t.Fatal(err)
			}
			var samples map[string][]struct {
				Text string `json:"text"`
			}
			json.Unmarshal(raw, &samples)
			var calls atomic.Int32
			var targeted atomic.Bool
			targeted.Store(true)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer gateway-secret" {
					t.Error("wrong credential")
				}
				switch r.URL.Path {
				case "/v1/observability/runtime":
					writeJSON(w, 200, map[string]any{"capabilities": map[string]bool{"targeted_responses": targeted.Load(), "targeted_unconfigured_models": true}})
				case "/v1/channel-settings/providers":
					writeJSON(w, 200, map[string]any{"providers": []configuredProvider{{Provider: "native", Base: "https://upstream.example/v1/responses", API: "upstream-secret"}}})
				case "/v1/responses":
					if r.Header.Get("X-Uni-API-Provider") != "native" {
						t.Error("non-targeted call")
					}
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["model"] != requested || body["stream"] != true {
						t.Error("incorrect parameters", body["model"])
					}
					if fingerprintModels[requested].Effort == "low" {
						if mustJSON(body["reasoning"]) != `{"effort":"low"}` {
							t.Error("wrong reference effort")
						}
					} else if body["reasoning"] != nil {
						t.Error("reference uses provider default effort")
					}
					n := calls.Add(1)
					text := samples[fingerprintModels[requested].ClassID][(n-1)%3].Text
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}}}}))
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer gateway.Close()
			s, a, src := bindingFixture(t, "https://site.example")
			src.Base = gateway.URL
			s.control.saveSource(context.Background(), src, false)
			target := fingerprintTarget{Source: src.ID, Provider: "native", Model: requested}
			if w := fingerprintRequest(t, s, a.Owner, "POST", []fingerprintTarget{target}); w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if !s.fingerprintOne(ctx) {
				t.Fatal("no job")
			}
			if calls.Load() != 3 {
				t.Fatal("expected exactly three calls", calls.Load())
			}
			w := fingerprintRequest(t, s, a.Owner, "GET", nil)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var out struct {
				Data []fingerprintRecord `json:"data"`
			}
			json.Unmarshal(w.Body.Bytes(), &out)
			var saved *fingerprintRecord
			for i := range out.Data {
				if out.Data[i].Source == src.ID {
					saved = &out.Data[i]
				}
			}
			if saved == nil || saved.State != "done" || !strings.Contains(string(saved.Result), `"status":"match"`) || saved.Revision == "" {
				t.Fatalf("bad result %+v", saved)
			}
			if strings.Contains(w.Body.String(), "secret") {
				t.Fatal("credential leaked")
			}
			var n int
			s.control.db.QueryRow(`SELECT count(*) FROM console_channel_checks WHERE source_id=$1`, src.ID).Scan(&n)
			if n != 0 {
				t.Fatal("fingerprint polluted legacy quality")
			}
			targeted.Store(false)
			_, err = s.prepareFingerprint(ctx, target)
			if err == nil || calls.Load() != 3 {
				t.Fatal("capability preflight did not fence paid request", err)
			}
		})
	}
}
func TestModelFingerprintSiteResultInvalidatesAfterKeyRotation(t *testing.T) {
	s, a, _ := bindingFixture(t, "https://site.example")
	encrypted, _ := s.control.encrypt("key-one")
	s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key,active) VALUES($1,1,'group','openai',$2,true)`, a.ID, encrypted)
	target := fingerprintTarget{Account: a.ID, Group: 1, Model: checkModel}
	if err := enqueueFingerprint(context.Background(), s.control.db, target); err != nil {
		t.Fatal(err)
	}
	s.control.db.Exec(`UPDATE console_model_fingerprints SET state='done',revision=$2,result='{"status":"match","probability":1}' WHERE scope=$1`, target.scope(), tokenHash(a.Base+"\n"+encrypted))
	read := func(owner string) []fingerprintRecord {
		w := fingerprintRequest(t, s, owner, "GET", nil)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		var out struct {
			Data []fingerprintRecord `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		var records []fingerprintRecord
		for _, r := range out.Data {
			if r.Account == a.ID {
				records = append(records, r)
			}
		}
		return records
	}
	if len(read(a.Owner)) != 1 || len(read("other-owner")) != 0 {
		t.Fatal("scope isolation")
	}
	rotated, _ := s.control.encrypt("key-two")
	s.control.db.Exec(`UPDATE console_sub_targets SET encrypted_key=$2 WHERE account_id=$1`, a.ID, rotated)
	if len(read(a.Owner)) != 0 {
		t.Fatal("stale evidence survived credential change")
	}
}

func TestModelFingerprintAutomaticBatchOnlySupportedAvailableModels(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
			subNativeSSE(w, subFixtureProbeModel(r), "test")
			return
		}
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		subSSE(w, "21", body.Model)
	}))
	defer gateway.Close()
	previous := subHTTP
	subHTTP = gateway.Client()
	defer func() { subHTTP = previous }()
	s, a, _ := bindingFixture(t, gateway.URL)
	key, _ := s.control.encrypt("local-test-key")
	_, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key,active,state) VALUES($1,1,'group','openai',$2,true,'queued')`, a.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{checkModel, "gpt-6.1-sol", "gpt-5.5", "gemini-3.1-pro", "gemini-3.8-flash", "gemini-4-argon"} {
		if _, err = s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state) VALUES($1,1,$2,'queued')`, a.ID, model); err != nil {
			t.Fatal(err)
		}
	}
	s.control.db.Exec(`UPDATE console_sub_accounts SET job_id='batch',state='running' WHERE id=$1`, a.ID)
	if err = s.subTestTargets(context.Background(), a.ID, gateway.URL, "batch", false); err != nil {
		t.Fatal(err)
	}
	var n int
	s.control.db.QueryRow(`SELECT count(*) FROM console_model_fingerprints WHERE account_id=$1 AND state='queued'`, a.ID).Scan(&n)
	if n != 5 {
		t.Fatal("batch should queue exactly five supported fingerprints", n)
	}
	if w := fingerprintRequest(t, s, a.Owner, "GET", nil); w.Code != http.StatusOK {
		t.Fatalf("batch-queued fingerprints unreadable: HTTP %d: %s", w.Code, w.Body.String())
	}
	// Standalone quality detection must never enqueue a new identity probe.
	s.control.db.Exec(`UPDATE console_model_fingerprints SET state='done' WHERE account_id=$1`, a.ID)
	s.control.db.Exec(`UPDATE console_sub_models SET state='queued' WHERE account_id=$1 AND model=$2`, a.ID, checkModel)
	s.control.db.Exec(`UPDATE console_sub_accounts SET job_id='quality-only' WHERE id=$1`, a.ID)
	if err = s.subTestTargets(context.Background(), a.ID, gateway.URL, "quality-only", true); err != nil {
		t.Fatal(err)
	}
	s.control.db.QueryRow(`SELECT count(*) FROM console_model_fingerprints WHERE account_id=$1 AND state='queued'`, a.ID).Scan(&n)
	if n != 0 {
		t.Fatal("quality probe started a fingerprint", n)
	}
}

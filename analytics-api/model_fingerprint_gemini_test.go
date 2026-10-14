package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGeminiFingerprintSiteProtocolAndNativeBridge(t *testing.T) {
	raw, err := os.ReadFile("fingerprint/reference-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string][]struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"gemini-3.1-pro", "gemini-3.8-flash", "gemini-4-argon"} {
		t.Run(model, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1beta/models/"+model+":streamGenerateContent" || r.URL.RawQuery != "alt=sse" || r.Header.Get("x-goog-api-key") != "site-secret" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Request-ID") == "" {
					t.Error("wrong Gemini request", r.URL.Path)
				}
				var body struct {
					Model      string `json:"model"`
					Generation struct {
						Max      int `json:"maxOutputTokens"`
						Thinking *struct {
							Level string `json:"thinkingLevel"`
						} `json:"thinkingConfig"`
					} `json:"generationConfig"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != "" || body.Generation.Max != 8192 {
					t.Error("wrong body", body)
				}
				if model == "gemini-4-argon" {
					if body.Generation.Thinking == nil || body.Generation.Thinking.Level != "low" {
						t.Error("wrong Argon effort")
					}
				} else if body.Generation.Thinking != nil {
					t.Error("older Gemini reference uses default effort")
				}
				n := calls.Add(1)
				subNativeSSE(w, model, fixtures[fingerprintModels[model].ClassID][(n-1)%3].Text)
			}))
			defer up.Close()
			s, a, _ := bindingFixture(t, up.URL)
			key, _ := s.control.encrypt("site-secret")
			if _, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,encrypted_key,active) VALUES($1,1,'group','gemini',$2,true)`, a.ID, key); err != nil {
				t.Fatal(err)
			}
			target := fingerprintTarget{Account: a.ID, Group: 1, Model: model}
			ctx := context.Background()
			plan, err := s.prepareFingerprint(ctx, target)
			if err != nil || plan.config["format"] != "gemini" {
				t.Fatal("native fallback", plan.config["format"], err)
			}
			// A saved availability protocol takes precedence over the model default.
			for protocol, format := range map[string]string{"gemini": "gemini", "chat": "openai", "messages": "anthropic", "responses": ""} {
				_, err = s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,1,$2,'done',$3) ON CONFLICT(account_id,group_id,model) DO UPDATE SET result=excluded.result`, a.ID, model, mustJSON(map[string]any{"availability": map[string]string{"protocol": protocol}}))
				if err != nil {
					t.Fatal(err)
				}
				p, err := s.prepareFingerprint(ctx, target)
				if err != nil {
					t.Fatal(err)
				}
				got, _ := p.config["format"].(string)
				if got != format {
					t.Fatal(protocol, got)
				}
			}
			result, err := runFingerprintViaTransport(ctx, up.Client(), plan.config)
			if err != nil || calls.Load() != 3 || !strings.Contains(string(result), `"status":"match"`) || strings.Contains(string(result), "site-secret") {
				t.Fatalf("native result calls=%d err=%v: %s", calls.Load(), err, result)
			}
		})
	}
}

package main

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type creditsTransport func(*http.Request) (*http.Response, error)

func (f creditsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenRouterCreditsUseAccountCreditDifference(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		code               int
	}{
		{"credits", `{"data":{"total_credits":30.5,"total_usage":12.25}}`, "", 200},
		{"key quota is not wallet", `{"data":{"limit_remaining":25}}`, "openrouter_fetch_failed", 200},
		{"denied", `{"error":"untrusted remote message"}`, "openrouter_management_key_rejected", 403},
		{"rate-limited", ``, "rate_limited", 429},
		{"redirect", ``, "openrouter_fetch_failed", 302},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: creditsTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.String() != openRouterCreditsURL || r.Header.Get("Authorization") != "Bearer local-test-management" {
					t.Fatal("incorrect credits request")
				}
				return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Location": {"https://untrusted.example/"}}}, nil
			})}
			got := readOpenRouterCredits(context.Background(), client, "local-test-management")
			if calls != 1 || got.ErrorReason != tc.reason || got.ProviderKind != "openrouter" {
				t.Fatal("unexpected balance result", got, calls)
			}
			if tc.reason == "" {
				if got.Status != "ok" || got.Amount == nil || math.Abs(*got.Amount-18.25) > 1e-9 || got.CheckedAt == 0 {
					t.Fatal("actual account balance not returned", got)
				}
			} else if got.Status != "error" || got.Amount != nil {
				t.Fatal("failure invented an amount", got)
			}
		})
	}
}

func TestOpenRouterCreditsRequireAnAuthenticatedAndExplicitlyLinkedChannel(t *testing.T) {
	s := managementBindingTestService(t)
	t.Setenv("OPENROUTER_MANAGEMENT_KEY", "local-test-management")
	t.Setenv("OPENROUTER_BALANCE_SOURCE_ID", "source")
	t.Setenv("OPENROUTER_BALANCE_PROVIDER", "configured")
	read := func(provider, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/sources/source/openrouter-balance?provider="+provider, nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), "local-test-management") {
			t.Fatal("management key leaked")
		}
		return w
	}
	if got := read("configured", ""); got.Code != 401 {
		t.Fatal("anonymous request accessed credits", got.Code)
	}
	token, err := s.control.newSession(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if got := read("different", token); got.Code != 200 || !strings.Contains(got.Body.String(), `"error_reason":"openrouter_management_key_required"`) {
		t.Fatal("allowed implicit wallet sharing", got.Code, got.Body.String())
	}
	if got := read("configured", token); got.Code != 200 || !strings.Contains(got.Body.String(), `"error_reason":"openrouter_channel_unverified"`) {
		t.Fatal("allowed credits for unknown channel", got.Code, got.Body.String())
	}
	for _, base := range []string{"http://openrouter.ai/api/v1", "https://openrouter.ai.example/api/v1", "https://openrouter.ai:8443", "https://user@openrouter.ai"} {
		if officialOpenRouter(base) {
			t.Errorf("non-official OpenRouter origin accepted: %s", base)
		}
	}
	if !officialOpenRouter("https://openrouter.ai/api/v1/responses") {
		t.Fatal("official API URL was rejected")
	}
}

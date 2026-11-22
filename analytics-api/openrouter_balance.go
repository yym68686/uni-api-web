package main

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

const openRouterCreditsURL = "https://openrouter.ai/api/v1/credits"

func officialOpenRouter(base string) bool {
	u, err := url.Parse(base)
	return err == nil && u.Scheme == "https" && u.Hostname() == "openrouter.ai" &&
		(u.Port() == "" || u.Port() == "443") && u.User == nil
}

func readOpenRouterCredits(ctx context.Context, client *http.Client, key string) subAccountBalance {
	bal := subAccountBalance{Status: "error", ProviderKind: "openrouter", ErrorReason: "openrouter_fetch_failed"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openRouterCreditsURL, nil)
	if err != nil {
		return bal
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Do(req)
	if err != nil {
		return bal
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		bal.ErrorReason = "openrouter_management_key_rejected"
		return bal
	case http.StatusTooManyRequests:
		bal.ErrorReason = "rate_limited"
		return bal
	case http.StatusOK:
	default:
		return bal
	}
	var result struct {
		Data struct {
			Credits *float64 `json:"total_credits"`
			Usage   *float64 `json:"total_usage"`
		} `json:"data"`
	}
	const maxBody = 16 << 10
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(raw) > maxBody || json.Unmarshal(raw, &result) != nil || result.Data.Credits == nil || result.Data.Usage == nil {
		return bal
	}
	amount := *result.Data.Credits - *result.Data.Usage
	if math.IsNaN(amount) || math.IsInf(amount, 0) {
		return bal
	}
	return subAccountBalance{Amount: &amount, CheckedAt: time.Now().Unix(), Status: "ok", ProviderKind: "openrouter"}
}

func (s *Service) openRouterBalance(w http.ResponseWriter, r *http.Request) {
	id, provider := r.PathValue("id"), r.URL.Query().Get("provider")
	key := env("OPENROUTER_MANAGEMENT_KEY", "")
	// A management key reports an account/organization wallet, not a model
	// key's quota. Never attach one wallet to every OpenRouter configuration.
	if key == "" || id == "" || provider == "" || id != env("OPENROUTER_BALANCE_SOURCE_ID", "") || provider != env("OPENROUTER_BALANCE_PROVIDER", "") {
		writeJSON(w, 200, subAccountBalance{Status: "error", ProviderKind: "openrouter", ErrorReason: "openrouter_management_key_required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	src, err := s.control.source(ctx, id)
	if err == nil {
		providers, err := effectiveProviders(ctx, src, true)
		if err == nil {
			for _, item := range providers {
				if item.Provider == provider && officialOpenRouter(item.Base) {
					writeJSON(w, 200, readOpenRouterCredits(ctx, http.DefaultClient, key))
					return
				}
			}
		}
	}
	writeJSON(w, 200, subAccountBalance{Status: "error", ProviderKind: "openrouter", ErrorReason: "openrouter_channel_unverified"})
}

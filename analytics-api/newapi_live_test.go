package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in operator smoke test. Credentials stay outside the repository;
// only its newly-created dedicated keys/session are removed on completion.
func TestNewAPILiveAdapter(t *testing.T) {
	path := os.Getenv("TEST_NEWAPI_CREDENTIALS")
	if path == "" {
		t.Skip("explicit new-api test account required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("credentials unavailable")
	}
	var cfg struct{ Base, Username, Password string }
	if json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("credentials invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	kind, err := detectSite(ctx, cfg.Base)
	if err != nil || kind != "newapi" {
		t.Fatal("automatic detection failed", err)
	}
	auth, _, err := newAPILoginSession(ctx, cfg.Base, cfg.Username, cfg.Password, "", "")
	if err != nil {
		t.Fatal("login", err)
	}
	cfg.Password = ""
	auth.QuotaPerUnit, err = newAPIQuota(ctx, cfg.Base)
	if err != nil {
		t.Fatal(err)
	}
	var user newAPIUser
	if err = newAPIJSON(ctx, cfg.Base, "GET", "/api/user/self", &auth, nil, &user); err != nil || user.ID != auth.UserID || !strings.EqualFold(user.Username, cfg.Username) {
		t.Fatal("identity", err)
	}
	s, account, _ := newAPITestService(t)
	encoded, _ := json.Marshal(auth)
	encrypted, _ := s.control.encrypt(string(encoded))
	if _, err = s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET base=$2,encrypted_auth=$3,job_id='live-smoke',state='running' WHERE id=$1`, account, cfg.Base, encrypted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		latest, _, e := s.newAPIAuth(cleanup, account)
		if e != nil {
			latest = auth
		}
		_ = newAPIJSON(cleanup, cfg.Base, "POST", "/api/user/auth/logout", &latest, nil, nil)
	})
	groups, err := s.newAPIGroups(ctx, account, cfg.Base)
	if err != nil || len(groups) == 0 {
		t.Fatal("groups", err)
	}
	var group subRemoteGroup
	for _, g := range groups {
		if g.Name == "default" {
			group = g
		}
	}
	if group.ID == 0 {
		t.Fatal("authorized smoke group absent")
	}
	var models []string
	if err = s.newAPICall(ctx, account, cfg.Base, "GET", "/api/user/models?group=default", nil, &models); err != nil {
		t.Fatal("models", err)
	}
	for _, model := range []string{"gpt-6-sol", "gpt-6-astra"} {
		found := false
		for _, m := range models {
			found = found || m == model
		}
		if !found {
			t.Fatal("smoke model unavailable", model)
		}
	}
	if os.Getenv("TEST_NEWAPI_RECEIPTS_ONLY") == "1" {
		var list struct {
			Items []newAPILog `json:"items"`
		}
		if err = s.newAPICall(ctx, account, cfg.Base, "GET", "/api/log/self?type=2&p=1&page_size=100", nil, &list); err != nil {
			t.Fatal(err)
		}
		printed := 0
		for _, log := range list.Items {
			if log.At < time.Now().Add(-2*time.Hour).Unix() || (log.Model != "gpt-6-sol" && log.Model != "gpt-6-astra") {
				continue
			}
			var meta map[string]any
			_ = json.Unmarshal([]byte(log.Other), &meta)
			if enc, ok := meta["expr_b64"].(string); ok {
				raw, _ := base64.StdEncoding.DecodeString(enc)
				meta["expression"] = string(raw)
			}
			allowed := map[string]any{}
			for _, k := range []string{"billing_mode", "matched_tier", "group_ratio", "user_group_ratio", "request_rules", "billing_tokens", "expression", "model_ratio", "completion_ratio", "cache_tokens", "cache_creation_tokens", "billing_source"} {
				if v, ok := meta[k]; ok {
					allowed[k] = v
				}
			}
			receipt, _, err := newAPIReceipt(log, auth.QuotaPerUnit)
			if err != nil {
				t.Fatal(err)
			}
			result := subUsageReceipt(receipt)
			t.Logf("billing model=%s input_price_confirmed=%t output_price_confirmed=%t", log.Model, result.InputPrice != nil, result.OutputPrice != nil)
			if result.InputPrice == nil || result.OutputPrice == nil {
				t.Fatal("expected supported expression price", mustJSON(allowed))
			}
			printed++
			if printed >= 6 {
				break
			}
		}
		return
	}

	key, err := s.newAPIEnsureKey(ctx, account, cfg.Base, subKeyName(account, group.ID), group.ID)
	if err != nil {
		t.Fatal("create", err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if e := s.newAPICall(cleanup, account, cfg.Base, "DELETE", "/api/token/"+strconv.FormatInt(key.ID, 10), nil, nil); e != nil {
			t.Error("smoke key cleanup failed", key.ID, e)
		}
	})
	reused, err := s.newAPIEnsureKey(ctx, account, cfg.Base, subKeyName(account, group.ID), group.ID)
	if err != nil || reused.ID != key.ID {
		t.Fatal("key reuse", err)
	}
	business, err := s.newAPIEnsureKey(ctx, account, cfg.Base, "uni-console-route-"+account+"-"+strconv.FormatInt(group.ID, 10), group.ID)
	if err != nil {
		t.Fatal("business key", err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if e := s.newAPICall(cleanup, account, cfg.Base, "DELETE", "/api/token/"+strconv.FormatInt(business.ID, 10), nil, nil); e != nil {
			t.Error("business key cleanup failed", business.ID, e)
		}
	})
	if key.ID == business.ID || key.Key == business.Key || key.Quota != nil || business.Quota != nil {
		t.Fatal("probe/business isolation or unlimited failed")
	}
	probes := []subProbe{
		subProbeStream(ctx, subHTTP, cfg.Base, key.Key, "say test", "gpt-6-sol"),
	}
	tool := subProbeToolUse(ctx, subHTTP, cfg.Base, key.Key, "gpt-6-sol")
	compact := subProbeCompaction(ctx, subHTTP, cfg.Base, key.Key, "gpt-6-sol")
	quality := subRunQualityProbe(ctx, subHTTP, cfg.Base, key.Key)
	probes = append(probes, tool, compact, quality.Quality)
	for _, p := range probes {
		t.Logf("probe model=%s status=%s model_match=%s", p.RequestedModel, p.Status, p.ModelMatch)
		if p.Status != "success" && p.Status != "supported" {
			t.Fatal("probe did not pass", p.Status, p.Message)
		}
	}
	q := url.Values{"page": {"1"}, "api_key_id": {strconv.FormatInt(key.ID, 10)}}
	var listing struct {
		Items []subUsageLog `json:"items"`
	}
	for attempt := 0; attempt < 4; attempt++ {
		err = s.newAPIUsage(ctx, account, cfg.Base, q, &listing)
		if err != nil {
			t.Fatal("receipts", err)
		}
		if len(listing.Items) >= len(probes) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	total := 0.0
	for _, p := range probes {
		receipt := subMatchUsage(subUsageTask{key: key.ID, group: group.ID, ids: p.RequestIDs}, listing.Items)
		if receipt == nil || receipt.ActualCost == nil {
			t.Fatal("missing exact receipt", p.RequestedModel)
		}
		total += *receipt.ActualCost
		t.Logf("receipt model=%s cost=%g input_price=%v output_price=%v", p.RequestedModel, *receipt.ActualCost, receipt.InputPrice != nil, receipt.OutputPrice != nil)
	}
	// Force one rejected read to exercise durable Cookie rotation.
	auth.Access = "expired-smoke-access"
	encoded, _ = json.Marshal(auth)
	encrypted, _ = s.control.encrypt(string(encoded))
	_, err = s.control.db.ExecContext(ctx, `UPDATE console_sub_accounts SET encrypted_auth=$2 WHERE id=$1`, account, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.newAPICall(ctx, account, cfg.Base, "GET", "/api/user/self", nil, &user); err != nil {
		t.Fatal("refresh", err)
	}
	t.Logf("automatic_detection=newapi groups=%d models=%d exact_receipts=%d total_usd=%.8f key_reuse=true isolated_unlimited_keys=true refresh=true", len(groups), len(models), len(probes), total)
}

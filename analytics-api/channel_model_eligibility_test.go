package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfiguredModelEligibilityIsScopedAndReusesConfirmedSiteEvidence(t *testing.T) {
	s, account, src := bindingFixture(t, "https://upstream.test/v1")
	ctx := context.Background()
	provider := configuredProvider{Provider: "native", Base: "https://upstream.test/v1/responses", API: "fixture-key"}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("validation must not mutate gateway", r.Method)
		}
		switch r.URL.Path {
		case "/v1/channel-settings/providers":
			writeJSON(w, 200, map[string]any{"providers": []configuredProvider{provider}})
		case "/v1/model-channels":
			writeJSON(w, 200, map[string]any{"data": []batchCatalogRow{{Provider: "native", Model: "public", Upstream: "actual"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	src.Base = gateway.URL
	check := func(current, desired map[string]string, want bool) {
		t.Helper()
		err := s.validateConfiguredModelChanges(ctx, src, "native", account.Owner, current, desired)
		if (err == nil) != want {
			t.Fatalf("availability want %v got %v", want, err)
		}
	}
	saved := map[string]string{"public": "actual"}
	check(saved, saved, true) // Existing serving pair survives a failed/missing probe.
	check(saved, map[string]string{"new-alias": "actual"}, false)
	seed := func(fingerprint, state, status string, stamp int64) {
		t.Helper()
		_, err := s.control.db.Exec(`INSERT INTO console_configured_checks(source_id,provider,kind,model,source_target,fingerprint,state,result) VALUES($1,'native','model','public',$2,$3,$4,$5) ON CONFLICT(source_id,provider,kind,model) DO UPDATE SET fingerprint=excluded.fingerprint,state=excluded.state,result=excluded.result`, src.ID, controlTarget(src), fingerprint, state, mustJSON(subResult{Model: "public", CheckedAt: stamp, Availability: subProbe{Status: status}}))
		if err != nil {
			t.Fatal(err)
		}
	}
	fingerprint := configuredProbeFingerprint(src, provider)
	seed("old-credentials", "done", "success", 10)
	check(nil, saved, false)
	seed(fingerprint, "done", "error", 10)
	check(nil, saved, false)
	seed(fingerprint, "running", "success", 10)
	check(nil, saved, false)
	seed(fingerprint, "done", "success", 10)
	check(nil, saved, true)
	check(nil, map[string]string{"alias": "unprobed"}, false)
	other := src
	other.ID = "other-source"
	if s.validateConfiguredModelChanges(ctx, other, "native", account.Owner, nil, saved) == nil {
		t.Fatal("borrowed another source's check")
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES($1,7,'fixture','openai')`, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES($1,$2,42,7)`, account.ID, tokenHash("fixture-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,7,'actual','done',$2)`, account.ID, mustJSON(subResult{Model: "actual", CheckedAt: 20, Availability: subProbe{Status: "error"}})); err != nil {
		t.Fatal(err)
	}
	check(nil, saved, false) // Newer failed site result overrides the old native success.
	if _, err := s.control.db.Exec(`DELETE FROM console_configured_checks WHERE source_id=$1`, src.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_models SET result=$2 WHERE account_id=$1 AND model='actual'`, account.ID, mustJSON(subResult{Model: "actual", CheckedAt: 30, Availability: subProbe{Status: "success"}})); err != nil {
		t.Fatal(err)
	}
	check(nil, saved, true) // Normalized site path plus exact credential binding.
	if s.validateConfiguredModelChanges(ctx, src, "native", "foreign-owner", nil, saved) == nil {
		t.Fatal("borrowed foreign account evidence")
	}
	provider.API = []string{"fixture-key", "unbound-key"}
	check(nil, saved, false) // Every key must be bound before reusing site evidence.
}

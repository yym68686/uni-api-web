package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestServiceTierFilterBeforePaginationAndExpiry(t *testing.T) {
	now := time.Unix(1795520000, 0)
	filter, err := parseManagementQuery(url.Values{"filters": {`{"model":"public","fast":"supported","ultrafast":"supported"}`}})
	if err != nil || !filter.hasServiceTiers() {
		t.Fatal(filter, err)
	}
	if _, err := parseManagementQuery(url.Values{"filters": {`{"fast":"fake"}`}}); !errors.Is(err, errManagementFilters) {
		t.Fatal("invalid filter accepted", err)
	}
	digest := strings.Repeat("a", 64)
	scope := strings.Repeat("b", 64)
	in := managementQueryInput{ModelsRevision: "stable"}
	add := func(src, name string, models ...string) {
		in.Channels = append(in.Channels, managementChannel{Engine: "gpt",
			subInstalledChannel: subInstalledChannel{SourceID: src, Provider: name, Name: name, Models: models, ServiceTierScope: scope}})
	}
	evidence := func(src, provider, tier, model, fingerprint, status string, at int64) {
		checked := serviceTierResult{Binding: digest, Status: status, CheckedAt: at}
		if status == "supported" {
			checked.VerifiedBinding, checked.VerifiedAt = digest, at
		}
		in.Checks = append(in.Checks, configuredCheck{Source: src, Provider: provider, Kind: tier, Model: model,
			Fingerprint: fingerprint, State: "done", Result: json.RawMessage(mustJSON(checked))})
	}
	for i := range 30 {
		name := fmt.Sprintf("valid-%02d", i)
		add("s", name, "public")
		for _, tier := range []string{"fast", "ultrafast"} {
			evidence("s", name, tier, "public", scope, "supported", now.Unix())
		}
	}
	add("s", "across-models", "public", "other")
	evidence("s", "across-models", "fast", "public", scope, "supported", now.Unix())
	evidence("s", "across-models", "ultrafast", "other", scope, "supported", now.Unix())
	add("s", "across-members", "public")
	add("second", "across-members", "public")
	evidence("s", "across-members", "fast", "public", scope, "supported", now.Unix())
	evidence("second", "across-members", "ultrafast", "public", scope, "supported", now.Unix())
	add("s", "new-credential", "public")
	evidence("s", "new-credential", "fast", "public", digest, "supported", now.Unix())
	add("s", "miss", "public")
	evidence("s", "miss", "fast", "public", scope, "not_honored", now.Unix())
	add("s", "upstream-denied", "public")
	evidence("s", "upstream-denied", "fast", "public", scope, "unsupported", now.Unix())
	in.Accounts = []subAccount{{ID: "site", Targets: []subTarget{{GroupID: 1, Active: true}, {GroupID: 2, Active: true}}}}
	in.Imports = []subInstalledChannel{{SourceID: "s", Provider: "installed", Models: []string{"alias"},
		ModelMappings: map[string]string{"alias": "public"}, ServiceTierScope: scope, AccountID: "site", GroupID: 1}}
	evidence("s", "installed", "fast", "alias", scope, "supported", now.Unix())
	evidence("s", "installed", "ultrafast", "alias", scope, "supported", now.Unix())
	read := func(f managementQueryFilters, at time.Time, cursor string) (managementQueryPage, error) {
		rows, err := buildManagementQueryRows(in, f.Model, f.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		return buildManagementQueryPageFromRows(in, rows, f, managementFilterEvidence{
			ServiceTiers: managementServiceTiers(in, rows, f, at.Unix()),
		}, "owner", []byte("test-key"), cursor, at)
	}
	first, err := read(filter, now, "")
	if err != nil || first.Total != 31 || len(first.Rows) != 25 || first.Rows[0].ID != "site:1" {
		t.Fatalf("wrong first page: total=%d, rows=%d err=%v", first.Total, len(first.Rows), err)
	}
	second, err := read(filter, now.Add(time.Second), first.NextCursor)
	if err != nil || len(second.Rows) != 6 {
		t.Fatal("filter lost results across page boundary", len(second.Rows), err)
	}
	withoutModel := filter
	withoutModel.Model = ""
	if all, err := read(withoutModel, now, ""); err != nil || all.Total != first.Total {
		t.Fatal("two tiers incorrectly combined across different models/members", all.Total, err)
	}
	expired := now.Add(time.Duration(serviceTierTTL) * time.Second)
	if _, err := read(filter, expired, first.NextCursor); !errors.Is(err, errManagementRevision) {
		t.Fatal("expiry did not fence the old cursor", err)
	}
	if out, err := read(filter, expired, ""); err != nil || out.Total != 0 {
		t.Fatal("expired support included", out.Total, err)
	}
	for status, name := range map[string]string{"not_honored": "miss", "unsupported": "upstream-denied"} {
		page, err := read(managementQueryFilters{Fast: status, Model: "public"}, now, "")
		if err != nil || page.Total != 1 || page.Rows[0].Target.Name != name {
			t.Fatal(status, page.Total, err)
		}
	}
}

func TestServiceTierSettingsChoicesRequireCurrentBoundEvidence(t *testing.T) {
	ctx := context.Background()
	store, err := newControlStore(isolatedControlTestDSN(t), strings.Repeat("fixture-master", 4))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	liveScope := strings.Repeat("a", 64)
	binding := strings.Repeat("b", 64)
	state := map[string]string{"fast": "unconfigured", "ultrafast": "disabled_verified"}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/channel-settings/providers" || r.Header.Get("Authorization") != "Bearer admin" {
			t.Error("unexpected gateway operation")
			http.NotFound(w, r)
			return
		}
		writeJSON(w, 200, map[string]any{"providers": []configuredProvider{{Provider: "p", ServiceTierScope: liveScope, ServiceTierRouting: state}}})
	}))
	defer gateway.Close()
	src := controlSource{sourceView: sourceView{ID: "s", Name: "fixture", Base: gateway.URL}, Key: "observer", ConfigKey: "admin"}
	if _, err := store.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	scope := serviceTierScope(src, liveScope)
	s := &Service{control: store}
	in := channelSettingMutation{TierChoices: []serviceTierRoutingChoice{{Provider: "p", Scope: scope, Tier: "fast", Enabled: true}, {Provider: "p", Scope: scope, Tier: "ultrafast", Enabled: false}}}
	if _, code, err := s.resolveServiceTierChoices(ctx, src, in, ""); err == nil || code != 409 {
		t.Fatal("enabled unverified route", code, err)
	}
	now := time.Now().Unix() - 5
	insert := func(provider, fingerprint string, at int64) {
		t.Helper()
		r := serviceTierResult{Status: "supported", Binding: binding, VerifiedBinding: binding, CheckedAt: at, VerifiedAt: at}
		_, err := store.db.Exec(`INSERT INTO console_configured_checks(source_id,provider,kind,model,source_target,state,result,fingerprint)
 VALUES ($1,$2,'fast','m',$3,'done',$4,$5)
 ON CONFLICT(source_id,provider,kind,model) DO UPDATE SET result=excluded.result,fingerprint=excluded.fingerprint`, src.ID, provider, controlTarget(src), mustJSON(r), fingerprint)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("other", scope, now)
	insert("p", strings.Repeat("c", 64), now)
	if _, code, err := s.resolveServiceTierChoices(ctx, src, in, ""); err == nil || code != 409 {
		t.Fatal("accepted unrelated provider or scope", code, err)
	}
	insert("p", scope, now-serviceTierTTL)
	if _, code, err := s.resolveServiceTierChoices(ctx, src, in, ""); err == nil || code != 409 {
		t.Fatal("accepted expired evidence", code, err)
	}
	insert("p", scope, now)
	out, code, err := s.resolveServiceTierChoices(ctx, src, in, "")
	if err != nil || len(out.Changes) != 1 || len(out.TierChoices) != 0 {
		t.Fatal(out, code, err)
	}
	set := out.Changes[0].Set
	if len(set) != 3 || set["/preferences/service_tier_disabled/fast"] != false ||
		set["/preferences/service_tier_disabled/ultrafast"] != true ||
		set["/preferences/service_tier_evidence/"+binding+"/fast"] != now {
		t.Fatal(set)
	}
	in.Changes = []channelSettingChange{{Provider: "p", Set: map[string]any{"/api": "new-credential"}}}
	if _, code, err := s.resolveServiceTierChoices(ctx, src, in, ""); err == nil || code != 400 {
		t.Fatal("accepted simultaneous credential change", code, err)
	}
	in.Changes = nil
	in.TierChoices[0].Scope = strings.Repeat("d", 64)
	if _, code, err := s.resolveServiceTierChoices(ctx, src, in, ""); err == nil || code != 409 {
		t.Fatal("accepted stale scope", code, err)
	}
}

func TestServiceTierProbeNegotiationDoesNotRepeatOrImportVerification(t *testing.T) {
	binding, scope := strings.Repeat("a", 64), strings.Repeat("b", 64)
	version, realCalls, checks := 0, 0, 0
	status, observed := "supported", "ultrafast"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer config-key" {
			t.Error("wrong gateway credential")
			http.Error(w, "forbidden", 403)
			return
		}
		if r.URL.Path == "/v1/observability/runtime" {
			writeJSON(w, 200, map[string]int{"service_tier_probe_version": version})
			return
		}
		var req struct {
			Verify bool   `json:"verify_only"`
			Scope  string `json:"expected_scope"`
		}
		if r.URL.Path != serviceTierProbePath || json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("invalid probe call")
			http.NotFound(w, r)
			return
		}
		if req.Verify {
			checks++
			writeJSON(w, 200, serviceTierResult{Status: "ready", Binding: binding, Scope: scope})
			return
		}
		if req.Scope != scope {
			t.Error("missing credential fence")
		}
		realCalls++
		writeJSON(w, 200, serviceTierResult{Status: status, Binding: binding, Scope: scope, Observed: observed,
			CheckedAt: time.Now().Unix(), VerifiedAt: time.Now().Unix(), VerifiedBinding: strings.Repeat("f", 64)})
	}))
	defer upstream.Close()
	src := controlSource{sourceView: sourceView{ID: "s", Base: upstream.URL}, Key: "observer", ConfigKey: "config-key"}
	job := configuredCheck{Source: "s", Provider: "p", Model: "m", Kind: "ultrafast"}
	var got any
	svc := &Service{}
	if err := svc.runServiceTierCheck(context.Background(), src, &job, &got); err == nil || realCalls != 0 || checks != 0 {
		t.Fatal("old gateway did not fail closed", err, realCalls, checks)
	}
	version = 1
	if err := svc.runServiceTierCheck(context.Background(), src, &job, &got); err != nil {
		t.Fatal(err)
	}
	if out := got.(serviceTierResult); out.VerifiedBinding != binding || out.Scope != "" || job.Fingerprint != serviceTierScope(src, scope) || realCalls != 1 || checks != 1 {
		t.Fatal("wrong call or verification", out, realCalls, checks)
	}
	status, observed = "not_honored", "default"
	if err := svc.runServiceTierCheck(context.Background(), src, &job, &got); err != nil {
		t.Fatal(err)
	}
	if out := got.(serviceTierResult); out.Status != "not_honored" || out.VerifiedAt != 0 || out.VerifiedBinding != "" || realCalls != 2 {
		t.Fatal("fallback or supplied verification was promoted", out, realCalls)
	}
}

func TestServiceTierFailedCheckPreservesOnlySameBinding(t *testing.T) {
	store, err := newControlStore(isolatedControlTestDSN(t), strings.Repeat("fixture-master", 4))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	if _, err := store.saveSource(context.Background(), controlSource{
		sourceView: sourceView{ID: "s", Name: "fixture", Base: "http://127.0.0.1"}, Key: "mock-key",
	}, false); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	binding, scope := strings.Repeat("a", 64), strings.Repeat("b", 64)
	old := serviceTierResult{Status: "supported", Observed: "fast", CheckedAt: now - 100,
		VerifiedAt: now - 100, Binding: binding, VerifiedBinding: binding}
	s := &Service{control: store}
	job := configuredCheck{Source: "s", Provider: "p", Model: "m", Kind: "fast", Fingerprint: scope}
	for _, scenario := range []struct {
		name, nextScope, state string
		raw                    any
		verified               bool
	}{
		{"transport", scope, "error", nil, true},
		{"miss", scope, "done", mustJSON(serviceTierResult{Status: "not_honored", Observed: "default", Binding: binding, CheckedAt: now}), true},
		{"denied", scope, "done", mustJSON(serviceTierResult{Status: "unsupported", Binding: binding, CheckedAt: now}), false},
		{"new-scope", binding, "error", nil, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := store.db.Exec(`INSERT INTO console_configured_checks(source_id,provider,kind,model,source_target,run_id,state,result,fingerprint)
 VALUES ('s','p','fast','m','fixture','run','running',$1,$2)
 ON CONFLICT(source_id,provider,kind,model) DO UPDATE SET state='running',result=$1,fingerprint=$2`, mustJSON(old), scope); err != nil {
				t.Fatal(err)
			}
			job.Fingerprint = scenario.nextScope
			s.saveServiceTierResult(context.Background(), job, "run", scenario.state, "", scenario.raw)
			var raw []byte
			var state string
			if err := store.db.QueryRow("SELECT result,state FROM console_configured_checks WHERE provider='p'").Scan(&raw, &state); err != nil || state != scenario.state {
				t.Fatal(state, err)
			}
			var latest serviceTierResult
			if raw != nil && json.Unmarshal(raw, &latest) != nil {
				t.Fatal("bad receipt", string(raw))
			}
			if verified := serviceTierFilterStatus(latest, now) == "supported"; verified != scenario.verified ||
				(verified && latest.VerifiedAt != old.VerifiedAt) {
				t.Fatal("wrong retained evidence", latest)
			}
		})
	}
}

func TestServiceTierImportRequiresCompatibleReceiptAndPreservesOptOut(t *testing.T) {
	ctx := context.Background()
	store, err := newControlStore(isolatedControlTestDSN(t), strings.Repeat("fixture-master", 4))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	sha := func(c string) string { return strings.Repeat(c, 64) }
	receipt, compatible, candidate := sha("a"), sha("b"), sha("c")
	var reads int
	checker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		var in struct {
			VerifyOnly bool `json:"verify_only"`
			Candidate  struct {
				Key    string            `json:"api_key"`
				Models map[string]string `json:"models"`
			}
		}
		if r.URL.Path != serviceTierProbePath || r.Header.Get("Authorization") != "Bearer admin" ||
			json.NewDecoder(r.Body).Decode(&in) != nil || !in.VerifyOnly || in.Candidate.Models["alias"] != "model" {
			t.Error("expected local-only verification of the exact model mapping")
			http.Error(w, "invalid", 400)
			return
		}
		digest := compatible
		if in.Candidate.Key != "matching-fixture-key" {
			digest = sha("d")
		}
		writeJSON(w, 200, map[string]any{"models": map[string]serviceTierImportBinding{
			"alias": {Binding: candidate, Compatibility: digest},
		}})
	}))
	defer checker.Close()
	src := controlSource{sourceView: sourceView{ID: "s", Name: "fixture", Base: checker.URL}, Key: "observer", ConfigKey: "admin"}
	if _, err := store.saveSource(ctx, src, false); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Unix() - 120
	insert := func(provider, tier, status string, checked, verified int64) {
		t.Helper()
		value := serviceTierResult{Status: status, CheckedAt: checked, VerifiedAt: verified,
			Binding: receipt, VerifiedBinding: receipt, Compatibility: compatible, UpstreamModel: "model"}
		if _, err := store.db.Exec(`INSERT INTO console_configured_checks
 (source_id,provider,kind,model,source_target,state,result) VALUES ($1,$2,$3,'former-alias',$4,'done',$5)`,
			src.ID, provider, tier, controlTarget(src), mustJSON(value)); err != nil {
			t.Fatal(err)
		}
	}
	insert("previous", "fast", "supported", at, at)
	insert("previous", "ultrafast", "supported", at-serviceTierTTL, at-serviceTierTTL)
	s := &Service{control: store}
	plan := func(key string) serviceTierImportPlan {
		t.Helper()
		got, err := s.planServiceTierImport(ctx, src, "prospective", "https://fixture.invalid/v1/responses", key, map[string]string{"alias": "model"})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	result := plan("matching-fixture-key")
	settings := importTierSettings(result, subImportInput{ServiceTierEnabled: map[string]bool{"fast": false}})
	path := "/preferences/service_tier_evidence/" + candidate + "/fast"
	if result.counts["fast"] != 1 || result.counts["ultrafast"] != 0 || settings[path] != at ||
		settings["/preferences/service_tier_disabled/fast"] != true || len(settings) != 2 {
		t.Fatal("wrong bound import settings", settings)
	}
	if got := plan("different-fixture-key"); len(got.set) != 0 {
		t.Fatal("evidence followed a different key")
	}
	insert("newer", "fast", "unsupported", at+30, 0)
	if got := plan("matching-fixture-key"); len(got.set) != 0 || reads != 3 {
		t.Fatal("older support overrode a newer explicit rejection", reads)
	}
}

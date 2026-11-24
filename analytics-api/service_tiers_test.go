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

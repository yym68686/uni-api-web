package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExtendedOptimizationCommitsProtocolAndTierInOneSnapshot(t *testing.T) {
	s, account, source := bindingFixture(t, "https://site.example/prefix")
	ctx := context.Background()
	key := "key-" + strings.Repeat("a", 64)
	root := subProviderName(account.ID, 1, key)
	messages, responses := protocolProvider(root, "messages"), protocolProvider(root, "responses")
	business := "installed-gpt"
	base := account.Base + "/v1/messages"
	scope, binding := strings.Repeat("a", 64), strings.Repeat("b", 64)
	old := retainedChannel{Provider: messages, KeyID: key, Base: base, Key: "fixture", Models: []string{"alias"},
		Definition: json.RawMessage(mustJSON(map[string]any{"provider": messages, "base_url": base, "api": []string{"fixture"},
			"model": []any{map[string]any{"claude-upstream": "alias"}}}))}
	initial := retainedSnapshot{Version: 2, Channels: []retainedChannel{old}, Settings: map[string]json.RawMessage{
		messages: json.RawMessage(`{"set":{"/preferences/timeout":42},"remove":[]}`),
		business: json.RawMessage(`{"set":{"/preferences/timeout":35,"/preferences/service_tier_disabled/fast":true},"remove":[]}`),
	}, Rules: []retainedRule{
		{KeyID: key, Model: "alias", Order: []string{"peer", messages}, Disabled: []string{messages}},
		{KeyID: key, Model: "new-model", Order: []string{"peer"}, Disabled: []string{"unrelated"}},
	}}
	current, revision, restores := initial, "r1", 0
	state := func() map[string]any {
		return map[string]any{"instance_id": "boot", "revision": revision, "temporary_channel_restore": true, "automatic_engine": true,
			"channel_settings": true, "temporary_channels": current.Channels, "rules": current.Rules}
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/channel-controls":
			writeJSON(w, 200, state())
		case "/v1/channel-settings/export":
			defs := map[string]json.RawMessage{}
			for _, c := range current.Channels {
				defs[c.Provider] = c.Definition
			}
			writeJSON(w, 200, map[string]any{"revision": revision, "temporary_definitions": defs, "channel_settings": current.Settings})
		case "/v1/channel-settings/providers":
			writeJSON(w, 200, map[string]any{"providers": []configuredProvider{{Provider: business, ServiceTierScope: scope,
				ServiceTierRouting: map[string]string{"fast": "disabled_verified", "ultrafast": "enabled"}}}})
		case "/v1/model-channels":
			rows := []batchCatalogRow{
				{Provider: "peer", Model: "alias", Upstream: "claude-upstream", Engine: "claude"},
				{Provider: messages, Model: "alias", Upstream: "claude-upstream", Engine: "claude"},
				{Provider: "peer", Model: "new-model", Upstream: "new-model", Engine: "codex"},
				{Provider: "peer", Model: "gpt-6.2-sol", Upstream: "gpt-6.2-sol", Engine: "codex"},
			}
			for _, c := range current.Channels {
				if c.Provider == responses {
					for _, model := range c.Models {
						rows = append(rows, batchCatalogRow{Provider: responses, Model: model, Upstream: model, Engine: "codex"})
					}
				}
			}
			writeJSON(w, 200, map[string]any{"data": rows})
		case "/v1/channel-controls/restore":
			var in struct {
				Revision string           `json:"revision"`
				Snapshot retainedSnapshot `json:"snapshot"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Revision != revision {
				http.Error(w, "stale", 409)
				return
			}
			restores++
			current, revision = in.Snapshot, "r"+strconv.Itoa(restores+1)
			writeJSON(w, 200, state())
		default:
			t.Errorf("unexpected gateway request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	source.Base = gateway.URL
	if _, err := s.control.saveSource(ctx, source, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform,active) VALUES($1,1,'group','openai',true)`, account.ID); err != nil {
		t.Fatal(err)
	}
	answer := subResult{Availability: subProbe{Status: "success", Protocol: "responses"}}
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES ($1,1,'new-model','done',$2)`, account.ID, mustJSON(answer)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	sourceScope := serviceTierScope(source, scope)
	for tier, status := range map[string]string{"fast": "supported", "ultrafast": "not_honored"} {
		result := serviceTierResult{Status: status, Binding: binding, VerifiedBinding: binding, CheckedAt: now, VerifiedAt: now}
		if _, err := s.control.db.Exec(`INSERT INTO console_configured_checks
 (source_id,provider,kind,model,source_target,state,fingerprint,result) VALUES ($1,$2,$3,'gpt-model',$4,'done',$5,$6)`,
			source.ID, business, tier, controlTarget(source), sourceScope, mustJSON(result)); err != nil {
			t.Fatal(err)
		}
	}
	token, err := s.control.newSession(ctx, account.Owner)
	if err != nil {
		t.Fatal(err)
	}
	call := func(in channelBatchInput) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/sources/"+source.ID+"/channel-batch", strings.NewReader(mustJSON(in)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	input := channelBatchInput{Revision: "r1", Part: "optimize",
		TierChoices: []optimizationTierChoice{
			{serviceTierRoutingChoice{Provider: business, Scope: sourceScope, Tier: "fast", Enabled: true}, []string{"gpt-model"}},
			{serviceTierRoutingChoice{Provider: business, Scope: sourceScope, Tier: "ultrafast", Enabled: false}, []string{"gpt-model"}},
		}, Targets: []channelBatchTarget{{
			Key: key, Provider: messages, Account: account.ID, Group: 1,
			Current:        map[string]string{"alias": "claude-upstream"},
			Models:         map[string]string{"alias": "claude-upstream", "new-model": "new-model"},
			Positions:      map[string]int{"alias": 2, "new-model": 2},
			SplitProtocols: map[string]string{"new-model": "responses"},
		}}}
	input.TierChoices[0].Scope = scope
	if got := call(input); got.Code != 409 || restores != 0 {
		t.Fatal("wrong credential evidence accepted", got.Code, got.Body.String())
	}
	input.TierChoices[0].Scope = sourceScope
	input.TierChoices[1].EvidenceModels = []string{"gpt-other"}
	if got := call(input); got.Code != 409 || restores != 0 {
		t.Fatal("missing tier evidence accepted", got.Code, got.Body.String())
	}
	input.TierChoices[1].EvidenceModels = []string{"gpt-model"}
	input.Targets[0].SplitProtocols["new-model"] = "chat"
	if got := call(input); got.Code != 400 || restores != 0 {
		t.Fatal("changed protocol accepted", got.Code, got.Body.String())
	}
	input.Targets[0].SplitProtocols["new-model"] = "responses"
	if got := call(input); got.Code != 200 || !strings.Contains(got.Body.String(), `"tier_changes":2`) || restores != 1 {
		t.Fatal(got.Code, got.Body.String(), restores)
	}
	if len(current.Channels) != 2 || !reflect.DeepEqual(current.Channels[0].Models, old.Models) ||
		current.Channels[0].Base != base || canonicalSettings(current.Settings[messages]) != canonicalSettings(initial.Settings[messages]) {
		t.Fatal("original protocol, models or settings changed")
	}
	added := current.Channels[1]
	if added.Provider != responses || added.Base != account.Base+"/v1/responses" || !reflect.DeepEqual(added.Models, []string{"new-model"}) {
		t.Fatal("new model did not get the selected protocol")
	}
	if !reflect.DeepEqual(current.Rules[0], initial.Rules[0]) || !reflect.DeepEqual(current.Rules[1].Order, []string{"peer", responses}) ||
		!reflect.DeepEqual(current.Rules[1].Disabled, initial.Rules[1].Disabled) {
		t.Fatal("existing routes not preserved", current.Rules)
	}
	settings := string(current.Settings[business])
	for _, field := range []string{`"/preferences/timeout":35`, `"/preferences/service_tier_disabled/fast":false`,
		`"/preferences/service_tier_disabled/ultrafast":true`, "/preferences/service_tier_evidence/" + binding + "/fast"} {
		if !strings.Contains(settings, field) {
			t.Fatal("missing expected setting", field, settings)
		}
	}
	if got := call(input); got.Code != 409 || restores != 1 {
		t.Fatal("stale revision applied", got.Code, got.Body.String())
	}

	// This time the protocol sibling already exists. The preview submits the
	// addition directly to that binding, with no target for Messages.
	answer.Availability.Protocol = "chat"
	_, err = s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES ($1,1,'gpt-6.2-sol','done',$2)`, account.ID, mustJSON(answer))
	if err != nil {
		t.Fatal(err)
	}
	reuse := channelBatchInput{Revision: revision, Part: "optimize", Targets: []channelBatchTarget{{
		Key: key, Provider: responses, Account: account.ID, Group: 1,
		Current:   map[string]string{"new-model": "new-model"},
		Models:    map[string]string{"new-model": "new-model", "gpt-6.2-sol": "gpt-6.2-sol"},
		Positions: map[string]int{"new-model": 2, "gpt-6.2-sol": 2},
	}}}
	if got := call(reuse); got.Code != 400 || restores != 1 {
		t.Fatal("changed detection protocol accepted", got.Code, got.Body.String())
	}
	answer.Availability.Protocol = "responses"
	_, err = s.control.db.Exec(`UPDATE console_sub_models SET result=$2 WHERE account_id=$1 AND group_id=1 AND model='gpt-6.2-sol'`, account.ID, mustJSON(answer))
	if err != nil {
		t.Fatal(err)
	}
	if got := call(reuse); got.Code != 200 || restores != 2 {
		t.Fatal("existing protocol binding not updated", got.Code, got.Body.String())
	}
	var reused retainedChannel
	for _, channel := range current.Channels {
		switch channel.Provider {
		case responses:
			reused = channel
		case messages:
			if !reflect.DeepEqual(channel, old) {
				t.Fatal("reusing Responses changed the original Messages binding")
			}
		}
	}
	if len(current.Channels) != 2 || reused.Base != account.Base+"/v1/responses" ||
		!reflect.DeepEqual(reused.Models, []string{"gpt-6.2-sol", "new-model"}) {
		t.Fatal("Responses was duplicated or the existing model was lost", reused)
	}
}

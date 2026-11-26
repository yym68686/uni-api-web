package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func signedFingerprintFixture(t *testing.T, key ed25519.PrivateKey, sequence int64, corruptBank bool) []byte {
	t.Helper()
	bundle := fingerprintBundle{Schema: 1, Sequence: sequence, Files: map[string][]byte{}}
	for _, f := range fingerprintBundleFiles {
		data, err := os.ReadFile("fingerprint/" + f)
		if err != nil {
			t.Fatal(err)
		}
		bundle.Files[f] = data
	}
	var models, protocols map[string]json.RawMessage
	for f, m := range map[string]*map[string]json.RawMessage{"models.json": &models, "protocols.json": &protocols} {
		if err := json.Unmarshal(bundle.Files[f], m); err != nil {
			t.Fatal(err)
		}
		(*m)["fixture-new-model"] = (*m)["gpt-6-astra"]
		bundle.Files[f], _ = json.Marshal(m)
	}
	if corruptBank {
		bundle.Files["data/unified_bank.json.gz"] = []byte("invalid gzip")
	}
	payload, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(struct {
		Payload   json.RawMessage `json:"payload"`
		Signature []byte          `json:"signature"`
	}{payload, ed25519.Sign(key, payload)})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFingerprintBundleChecksExpandedSamplingProfiles(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	base, _, err := verifyFingerprintBundle(signedFingerprintFixture(t, key, 42, false), pub)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, protocol, effort, deferred string
		max                              int
		ok                               bool
	}{
		{"messages with output cap", "messages", "default", "request_contract", 4096, true},
		{"chat minimal", "chat", "minimal", "offline_score", 0, true},
		{"gemini cannot use none", "gemini", "none", "request_contract", 0, false},
		{"unbounded output", "chat", "low", "request_contract", 32769, false},
		{"unrecognized deferred reason", "chat", "none", "untrusted diagnostic", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var models map[string]fingerprintReference
			var protocols map[string]string
			var manifest map[string]json.RawMessage
			files := map[string][]byte{}
			for k, v := range base.Files {
				files[k] = v
			}
			for name, out := range map[string]any{"models.json": &models, "protocols.json": &protocols, "manifest.json": &manifest} {
				if err := json.Unmarshal(files[name], out); err != nil {
					t.Fatal(err)
				}
			}
			const fixture = "fixture-new-model"
			protocols[fixture] = tc.protocol
			models[fixture] = fingerprintReference{ClassID: fingerprintModels["gpt-6-astra"].ClassID, Effort: tc.effort, MaxTokens: tc.max}
			manifest["deferred"] = json.RawMessage(mustJSON(map[string]string{"fixture-deferred": tc.deferred}))
			files["models.json"], files["protocols.json"], files["manifest.json"] = []byte(mustJSON(models)), []byte(mustJSON(protocols)), []byte(mustJSON(manifest))
			_, catalog, err := parseFingerprintBundle([]byte(mustJSON(fingerprintBundle{Schema: base.Schema, Sequence: base.Sequence, Files: files})))
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected bundle validation result: %v", err)
			}
			if tc.ok && (catalog.models[fixture] != models[fixture] || catalog.deferred["fixture-deferred"] != tc.deferred) {
				t.Fatal("sampling contract or deferred diagnostic was lost")
			}
		})
	}
}

func TestFingerprintCatalogPersistsOnlyVerifiedAndValidatedGenerations(t *testing.T) {
	s, account, source := bindingFixture(t, "https://example.test")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pub, signer, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		closeFingerprintCatalog(currentFingerprintCatalog())
		publishedFingerprintCatalog.Store(nil)
	}
	t.Cleanup(func() {
		reset()
		_, _ = s.control.db.Exec(`DELETE FROM console_fingerprint_catalog WHERE sequence IN (42,43)`)
	})
	baseKey := currentFingerprintCatalog().referenceKey
	raw := signedFingerprintFixture(t, signer, 42, false)
	bad := append([]byte(nil), raw...)
	bad[len(bad)/2] ^= 1
	if err = s.activateFingerprintBundle(ctx, bad, pub, true); err == nil || currentFingerprintCatalog().referenceKey != baseKey {
		t.Fatal("bad signature published", err)
	}
	if err = s.activateFingerprintBundle(ctx, raw, pub, true); err != nil {
		t.Fatal(err)
	}
	ref := currentFingerprintCatalog()
	if ref.sequence != 42 || ref.referenceKey == baseKey || !slices.Contains(ref.names, "fixture-new-model") {
		t.Fatal("Go and validated scorer did not activate together")
	}
	for _, fixture := range []struct {
		model, key string
		retained   int
	}{{"fixture-new-model", ref.referenceKey, 1}, {"gpt-6-astra", baseKey, 0}} {
		target := fingerprintTarget{Source: source.ID, Provider: "fixture", Model: fixture.model}
		if err = enqueueFingerprint(ctx, s.control.db, target); err != nil {
			t.Fatal(err)
		}
		if _, err = s.control.db.ExecContext(ctx, `UPDATE console_model_fingerprints SET sample_state=jsonb_build_object('reference_key',$3::text,'entries','[{}]'::jsonb) WHERE scope=$1 AND model=$2`, target.scope(), target.Model, fixture.key); err != nil {
			t.Fatal(err)
		}
		records, err := readManagementFingerprintsFrom(ctx, s.control.db, account.Owner)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(records, func(record fingerprintRecord) bool {
			return record.Source == source.ID && record.Model == fixture.model && record.RetainedSamples == fixture.retained
		}) {
			t.Fatalf("%s: expected %d retained samples for current reference", fixture.model, fixture.retained)
		}
	}
	w := fingerprintRequest(t, s, account.Owner, "GET", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"fixture-new-model"`) {
		t.Fatal("API did not expose dynamic catalog", w.Code)
	}
	if err = s.activateFingerprintBundle(ctx, signedFingerprintFixture(t, signer, 43, true), pub, true); err == nil ||
		currentFingerprintCatalog() != ref {
		t.Fatal("invalid candidate replaced active scorer", err)
	}
	reset()
	if err = s.loadSavedFingerprintBundle(ctx, pub); err != nil || currentFingerprintCatalog().sequence != 42 {
		t.Fatal("last known good was not restored", err)
	}
}

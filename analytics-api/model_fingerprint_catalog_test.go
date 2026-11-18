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

func TestFingerprintCatalogPersistsOnlyVerifiedAndValidatedGenerations(t *testing.T) {
	s, account, _ := bindingFixture(t, "https://example.test")
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

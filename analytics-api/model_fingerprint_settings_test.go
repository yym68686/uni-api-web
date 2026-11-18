package main

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestFingerprintSettingsRequireOptInForNewProfiles(t *testing.T) {
	s, account, src := bindingFixture(t, "https://site.example")
	ctx := context.Background()
	other := account.Owner + "-other"
	t.Cleanup(func() {
		_, _ = s.control.db.Exec(`DELETE FROM console_fingerprint_preferences WHERE owner IN ($1,$2)`, account.Owner, other)
	})
	base := currentFingerprintCatalog()
	extended := *base
	const added = "fixture-new-model"
	extended.models = maps.Clone(base.models)
	extended.models[added] = base.models[checkModel]
	extended.names = append(slices.Clone(base.names), added)
	publishedFingerprintCatalog.Store(&extended)
	t.Cleanup(func() { publishedFingerprintCatalog.Store(nil) })
	request := func(owner, method, body string, want int) (out struct {
		fingerprintPreferences
		Supported []string `json:"supported_models"`
	}) {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/model-fingerprints/settings", strings.NewReader(body))
		if owner != "" {
			token, err := s.control.newSession(ctx, owner)
			if err != nil {
				t.Fatal(err)
			}
			r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s settings: want %d, got %d: %s", method, want, w.Code, w.Body.String())
		}
		if want == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	request("", "GET", "", 401)
	defaults := request(account.Owner, "GET", "", 200)
	if !slices.Contains(defaults.Supported, added) || !slices.Contains(defaults.Models, checkModel) || slices.Contains(defaults.Models, added) {
		t.Fatal("newly supported model should be visible but not enabled by default", defaults)
	}
	count := func(tgt fingerprintTarget, want int) {
		t.Helper()
		var n int
		err := s.control.db.QueryRow(`SELECT count(*) FROM console_model_fingerprints WHERE scope=$1 AND model=$2`, tgt.scope(), tgt.Model).Scan(&n)
		if err != nil || n != want {
			t.Fatalf("%+v: want %d jobs, got %d, %v", tgt, want, n, err)
		}
	}
	enq := func(tgt fingerprintTarget, owner string) {
		t.Helper()
		if err := enqueueAutomaticFingerprint(ctx, s.control.db, tgt, owner); err != nil {
			t.Fatal(err)
		}
	}
	native := fingerprintTarget{Source: src.ID, Provider: "fixture", Model: added}
	enq(native, account.Owner)
	count(native, 0)
	request(account.Owner, "PUT", `{"enabled_models":["fixture-new-model"]}`, 200)
	request(account.Owner, "PUT", `{}`, 400)
	request(account.Owner, "PUT", `{"enabled_models":["unknown-model"]}`, 400)
	enq(native, other)
	count(native, 0)
	enq(native, account.Owner)
	count(native, 1)

	// Explicitly saving none must not revert to the defaults on read or enqueue.
	request(account.Owner, "PUT", `{"enabled_models":[]}`, 200)
	if out := request(account.Owner, "GET", "", 200); out.Models == nil || len(out.Models) != 0 {
		t.Fatal("empty opt-out not retained", out)
	}
	site := fingerprintTarget{Account: account.ID, Group: 7, Model: checkModel}
	enq(site, account.Owner)
	count(site, 0)
	if err := enqueueFingerprint(ctx, s.control.db, site); err != nil {
		t.Fatal(err)
	}
	count(site, 1)
}

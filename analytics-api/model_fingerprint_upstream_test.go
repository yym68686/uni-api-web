package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type fingerprintHeadTransport func(*http.Request) (*http.Response, error)

func (f fingerprintHeadTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestFingerprintUpstreamRefreshKeepsKnownGoodOnContractChange(t *testing.T) {
	s, _, _ := bindingFixture(t, "https://example.test")
	ctx := context.Background()
	base := currentFingerprintCatalog()
	reset := func() {
		closeFingerprintCatalog(currentFingerprintCatalog())
		publishedFingerprintCatalog.Store(nil)
	}
	t.Cleanup(func() {
		reset()
		_, _ = s.control.db.Exec(`DELETE FROM console_fingerprint_upstream_catalog`)
	})
	// The fallback simulates a legacy catalog at the very same upstream SHA.
	commit := base.commit
	client := &http.Client{Transport: fingerprintHeadTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != fingerprintUpstreamHeadURL || r.Header.Get("Accept") != "application/vnd.github.sha" {
			t.Fatalf("incorrect upstream request: %v", r)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(commit))}, nil
	})}
	makeBundle := func(ctx context.Context, head string, sequence int64) ([]byte, error) {
		files := map[string][]byte{}
		for _, path := range fingerprintBundleFiles {
			var err error
			if files[path], err = os.ReadFile("fingerprint/" + path); err != nil {
				return nil, err
			}
		}
		var manifest map[string]json.RawMessage
		if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
			return nil, err
		}
		manifest["commit"], _ = json.Marshal(head)
		manifest["catalog_builder_version"], _ = json.Marshal(fingerprintCatalogBuilderVersion)
		files["manifest.json"], _ = json.Marshal(manifest)
		var models map[string]json.RawMessage
		var protocols map[string]string
		_ = json.Unmarshal(files["models.json"], &models)
		_ = json.Unmarshal(files["protocols.json"], &protocols)
		models["fixture-new-model"] = models[checkModel]
		protocols["fixture-new-model"] = protocols[checkModel]
		if head[0] == 'b' {
			protocols["fixture-new-model"] = "chat"
		}
		files["models.json"], _ = json.Marshal(models)
		files["protocols.json"], _ = json.Marshal(protocols)
		return json.Marshal(fingerprintBundle{Schema: 1, Sequence: sequence, Files: files})
	}
	if err := s.refreshUpstreamFingerprintCatalog(ctx, client, makeBundle); err != nil {
		t.Fatal(err)
	}
	active := currentFingerprintCatalog()
	if active == base || active.commit != commit || active.builderVersion != fingerprintCatalogBuilderVersion || !fingerprintModel("fixture-new-model") {
		t.Fatal("compatible candidate did not activate at the unchanged upstream commit")
	}
	if err := s.refreshUpstreamFingerprintCatalog(ctx, client, func(context.Context, string, int64) ([]byte, error) {
		return nil, fmt.Errorf("unchanged head should skip derivation")
	}); err != nil {
		t.Fatal(err)
	}
	commit = strings.Repeat("b", 40)
	if err := s.refreshUpstreamFingerprintCatalog(ctx, client, makeBundle); err == nil || currentFingerprintCatalog() != active {
		t.Fatal("changed request contract did not preserve active catalog", err)
	}
	reset()
	if err := s.loadUpstreamFingerprintCatalog(ctx); err != nil || currentFingerprintCatalog().commit != active.commit {
		t.Fatal("last known good catalog was not restored", err)
	}
}

func TestFingerprintCatalogBuilderVersionRechecksAnUnchangedUpstreamCommit(t *testing.T) {
	for _, test := range []struct {
		name    string
		commit  string
		version int
		want    bool
	}{
		{name: "changed upstream", commit: "new", version: fingerprintCatalogBuilderVersion, want: true},
		{name: "new builder", commit: "same", version: fingerprintCatalogBuilderVersion - 1, want: true},
		{name: "already current", commit: "same", version: fingerprintCatalogBuilderVersion, want: false},
		{name: "newer saved builder", commit: "same", version: fingerprintCatalogBuilderVersion + 1, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := &fingerprintCatalog{commit: "same", builderVersion: test.version}
			if got := fingerprintCatalogNeedsRefresh(catalog, test.commit); got != test.want {
				t.Fatalf("refresh=%t, want %t", got, test.want)
			}
		})
	}
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func cleanFingerprintLeases(t *testing.T, s *Service) {
	t.Helper()
	t.Cleanup(func() {
		s.control.db.Exec(`DELETE FROM console_fingerprint_site_leases`)
		s.control.db.Exec(`UPDATE console_fingerprint_worker SET deadline=NULL WHERE id=true`)
	})
}

func TestFingerprintSiteUsesUpstreamHostAcrossPathsAndProtocols(t *testing.T) {
	for _, base := range []string{"https://SITE.example/v1/responses", "https://site.example:443/tenant/v1/messages", "http://site.example:80/v1", "https://site.example./"} {
		if got := fingerprintSite(base); got != "site.example" {
			t.Fatalf("%q: %q", base, got)
		}
	}
	for base, expected := range map[string]string{"https://site.example:8443/v1": "site.example:8443", "https://other.example/v1": "other.example", "https://[::1]:8443/v1": "[::1]:8443", "https://secret@site.example/v1": "", "file:///tmp/example": ""} {
		if got := fingerprintSite(base); got != expected {
			t.Fatalf("%q: %q", base, got)
		}
	}
}

func TestFingerprintFiftySitesAcrossReplicasAndCapacityRelease(t *testing.T) {
	s, _, src := bindingFixture(t, "https://site.example")
	cleanFingerprintLeases(t, s)
	ctx := context.Background()
	var providers []configuredProvider
	for i := range 51 {
		providers = append(providers, configuredProvider{Provider: fmt.Sprintf("channel-%02d", i), Base: fmt.Sprintf("https://site-%02d.example/v1/responses", i), API: "test"})
	}
	if err := s.saveConfiguredInventory(ctx, src, providers); err != nil {
		t.Fatal(err)
	}
	for _, p := range providers {
		if err := enqueueFingerprint(ctx, s.control.db, fingerprintTarget{Source: src.ID, Provider: p.Provider, Model: checkModel}); err != nil {
			t.Fatal(err)
		}
	}
	type claimed struct {
		target fingerprintTarget
		run    string
		err    error
	}
	start, results := make(chan struct{}), make(chan claimed, 51)
	other := &Service{control: s.control}
	for i := range 51 {
		worker := s
		if i%2 == 0 {
			worker = other
		}
		go func() {
			<-start
			target, run, err := worker.claimFingerprint(ctx)
			results <- claimed{target, run, err}
		}()
	}
	close(start)
	var active []claimed
	seen := map[string]bool{}
	for range 51 {
		r := <-results
		if r.err != nil {
			if !errors.Is(r.err, sql.ErrNoRows) {
				t.Fatal(r.err)
			}
			continue
		}
		if seen[r.target.Provider] {
			t.Fatal("duplicate claim", r.target)
		}
		seen[r.target.Provider] = true
		active = append(active, r)
	}
	if len(active) != 50 {
		t.Fatalf("expected global limit 50, got %d", len(active))
	}
	first := active[0]
	if err := s.finishFingerprint(ctx, first.target, first.run, "done", "", "", nil); err != nil {
		t.Fatal(err)
	}
	next, _, err := other.claimFingerprint(ctx)
	if err != nil || seen[next.Provider] {
		t.Fatal("capacity was not released to remaining site", next, err)
	}
}

func TestFingerprintSameSiteAccountsGroupsModelsAndNativeChannelsSerialize(t *testing.T) {
	s, a, src := bindingFixture(t, "https://site.example")
	cleanFingerprintLeases(t, s)
	ctx := context.Background()
	if err := s.saveConfiguredInventory(ctx, src, []configuredProvider{
		{Provider: "same-site", Base: "https://SITE.example:443/v1/responses", API: "test"},
		{Provider: "other-site", Base: "https://other.example/v1/responses", API: "test"},
	}); err != nil {
		t.Fatal(err)
	}
	_, secondAccount, _ := subUsageTestService(t)
	if _, err := s.control.db.Exec(`UPDATE console_sub_accounts SET base='https://site.example/v1' WHERE id=$1`, secondAccount); err != nil {
		t.Fatal(err)
	}
	targets := []fingerprintTarget{
		{Account: a.ID, Group: 1, Model: checkModel},
		{Account: a.ID, Group: 2, Model: checkModel},
		{Account: a.ID, Group: 1, Model: "gpt-6.1-sol"},
		{Account: a.ID, Group: 1, Model: "gemini-4-argon"},
		{Account: secondAccount, Group: 1, Model: checkModel},
		{Source: src.ID, Provider: "same-site", Model: checkModel},
		{Source: src.ID, Provider: "other-site", Model: checkModel},
	}
	for _, target := range targets {
		if err := enqueueFingerprint(ctx, s.control.db, target); err != nil {
			t.Fatal(err)
		}
	}
	first, run, err := s.claimFingerprint(ctx)
	if err != nil || first != targets[0] {
		t.Fatal(first, err)
	}
	second, _, err := s.claimFingerprint(ctx)
	if err != nil || second.Provider != "other-site" {
		t.Fatal("busy head blocked another site", second, err)
	}
	if _, _, err = s.claimFingerprint(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("same site overlapped", err)
	}
	if err = s.finishFingerprint(ctx, first, run, "done", "", "", nil); err != nil {
		t.Fatal(err)
	}
	third, _, err := s.claimFingerprint(ctx)
	if err != nil || third != targets[1] {
		t.Fatal("same-site queue did not advance", third, err)
	}
}

func TestFingerprintFreshConfigurationRebindsUnknownOrStaleSiteBeforeProbes(t *testing.T) {
	s, _, src := bindingFixture(t, "https://site.example")
	cleanFingerprintLeases(t, s)
	ctx := context.Background()
	a := fingerprintTarget{Source: src.ID, Provider: "unknown-a", Model: checkModel}
	b := fingerprintTarget{Source: src.ID, Provider: "stale-b", Model: checkModel}
	if err := s.saveConfiguredInventory(ctx, src, []configuredProvider{{Provider: b.Provider, Base: "https://old.example/v1", API: "test"}}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []fingerprintTarget{a, b} {
		if err := enqueueFingerprint(ctx, s.control.db, target); err != nil {
			t.Fatal(err)
		}
	}
	one, run1, err := s.claimFingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	two, run2, err := s.claimFingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.bindFingerprintSite(ctx, one, run1, "actual.example"); err != nil {
		t.Fatal(err)
	}
	if err = s.bindFingerprintSite(ctx, two, run2, "actual.example"); !errors.Is(err, errFingerprintSiteBusy) {
		t.Fatal("fresh same-site collision not fenced", err)
	}
	if err = s.finishFingerprint(ctx, two, run2, "queued", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.claimFingerprint(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("resolved site not remembered", err)
	}
	if err = s.finishFingerprint(ctx, one, run1, "done", "", "", nil); err != nil {
		t.Fatal(err)
	}
	next, _, err := s.claimFingerprint(ctx)
	if err != nil || next != two {
		t.Fatal(next, err)
	}
	if err = s.renewFingerprint(ctx, two, run2); err == nil {
		t.Fatal("old worker renewed new run")
	}
}

func TestFingerprintRollingReleaseCoordinatesLegacyWorker(t *testing.T) {
	s, _, src := bindingFixture(t, "https://site.example")
	cleanFingerprintLeases(t, s)
	ctx := context.Background()
	for _, provider := range []string{"first", "second"} {
		if err := enqueueFingerprint(ctx, s.control.db, fingerprintTarget{Source: src.ID, Provider: provider, Model: checkModel}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.control.db.Exec(`UPDATE console_fingerprint_worker SET run_id='old-worker',deadline=now()+interval '30 seconds' WHERE id=true`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.claimFingerprint(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("overlapped old singleton", err)
	}
	s.control.db.Exec(`UPDATE console_fingerprint_worker SET deadline=NULL WHERE id=true`)
	one, run, err := s.claimFingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.claimFingerprint(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.finishFingerprint(ctx, one, run, "done", "", "", nil); err != nil {
		t.Fatal(err)
	}
	var legacyFree bool
	if err = s.control.db.QueryRow(`SELECT deadline IS NULL OR deadline<now() FROM console_fingerprint_worker WHERE id=true`).Scan(&legacyFree); err != nil || legacyFree {
		t.Fatal("legacy worker can overlap remaining site", legacyFree, err)
	}
}

func TestFingerprintLoopOverlapsSitesButFinishesSameSiteInOrder(t *testing.T) {
	raw, err := os.ReadFile("fingerprint/reference-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples map[string][]struct {
		Text string `json:"text"`
	}
	if err = json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := map[string]int{}
	arrived, release := make(chan string, 3), make(chan struct{})
	var s *Service
	var src controlSource
	providers := []configuredProvider{
		{Provider: "a1", Base: "https://a.example/v1/responses", API: "test"},
		{Provider: "a2", Base: "https://a.example/v1/chat/completions", API: "test"},
		{Provider: "b", Base: "https://b.example/v1/responses", API: "test"},
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/observability/runtime":
			writeJSON(w, 200, map[string]any{"capabilities": map[string]bool{"targeted_responses": true}})
		case "/v1/channel-settings/providers":
			writeJSON(w, 200, map[string]any{"providers": providers})
		case "/v1/responses":
			p := r.Header.Get("X-Uni-API-Provider")
			mu.Lock()
			calls[p]++
			n := calls[p]
			mu.Unlock()
			if n == 1 {
				if p == "a2" {
					var state string
					queryErr := s.control.db.QueryRow(`SELECT state FROM console_model_fingerprints WHERE source_id=$1 AND provider='a1'`, src.ID).Scan(&state)
					if queryErr != nil || state != "done" {
						t.Errorf("same-site predecessor still active: %s, %v", state, queryErr)
					}
				} else {
					arrived <- p
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
			}
			text := samples["gpt-6-astra"][(n-1)%3].Text
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}}}}))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	s, _, src = bindingFixture(t, "https://site.example")
	cleanFingerprintLeases(t, s)
	src.Base = gateway.URL
	if _, err = s.control.saveSource(context.Background(), src, false); err != nil {
		t.Fatal(err)
	}
	if err = s.saveConfiguredInventory(context.Background(), src, providers); err != nil {
		t.Fatal(err)
	}
	for _, p := range providers {
		if err = enqueueFingerprint(context.Background(), s.control.db, fingerprintTarget{Source: src.ID, Provider: p.Provider, Model: checkModel}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	finished := make(chan struct{})
	go func() { defer close(finished); s.fingerprintLoop(ctx) }()
	defer func() { cancel(); <-finished }()
	seen := map[string]bool{}
	for range 2 {
		select {
		case p := <-arrived:
			seen[p] = true
		case <-ctx.Done():
			t.Fatal("different sites never overlapped")
		}
	}
	if !seen["a1"] || !seen["b"] {
		t.Fatal("wrong overlapping sites", seen)
	}
	close(release)
	for {
		var completed int
		if err = s.control.db.QueryRow(`SELECT count(*) FROM console_model_fingerprints WHERE source_id=$1 AND state='done'`, src.ID).Scan(&completed); err != nil {
			t.Fatal(err)
		}
		if completed == 3 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("jobs did not complete")
		case <-time.After(20 * time.Millisecond):
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range providers {
		if calls[p.Provider] != 3 {
			t.Errorf("%s sent %d requests", p.Provider, calls[p.Provider])
		}
	}
}

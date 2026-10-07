package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func observationFixture() controlSource {
	return controlSource{sourceView: sourceView{ID: "offline", Base: "https://offline.example"}, Key: "fixture"}
}

func waitObservationProbe(t *testing.T, c *sourceObservationCircuit, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		state := c.entries[id]
		done := state == nil || !state.probing
		c.mu.Unlock()
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("background probe did not finish")
}

func TestSourceObservationSkipsOfflineAndProbesWithoutBlocking(t *testing.T) {
	var c sourceObservationCircuit
	src := observationFixture()
	now := time.Now()
	transportErr := &sourceTransportError{message: "unavailable", cause: context.DeadlineExceeded}
	noProbe := func() error { t.Error("unexpected recovery probe"); return nil }
	if c.skip(src, now, noProbe) {
		t.Fatal("unknown source skipped")
	}
	c.failed(src, transportErr, now)
	if !c.skip(src, now.Add(time.Minute), noProbe) {
		t.Fatal("offline source was retried during cooldown")
	}
	healthy := src
	healthy.ID = "healthy"
	if c.skip(healthy, now, noProbe) {
		t.Fatal("offline source blocked an independent source")
	}

	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	probe := func() error {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !c.skip(src, now.Add(sourceObservationCooldown), probe) {
				t.Error("a refresh waited for recovery instead of skipping")
			}
		}()
	}
	wg.Wait() // Must complete while the probe is still blocked.
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("recovery probe not started")
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent recovery probes", calls.Load())
	}
	close(release)
	waitObservationProbe(t, &c, src.ID)
	if c.skip(src, now.Add(sourceObservationCooldown), noProbe) {
		t.Fatal("recovered source stayed unavailable")
	}
}

func TestSourceObservationRecoveryFailureAndConfigurationChange(t *testing.T) {
	var c sourceObservationCircuit
	src := observationFixture()
	now := time.Now()
	transportErr := &sourceTransportError{message: "unavailable", cause: errors.New("connection refused")}
	c.failed(src, transportErr, now)
	if !c.skip(src, now.Add(sourceObservationCooldown), func() error { return transportErr }) {
		t.Fatal("known offline source not skipped")
	}
	waitObservationProbe(t, &c, src.ID)
	if !c.skip(src, time.Now(), func() error { t.Error("failed probe did not reset cooldown"); return nil }) {
		t.Fatal("failed probe cleared circuit")
	}
	changed := src
	changed.Base = "https://replacement.example"
	if c.skip(changed, now, nil) {
		t.Fatal("changed source inherited old address health")
	}

	c.failed(src, transportErr, now)
	started, release := make(chan struct{}), make(chan struct{})
	c.skip(src, now.Add(sourceObservationCooldown), func() error { close(started); <-release; return transportErr })
	<-started
	c.reset(src.ID)
	close(release)
	waitObservationProbe(t, &c, src.ID)
	if c.skip(src, now, nil) {
		t.Fatal("in-flight probe overwrote a configuration reset")
	}
}

func TestSourceObservationDoesNotConfuseCancellationOrApplicationErrorsWithOffline(t *testing.T) {
	src := observationFixture()
	for _, err := range []error{
		nil,
		errors.New("source returned HTTP 403"),
		errors.New("invalid source JSON"),
		&sourceTransportError{message: "unavailable", cause: context.Canceled},
	} {
		var c sourceObservationCircuit
		c.failed(src, err, time.Now())
		if c.skip(src, time.Now(), nil) {
			t.Fatalf("non-offline error opened circuit: %v", err)
		}
	}
	s := &Service{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.observeSourceFailure(ctx, src, &sourceTransportError{message: "unavailable", cause: context.DeadlineExceeded})
	if s.skipUnavailableObservation(src) {
		t.Fatal("cancelled caller marked source offline")
	}

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "forbidden", 403) }))
	src.Base = gateway.URL
	for _, read := range []func() error{
		func() error { _, _, err := fetchSource(context.Background(), src, "/v1/api-keys", nil); return err },
		func() error {
			_, _, err := subGateway(context.Background(), src, "GET", "/v1/channel-controls", nil)
			return err
		},
	} {
		if err := read(); err == nil || sourceTransportFailed(err) {
			t.Fatal("HTTP permission error treated as offline", err)
		}
	}
	gateway.Close()
	_, _, err := fetchSource(context.Background(), src, "/v1/api-keys", nil)
	if !sourceTransportFailed(err) {
		t.Fatal("connection failure not classified", err)
	}
}

func TestObservationRoutesShareOfflineHealthAndPreserveExplicitOperations(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var offlineCalls, onlineCalls atomic.Int32
	offline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offlineCalls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
	}))
	defer offline.Close()
	online := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		onlineCalls.Add(1)
		switch r.URL.Path {
		case "/v1/api-keys":
			writeJSON(w, 200, map[string]any{"can_inspect_all": true, "data": []any{map[string]any{"key_id": "fixture-key", "prefix": "masked"}}})
		case "/v1/channel-controls":
			writeJSON(w, 200, map[string]any{"revision": "fixture", "temporary_channels": []any{}, "rules": []any{}})
		case "/v1/channel-settings/providers":
			writeJSON(w, 200, map[string]any{"providers": []any{map[string]any{"provider": "online", "base_url": "https://site.example"}}})
		default:
			writeJSON(w, 200, map[string]any{"data": []any{}})
		}
	}))
	defer online.Close()
	prefix := "observation-" + randomID()[:10]
	bad := controlSource{sourceView: sourceView{ID: prefix + "-offline", Name: "Offline fixture", Base: offline.URL}, Key: "fixture-key"}
	good := controlSource{sourceView: sourceView{ID: prefix + "-online", Name: "Online fixture", Base: online.URL}, Key: "fixture-key"}
	for _, src := range []controlSource{bad, good} {
		if _, err := store.saveSource(context.Background(), src, false); err != nil {
			t.Fatal(err)
		}
		defer store.db.Exec(`DELETE FROM console_sources WHERE id=$1`, src.ID)
	}
	s := &Service{control: store}
	token, err := store.newSession(context.Background(), prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec(`DELETE FROM console_sessions WHERE username=$1`, prefix)
	call := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := call("/v1/sources/" + bad.ID + "/proxy/v1/api-keys"); w.Code != 502 {
		t.Fatal("first offline observation was not reported", w.Code)
	}
	before := offlineCalls.Load()
	if before == 0 {
		t.Fatal("test did not contact offline fixture")
	}
	for i := 0; i < 2; i++ {
		for _, path := range []string{"/v1/sources/all/proxy/v1/api-keys", "/v1/sub2api/channels", "/v1/channel-sites", "/v1/channel-management"} {
			w := call(path)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "unavailable_sources") || !strings.Contains(w.Body.String(), "offline") && !strings.Contains(w.Body.String(), "Offline fixture") {
				t.Fatal("partial observation lost unavailable source", path, w.Code, w.Body.String())
			}
		}
	}
	s.refreshConfiguredBindings(context.Background())
	if offlineCalls.Load() != before {
		t.Fatal("refresh retried known offline source", before, offlineCalls.Load())
	}
	if onlineCalls.Load() < 10 {
		t.Fatal("healthy source stopped being read", onlineCalls.Load())
	}
	w := call("/v1/sources")
	var settings struct {
		Data []struct {
			ID          string `json:"id"`
			Unavailable bool   `json:"temporarily_unavailable"`
		} `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &settings) != nil {
		t.Fatal("source health unavailable to settings", w.Code)
	}
	found := false
	for _, item := range settings.Data {
		if item.ID == bad.ID {
			found = true
			if !item.Unavailable {
				t.Fatal("offline source not labeled in settings")
			}
		}
		if item.ID == good.ID && item.Unavailable {
			t.Fatal("healthy source labeled offline")
		}
	}
	if !found || offlineCalls.Load() != before {
		t.Fatal("reading settings lost or retried offline source")
	}
	// The shared low-level transport still contacts explicit operations; no
	// mutation or its validation is silently suppressed by observation health.
	_, _, _ = subGateway(context.Background(), bad, "POST", "/v1/channel-controls", map[string]any{"fixture": true})
	if offlineCalls.Load() <= before {
		t.Fatal("observation circuit blocked explicit operation")
	}
	var enabled bool
	if err := store.db.QueryRow(`SELECT enabled FROM console_sources WHERE id=$1`, bad.ID).Scan(&enabled); err != nil || !enabled {
		t.Fatal("observation circuit modified configuration intent", err)
	}
}

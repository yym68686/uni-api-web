package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubBindingsOverlapsIndependentReadsAndJoinsBeforeResponse(t *testing.T) {
	for _, mode := range []string{"success", "configured_error", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			s := managementBindingTestService(t)
			ctx := context.Background()
			started, release := make(chan struct{}, 3), make(chan struct{})
			var holding atomic.Bool
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if holding.Load() {
					started <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				if r.URL.Path == "/v1/channel-controls" {
					writeJSON(w, 200, map[string]any{"revision": "boot:1", "temporary_channels": []any{}})
				} else {
					writeJSON(w, 200, map[string]any{"data": []any{}})
				}
			}))
			defer gateway.Close()
			defer unblock()
			source := controlSource{sourceView: sourceView{ID: "fixture", Name: "Fixture", Base: gateway.URL}, Key: "local-only-gateway-secret"}
			if _, err := s.control.saveSource(ctx, source, false); err != nil {
				t.Fatal(err)
			}
			if err := s.saveConfiguredInventory(ctx, source, []configuredProvider{{Provider: "configured", Base: "https://site.test", API: "local-only-routing-secret"}}); err != nil {
				t.Fatal(err)
			}
			if mode == "configured_error" {
				if _, err := s.control.db.Exec(`UPDATE console_configured_channels SET key_hashes='{}'`); err != nil {
					t.Fatal(err)
				}
			}
			session, err := s.control.newSession(ctx, "owner")
			if err != nil {
				t.Fatal(err)
			}
			request := func(ctx context.Context, query string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", "/v1/sub2api/channels"+query, nil).WithContext(ctx)
				r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: session})
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				return w
			}
			baseline := request(ctx, "") // Legacy full view keeps its serial reads.
			lock, err := s.control.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback()
			var locker int
			if err := lock.QueryRow(`SELECT pg_backend_pid()`).Scan(&locker); err != nil {
				t.Fatal(err)
			}
			if _, err := lock.Exec(`LOCK TABLE console_configured_channels IN ACCESS EXCLUSIVE MODE`); err != nil {
				t.Fatal(err)
			}
			holding.Store(true)
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			finished := make(chan *httptest.ResponseRecorder, 1)
			go func() { finished <- request(readCtx, "?view=bindings&source_id=fixture") }()
			for range 3 {
				select {
				case <-started:
				case <-readCtx.Done():
					t.Fatal("gateway readers did not start")
				}
			}
			// A real database barrier proves overlap; it does not infer it from a
			// timing threshold. The old serial handler cannot reach this query
			// while all three gateway responses are still held above.
			overlapped := false
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				err := s.control.db.QueryRowContext(readCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE 'SELECT c.source_id,s.name,c.provider,%')`, locker).Scan(&overlapped)
				if err != nil {
					t.Fatal(err)
				}
				if overlapped {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !overlapped {
				t.Fatal("configured read still waits for gateway responses")
			}
			select {
			case <-finished:
				t.Fatal("response escaped before both reads joined")
			default:
			}
			if mode == "canceled" {
				cancel() // Both database and HTTP work must stop and be joined.
			} else {
				if err := lock.Rollback(); err != nil {
					t.Fatal(err)
				}
				unblock()
			}
			var result *httptest.ResponseRecorder
			select {
			case result = <-finished:
			case <-time.After(2 * time.Second):
				t.Fatal("independent reader leaked after completion/cancellation")
			}
			if mode == "canceled" {
				if result.Code != 503 {
					t.Fatal("canceled configured read published partial success", result.Code)
				}
				return
			}
			if result.Code != baseline.Code {
				t.Fatal("configured-binding failure behavior changed", baseline.Code, result.Code)
			}
			if mode == "configured_error" {
				if result.Code != 503 || result.Body.String() != baseline.Body.String() {
					t.Fatal("configured-binding error changed")
				}
				return
			}
			type bindingView struct {
				Data        []subInstalledChannel        `json:"data"`
				Labels      map[string]map[string]string `json:"labels"`
				Unavailable []string                     `json:"unavailable_sources"`
			}
			var want, got bindingView
			if err := json.Unmarshal(baseline.Body.Bytes(), &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(result.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) || len(got.Data) != 1 || got.Data[0].BindingStatus != "no_account" {
				t.Fatal("parallel read changed complete binding data")
			}
			for _, private := range []string{"local-only-gateway-secret", "local-only-routing-secret"} {
				if strings.Contains(result.Body.String(), private) {
					t.Fatal("credential escaped binding view")
				}
			}
		})
	}
}

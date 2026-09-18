package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type restoreTestObjects struct {
	*fakeStateObjects
	get func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error)
}

func (f *restoreTestObjects) GetObject(ctx context.Context, req *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return f.get(ctx, req)
}

func checkpointFixture(t *testing.T) (*checkpointStore, *fakeStateObjects) {
	t.Helper()
	source := stateTestEngine(t)
	err := source.Import(context.Background(), "one.jsonl", "e1", []Fact{{Schema: 1, EventID: "checkpoint-request", Kind: "request", AtMS: time.Now().UnixMilli(), Provider: "fixture", Model: "custom", Outcome: "success"}})
	if err != nil {
		t.Fatal(err)
	}
	objects := &fakeStateObjects{}
	store := newCheckpointStore(objects, Config{StateBucket: "state", StatePrefix: "analytics"})
	if err := store.save(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	return store, objects
}

func TestCheckpointRestoreRetriesTruncatedBodyAndTransientErrors(t *testing.T) {
	for _, failure := range []string{"truncated", "timeout", "throttled"} {
		t.Run(failure, func(t *testing.T) {
			store, objects := checkpointFixture(t)
			calls := 0
			store.client = &restoreTestObjects{objects, func(ctx context.Context, req *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
				calls++
				if calls == 1 {
					switch failure {
					case "timeout":
						<-ctx.Done()
						return nil, ctx.Err()
					case "throttled":
						return nil, &smithy.GenericAPIError{Code: "SlowDown"}
					default:
						out, err := objects.GetObject(ctx, req)
						out.Body.Close()
						out.Body = io.NopCloser(bytes.NewReader(objects.body[:len(objects.body)/2]))
						return out, err
					}
				}
				return objects.GetObject(ctx, req)
			}}
			service := &Service{engine: stateTestEngine(t), checkpoints: store}
			outcome := service.restoreCheckpoint(context.Background(), checkpointRestorePolicy{3, 100 * time.Millisecond, time.Millisecond})
			if outcome != "restored" || calls != 2 {
				t.Fatal(outcome, calls)
			}
			var n int
			if err := service.engine.DB.QueryRow("SELECT count(*) FROM imported_objects").Scan(&n); err != nil || n != 1 {
				t.Fatal("retry lost or duplicated replay state", n, err)
			}
		})
	}
}

func TestCheckpointRestoreStopsAtBoundAndDoesNotRetryPermanentFailures(t *testing.T) {
	for _, failure := range []string{"unavailable", "denied", "corrupt", "missing"} {
		t.Run(failure, func(t *testing.T) {
			store, objects := checkpointFixture(t)
			store.legacyKey = ""
			calls := 0
			store.client = &restoreTestObjects{objects, func(ctx context.Context, req *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
				calls++
				switch failure {
				case "unavailable":
					return nil, &smithy.GenericAPIError{Code: "ServiceUnavailable"}
				case "denied":
					return nil, &smithy.GenericAPIError{Code: "AccessDenied", Message: "secret signed URL"}
				case "missing":
					return nil, &smithy.GenericAPIError{Code: "NoSuchKey"}
				default:
					objects.body[0] ^= 1
					return objects.GetObject(ctx, req)
				}
			}}
			service := &Service{engine: stateTestEngine(t), checkpoints: store}
			outcome := service.restoreCheckpoint(context.Background(), checkpointRestorePolicy{3, time.Second, time.Millisecond})
			wantCalls, wantOutcome := 1, "rebuild_from_facts"
			if failure == "unavailable" {
				wantCalls = 3
			}
			if failure == "missing" {
				wantOutcome = "not_needed"
			}
			if calls != wantCalls || outcome != wantOutcome {
				t.Fatal(calls, outcome)
			}
		})
	}
}

func TestCheckpointFailedApplyRollsBackAllHistory(t *testing.T) {
	store, objects := checkpointFixture(t)
	target := stateTestEngine(t)
	ctx := context.Background()
	if err := target.Import(ctx, "existing.jsonl", "etag", []Fact{{Schema: 1, EventID: "existing", Kind: "request", AtMS: time.Now().UnixMilli(), Outcome: "success"}}); err != nil {
		t.Fatal(err)
	}
	// An empty replay index allows restoration, but existing derived rows must
	// still survive any failure, including validation after all three inserts.
	if _, err := target.DB.Exec("DELETE FROM imported_objects"); err != nil {
		t.Fatal(err)
	}
	objects.metadata["objects"] = "2"
	if restored, err := store.restore(ctx, target); restored || err == nil || checkpointFailure(err).Stage != "apply" {
		t.Fatal(restored, err)
	}
	var id string
	if err := target.DB.QueryRow("SELECT event_id FROM facts").Scan(&id); err != nil || id != "existing" {
		t.Fatal("failed restore replaced existing facts", id, err)
	}
	var n int
	if err := target.DB.QueryRow("SELECT count(*) FROM imported_objects").Scan(&n); err != nil || n != 0 {
		t.Fatal("failed restore committed replay index", n, err)
	}
}

func TestCheckpointRetryCancellationAndSafeLogs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, objects := checkpointFixture(t)
	var calls atomic.Int32
	store.client = &restoreTestObjects{objects, func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		calls.Add(1)
		return nil, &smithy.GenericAPIError{Code: "SlowDown", Message: "https://secret.example/?token=private"}
	}}
	service := &Service{engine: stateTestEngine(t), checkpoints: store}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	done := make(chan string, 1)
	go func() { done <- service.restoreCheckpoint(ctx, checkpointRestorePolicy{3, time.Second, time.Hour}) }()
	deadline := time.Now().Add(time.Second)
	for service.checkpointError.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case result := <-done:
		if result != "canceled" || calls.Load() != 1 {
			t.Fatal(result, calls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for retry backoff")
	}
	output := logs.String()
	for _, expected := range []string{"stage=download", "class=storage_transient", "attempt=1/3", "duration_ms=", "retry=true"} {
		if !strings.Contains(output, expected) {
			t.Fatal("missing diagnostic", expected, output)
		}
	}
	if strings.Contains(output, "secret") || strings.Contains(output, "private") {
		t.Fatal("checkpoint diagnostics leaked upstream message")
	}
	if d := checkpointFailure(checkpointStageError("apply", errors.New("private SQL"))); d.Class != "database" || d.Retryable {
		t.Fatal(d)
	}
}

func blockCheckpointUntilCanceled(t *testing.T, service *Service) <-chan struct{} {
	t.Helper()
	started := make(chan struct{})
	var once sync.Once
	service.checkpoints = newCheckpointStore(&restoreTestObjects{&fakeStateObjects{}, func(ctx context.Context, _ *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}}, Config{StateBucket: "state"})
	return started
}

func TestHistoryReadinessNeverReturnsPartialStatistics(t *testing.T) {
	store, objects := checkpointFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	store.client = &restoreTestObjects{objects, func(ctx context.Context, req *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return objects.GetObject(ctx, req)
		}
	}}
	service, err := NewService(stateTestEngine(t), Config{Upstream: "https://fixture.example", RequireInitialImport: true})
	if err != nil {
		t.Fatal(err)
	}
	service.checkpoints = store
	priceObjects := &fakeStateObjects{}
	priceConfig := Config{StateBucket: "settings"}
	if err := newStateStore(priceObjects, priceConfig).save(context.Background(), stateTestEngine(t), Price{Model: "operator-price", Input: 9, Verified: true}); err != nil {
		t.Fatal(err)
	}
	service.state = newStateStore(priceObjects, priceConfig)
	ctx, cancel := context.WithCancel(context.Background())
	done := service.startBackground(ctx)
	defer func() { cancel(); <-done }()
	<-entered
	for path, code := range map[string]int{"/healthz": 200, "/readyz": 503} {
		w := httptest.NewRecorder()
		service.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != code {
			t.Fatal(path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	service.analytics(w, httptest.NewRequest("GET", "/v1/analytics?range=all", nil))
	if w.Code != 503 || w.Header().Get("Retry-After") != "5" || !strings.Contains(w.Body.String(), "analytics_initializing") || strings.Contains(w.Body.String(), "total") {
		t.Fatal("partial history exposed", w.Code, w.Body.String())
	}
	priceDeadline := time.Now().Add(time.Second)
	for !service.stateReady.Load() && time.Now().Before(priceDeadline) {
		time.Sleep(time.Millisecond)
	}
	prices := httptest.NewRecorder()
	service.prices(prices, httptest.NewRequest("GET", "/v1/prices", nil))
	if prices.Code != 200 || !strings.Contains(prices.Body.String(), `"input":9`) || service.analyticsReady() {
		t.Fatal("price configuration waited for history", prices.Code, prices.Body.String())
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for !service.analyticsReady() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	w = httptest.NewRecorder()
	service.analytics(w, httptest.NewRequest(http.MethodGet, "/v1/analytics?range=all", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"requests":1`) {
		t.Fatal("restored history not available", w.Code, w.Body.String())
	}
}

func TestProductionTCPReadinessWaitsForCompleteHistory(t *testing.T) {
	s, err := NewService(stateTestEngine(t), Config{Upstream: "https://fixture.example", RequireInitialImport: true})
	if err != nil {
		t.Fatal(err)
	}
	s.historyLoading.Store(true)
	// Reserve an ephemeral address, then close it: only the real application
	// serving path can reopen the socket and satisfy a platform TCP probe.
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reservation.Addr().String()
	reservation.Close()
	server := &http.Server{Addr: address, Handler: s.Handler()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.listenAndServe(ctx, server) }()
	defer func() { cancel(); server.Close(); <-done }()
	for range 3 {
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Fatal("replacement accepted traffic while history was incomplete")
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.lastCollect.Store(time.Now().UnixMilli())
	s.historyLoading.Store(false)
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := client.Get("http://" + address + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatal("socket opened before analytics readiness", resp.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ready replacement never listened", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTrafficGateCancelsWithoutPublishingUnreadyReplica(t *testing.T) {
	s := &Service{cfg: Config{RequireInitialImport: true}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.listenAndServe(ctx, &http.Server{Addr: "127.0.0.1:0"}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled initialization activated listener", err)
	}
}

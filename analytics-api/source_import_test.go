package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func importFixtureClient(t *testing.T, endpoint string) *Service {
	t.Helper()
	cfg := Config{SourceID: "primary", S3Endpoint: endpoint, S3Bucket: "bucket", S3Prefix: "facts/"}
	client, err := newObjectClient(context.Background(), endpoint, "fixture", "fixture", time.Second*5)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{engine: stateTestEngine(t), cfg: cfg, factClient: client}
}
func writeList(w http.ResponseWriter, key, token string) {
	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprint(w, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	if key != "" {
		fmt.Fprintf(w, `<Contents><Key>%s</Key><LastModified>2026-09-19T00:00:00Z</LastModified><ETag>"fixture"</ETag><Size>100</Size></Contents>`, key)
	}
	if token != "" {
		fmt.Fprintf(w, `<IsTruncated>true</IsTruncated><NextContinuationToken>%s</NextContinuationToken>`, token)
	} else {
		fmt.Fprint(w, `<IsTruncated>false</IsTruncated>`)
	}
	fmt.Fprint(w, `</ListBucketResult>`)
}
func TestImportMakesDiscoveredFactsVisibleBeforeSlowFinalPage(t *testing.T) {
	secondPage := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	now := time.Now().UnixMilli()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") == "2" {
			if r.URL.Query().Get("continuation-token") == "next" {
				close(secondPage)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				writeList(w, "", "")
			} else {
				writeList(w, "facts/fresh.jsonl", "next")
			}
			return
		}
		json.NewEncoder(w).Encode(Fact{Schema: 1, EventID: "fresh", Kind: "request", AtMS: now, Model: "m", Outcome: "success"})
	}))
	defer up.Close()
	s := importFixtureClient(t, up.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.importSingleS3(ctx) }()
	defer func() { unblock(); <-done }()
	select {
	case <-secondPage:
	case <-time.After(2 * time.Second):
		t.Fatal("second page never started")
	}
	var count int
	if err := s.engine.DB.QueryRow(`SELECT count(*) FROM facts WHERE event_id='fresh'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("fresh object hidden behind full archive scan", count, err)
	}
}

func TestIndependentSourcePollsContinueWhileAnotherScanIsBlocked(t *testing.T) {
	blocked := make(chan struct{})
	var blockOnce sync.Once
	var fastLists atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/slow/") {
			blockOnce.Do(func() { close(blocked) })
			<-r.Context().Done()
			return
		}
		fastLists.Add(1)
		writeList(w, "", "")
	}))
	defer up.Close()
	slow := importFixtureClient(t, up.URL+"/slow")
	// Polling goes through real source discovery; configure the second source
	// in the test PostgreSQL rather than bypassing the production scheduler.
	store := sourceImportStore(t, up.URL+"/fast")
	defer store.Close()
	defer store.db.Exec(`DELETE FROM console_sources WHERE id IN ('primary','fast')`)
	slow.control = store
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); slow.pollSourceImports(ctx, 20*time.Millisecond) }()
	defer func() { cancel(); <-done }()
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("slow source did not start")
	}
	deadline := time.Now().Add(2 * time.Second)
	for fastLists.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatal("fast source was blocked by another source", fastLists.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	status := slow.importStatus([]string{"primary"})
	if status["caught_up"] != false || status["scanning"] != true {
		t.Fatal("scan reported caught up", status)
	}
	sourceRows := status["sources"].([]map[string]any)
	if len(sourceRows) != 1 || sourceRows[0]["source_id"] != "primary" {
		t.Fatal("import status source leak", status)
	}
}

func TestInitialImportStillRequiresEveryPageAndRemainsIdempotent(t *testing.T) {
	var downloads atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") == "2" {
			writeList(w, "facts/one.jsonl", "")
			return
		}
		downloads.Add(1)
		json.NewEncoder(w).Encode(Fact{Schema: 1, EventID: "one", Kind: "request", AtMS: time.Now().UnixMilli(), Model: "m", Outcome: "success"})
	}))
	defer up.Close()
	s := importFixtureClient(t, up.URL)
	ctx := context.Background()
	s.maybeImport(ctx)
	s.maybeImport(ctx)
	var count int
	s.engine.DB.QueryRow(`SELECT count(*) FROM facts`).Scan(&count)
	if s.lastCollect.Load() == 0 || count != 1 || downloads.Load() != 1 {
		t.Fatal("replayed object or incomplete initial import", count, downloads.Load(), s.importError.Load())
	}
}

func sourceImportStore(t *testing.T, endpoint string) *controlStore {
	t.Helper()
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	store.db.Exec(`DELETE FROM console_sources`)
	for _, id := range []string{"primary", "fast"} {
		_, err = store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: id, Name: id, Base: "https://example.test"}, Key: "fixture", Storage: sourceStorage{Endpoint: endpoint, Bucket: "bucket", Prefix: "facts/", AccessKey: "fixture", SecretKey: "fixture"}}, false)
		if err != nil {
			store.Close()
			t.Fatal(err)
		}
	}
	return store
}

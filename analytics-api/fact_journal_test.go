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
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func journalTestStore(t *testing.T) *controlStore {
	t.Helper()
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	c, err := newControlStore(dsn, strings.Repeat("j", 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestJournalCommitOrderingDedupAndGap(t *testing.T) {
	c := journalTestStore(t)
	ctx := context.Background()
	key := randomID()
	if err := c.ensureJournal(ctx, key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.db.Exec(`DELETE FROM console_fact_journal_objects WHERE source_key=$1`, key)
		c.db.Exec(`DELETE FROM console_fact_journals WHERE source_key=$1`, key)
	})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o := types.Object{Key: aws.String(fmt.Sprintf("facts/%d.jsonl", i)), ETag: aws.String("etag"), Size: aws.Int64(50)}
			if err := c.appendJournal(ctx, key, []types.Object{o, o}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	head, err := c.journalHead(ctx, key)
	if err != nil || head.Cursor != 12 {
		t.Fatal(head, err)
	}
	var n, lo, hi int
	c.db.QueryRow(`SELECT count(*),min(seq),max(seq) FROM console_fact_journal_objects WHERE source_key=$1`, key).Scan(&n, &lo, &hi)
	if n != 12 || lo != 1 || hi != 12 {
		t.Fatal(n, lo, hi)
	}
	if err = c.appendJournal(ctx, key, []types.Object{{Key: aws.String("facts/0.jsonl"), ETag: aws.String("changed"), Size: aws.Int64(50)}}); err == nil {
		t.Fatal("changed immutable object accepted")
	}
	unchanged, _ := c.journalHead(ctx, key)
	if unchanged.Cursor != 12 {
		t.Fatal("failed append advanced cursor")
	}
}

func TestJournalReplaySurvivesCheckpointAndRefusesMissingEntries(t *testing.T) {
	c := journalTestStore(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("If-Match") != "etag" {
			t.Error("unconditional object read", r.Method, r.Header.Get("If-Match"))
		}
		json.NewEncoder(w).Encode(Fact{Schema: 1, EventID: r.URL.Path, Kind: "request", AtMS: now, Model: "m", Outcome: "success"})
	}))
	defer up.Close()
	s := importFixtureClient(t, up.URL)
	s.control = c
	key := s.journalKey()
	if err := c.ensureJournal(ctx, key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.db.Exec(`DELETE FROM console_fact_journal_objects WHERE source_key=$1`, key)
		c.db.Exec(`DELETE FROM console_fact_journals WHERE source_key=$1`, key)
	})
	objects := []types.Object{{Key: aws.String("facts/late.jsonl"), ETag: aws.String("etag"), Size: aws.Int64(100)}}
	if err := c.appendJournal(ctx, key, objects); err != nil {
		t.Fatal(err)
	}
	if err := s.replayJournal(ctx, now); err != nil {
		t.Fatal(err)
	}
	state, ok := s.engine.replayState(key)
	if !ok || state.Cursor != 1 || state.AuditMS != now {
		t.Fatal(state)
	}
	storage := &fakeStateObjects{}
	cp := newCheckpointStore(storage, Config{StateBucket: "test", Timezone: "UTC"})
	if err := cp.save(ctx, s.engine); err != nil {
		t.Fatal(err)
	}
	clone := importFixtureClient(t, up.URL)
	clone.control = c
	if ok, err := cp.restore(ctx, clone.engine); !ok || err != nil {
		t.Fatal(ok, err)
	}
	state, _ = clone.engine.replayState(key)
	if state.Cursor != 1 {
		t.Fatal("checkpoint lost replay position", state)
	}
	objects[0].Key = aws.String("facts/new.jsonl")
	if err := c.appendJournal(ctx, key, objects); err != nil {
		t.Fatal(err)
	}
	if err := clone.replayJournal(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := clone.replayJournal(ctx, 0); err != nil {
		t.Fatal(err)
	}
	var n int
	clone.engine.DB.QueryRow(`SELECT count(*) FROM facts`).Scan(&n)
	if n != 2 {
		t.Fatal("replay duplicated/lost facts", n)
	}
	objects[0].Key = aws.String("facts/missing.jsonl")
	if err := c.appendJournal(ctx, key, objects); err != nil {
		t.Fatal(err)
	}
	c.db.Exec(`DELETE FROM console_fact_journal_objects WHERE source_key=$1 AND seq=3`, key)
	if err := clone.replayJournal(ctx, 0); err == nil {
		t.Fatal("gap accepted")
	}
	state, _ = clone.engine.replayState(key)
	if state.Cursor != 2 {
		t.Fatal("gap advanced cursor")
	}
}

func TestVerifiedStartupDoesNotClaimAuditCaughtUp(t *testing.T) {
	c := journalTestStore(t)
	ctx := context.Background()
	s := importFixtureClient(t, "https://fixture.invalid")
	s.control = c
	// Use a source owned by this fixture; real source discovery is exercised.
	id := "journal-" + randomID()
	s.cfg.SourceID = id
	if _, err := c.saveSource(ctx, controlSource{sourceView: sourceView{ID: id, Name: id, Base: "https://fixture.invalid"}, Key: "fixture", Storage: sourceStorage{Endpoint: s.cfg.S3Endpoint, Bucket: s.cfg.S3Bucket, Prefix: s.cfg.S3Prefix, AccessKey: "fixture", SecretKey: "fixture"}}, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.db.Exec(`DELETE FROM console_sources WHERE id=$1`, id) })
	key := s.journalKey()
	c.ensureJournal(ctx, key)
	head, _ := c.journalHead(ctx, key)
	head.AuditMS = time.Now().UnixMilli()
	s.engine.setReplayState(key, head)
	t.Cleanup(func() {
		c.db.Exec(`DELETE FROM console_fact_journal_objects WHERE source_key=$1`, key)
		c.db.Exec(`DELETE FROM console_fact_journals WHERE source_key=$1`, key)
	})
	// Other fixture sources intentionally prevent resume; check just this child
	// status independently, which must not turn a journal cursor into freshness.
	s.auditPending.Store(true)
	s.lastCollect.Store(head.AuditMS)
	parent := &Service{sourceImports: map[string]*Service{id: s}}
	status := parent.importStatus([]string{id})
	if status["caught_up"] != false || status["audit_complete"] != false {
		t.Fatal(status)
	}
}

func TestVerifiedResumeUsesAllSourceIdentitiesAndNoS3Listing(t *testing.T) {
	var calls int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "listing blocked", 503) }))
	defer up.Close()
	c := sourceImportStore(t, up.URL)
	t.Cleanup(c.Close)
	defer c.db.Exec(`DELETE FROM console_sources WHERE id IN ('primary','fast')`)
	s := importFixtureClient(t, up.URL)
	s.control = c
	ctx := context.Background()
	jobs, err := s.factSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range jobs {
		key := child.journalKey()
		if err = c.ensureJournal(ctx, key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			c.db.Exec(`DELETE FROM console_fact_journal_objects WHERE source_key=$1`, key)
			c.db.Exec(`DELETE FROM console_fact_journals WHERE source_key=$1`, key)
		})
		head, e := c.journalHead(ctx, key)
		if e != nil {
			t.Fatal(e)
		}
		head.AuditMS = time.Now().UnixMilli()
		s.engine.setReplayState(key, head)
	}
	if !s.resumeVerifiedHistory(ctx) {
		t.Fatal("valid snapshot did not resume")
	}
	if calls != 0 {
		t.Fatal("resume scanned object storage")
	}
	if s.importStatus([]string{"primary", "fast"})["caught_up"] != false {
		t.Fatal("resume invented freshness")
	}
	s.cfg.S3Prefix = "different/"
	if s.resumeVerifiedHistory(ctx) {
		t.Fatal("changed source reused another prefix cursor")
	}
}

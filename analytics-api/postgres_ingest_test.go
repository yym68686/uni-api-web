package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPostgresIngestIsAuthenticatedSourceBoundAtomicAndIdempotent(t *testing.T) {
	engine := stateTestEngine(t)
	store, err := newControlStore(os.Getenv("TEST_CONTROL_DATABASE_URL"), strings.Repeat("fixture-master", 4))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "ingest-" + randomID()[:12]
	token := "fixture-ingest-credential"
	_, err = store.saveSource(context.Background(), controlSource{sourceView: sourceView{ID: id, Name: "Fixture", Base: "https://fixture.invalid"}, Key: token}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec("DELETE FROM console_sources WHERE id=$1", id)
	service, err := NewService(engine, Config{Upstream: "https://fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	service.control = store
	handler := service.Handler()
	raw := mustJSON(Fact{Schema: 1, SourceID: "forged-source", EventID: "one", Kind: "request", AtMS: time.Now().UnixMilli(), Model: "m", Provider: "p", Outcome: "success"}) + "\n"
	var sequence int
	send := func(body, credential, dropped string) *httptest.ResponseRecorder {
		sequence++
		r := httptest.NewRequest("POST", "/v1/runtime-facts/"+id, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-Uni-Facts-Epoch", "fixture-epoch")
		r.Header.Set("X-Uni-Facts-Sequence", strconv.Itoa(sequence))
		r.Header.Set("X-Uni-Facts-Instance", "fixture-instance")
		r.Header.Set("X-Uni-Facts-Pending", "0")
		r.Header.Set("X-Uni-Facts-Dropped", dropped)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := send(raw, "wrong", "0"); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
	if w := send(raw+"{", token, "0"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	var n int
	engine.DB.QueryRow("SELECT count(*) FROM facts").Scan(&n)
	if n != 0 {
		t.Fatal("partial batch committed", n)
	}
	digest := sha256.Sum256([]byte(raw))
	for range 2 {
		w := send(raw, token, "0")
		if w.Code != 200 || !strings.Contains(w.Body.String(), hex.EncodeToString(digest[:])) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if err = engine.DB.QueryRow("SELECT count(*) FROM facts WHERE source_id=?", id).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err = engine.DB.QueryRow("SELECT count(*) FROM facts WHERE source_id='forged-source'").Scan(&n); err != nil || n != 0 {
		t.Fatal("source escaped", n, err)
	}
	if service.importStatus([]string{id})["caught_up"] != true {
		t.Fatal("confirmed batch not visible")
	}
	if err = engine.DB.QueryRow("SELECT gap_after_ms,dropped_events FROM ingest_sources WHERE source_id=?", id).Scan(new(int64), new(int64)); err != nil {
		t.Fatal(err)
	}
	if w := send("", token, "1"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	var gap, dropped int64
	if err = engine.DB.QueryRow("SELECT gap_after_ms,dropped_events FROM ingest_sources WHERE source_id=?", id).Scan(&gap, &dropped); err != nil || gap == 0 || dropped != 1 {
		t.Fatal(gap, dropped, err)
	}
	if service.historyWindowAvailable(context.Background(), id, "1h") {
		t.Fatal("window with dropped facts authorized automatic changes")
	}
}

func TestPostgresOldHeartbeatCannotEraseNewBacklog(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	if err := e.recordExporterState(ctx, "source", "epoch", "instance", 2, 5, 0, 100); err != nil {
		t.Fatal(err)
	}
	if err := e.recordExporterState(ctx, "source", "epoch", "instance", 1, 0, 0, 50); err != nil {
		t.Fatal(err)
	}
	var pending int64
	if err := e.DB.QueryRow("SELECT pending FROM ingest_producers WHERE source_id='source'").Scan(&pending); err != nil || pending != 5 {
		t.Fatal(pending, err)
	}
}

func TestPostgresExporterLossAccumulatesAcrossEpochsWithoutCountingRetries(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	for _, update := range []struct {
		epoch             string
		sequence, dropped int64
	}{{"a", 1, 2}, {"a", 2, 2}, {"b", 1, 1}, {"a", 1, 5}, {"b", 2, 3}} {
		if err := e.recordExporterState(ctx, "source", update.epoch, "instance", update.sequence, 0, update.dropped, 100); err != nil {
			t.Fatal(err)
		}
	}
	var total int64
	if err := e.DB.QueryRow("SELECT dropped_events FROM ingest_sources WHERE source_id='source'").Scan(&total); err != nil || total != 5 {
		t.Fatal(total, err)
	}
}

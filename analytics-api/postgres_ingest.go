package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var errPriceConflict = errors.New("prices changed concurrently; refresh and retry")

const maxObjectBytes = 4 << 20

func decodeFacts(reader io.Reader) ([]Fact, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, 4<<20+1))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	out := []Fact{}
	bytes := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		bytes += len(line) + 1
		if bytes > 4<<20 {
			return nil, errors.New("object_too_large")
		}
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		var f Fact
		if json.Unmarshal(line, &f) != nil {
			return nil, errors.New("invalid fact JSON")
		}
		if err := validFact(f); err != nil {
			return nil, err
		}
		out = append(out, f)
		if len(out) > 1024 {
			return nil, errors.New("too many facts")
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, errors.New("fact_line_too_large")
		}
		return nil, err
	}
	return out, nil
}

// Source-bound machine authentication is independent of browser sessions.
// The source identity is assigned here, never trusted from an event payload.
func (s *Service) ingestFacts(w http.ResponseWriter, r *http.Request) {
	if s.control == nil {
		http.Error(w, "unavailable", 503)
		return
	}
	src, err := s.control.source(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	auth := r.Header.Get("Authorization")
	given := sha256.Sum256([]byte(strings.TrimPrefix(auth, "Bearer ")))
	expected := sha256.Sum256([]byte(src.Key))
	if !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare(given[:], expected[:]) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		http.Error(w, "invalid fact batch", 413)
		return
	}
	epoch, instance := r.Header.Get("X-Uni-Facts-Epoch"), r.Header.Get("X-Uni-Facts-Instance")
	pending, pendingErr := strconv.ParseInt(r.Header.Get("X-Uni-Facts-Pending"), 10, 64)
	dropped, droppedErr := strconv.ParseInt(r.Header.Get("X-Uni-Facts-Dropped"), 10, 64)
	sequence, sequenceErr := strconv.ParseInt(r.Header.Get("X-Uni-Facts-Sequence"), 10, 64)
	if epoch == "" || len(epoch) > 256 || len(instance) > 512 || pendingErr != nil || droppedErr != nil || pending < 0 || dropped < 0 || sequenceErr != nil || sequence <= 0 {
		http.Error(w, "invalid exporter state", 400)
		return
	}
	facts, err := decodeFacts(strings.NewReader(string(raw)))
	if err != nil {
		http.Error(w, "invalid fact batch", 400)
		return
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	var latest int64
	for i := range facts {
		facts[i].SourceID = src.ID
		facts[i].EventID = src.ID + "::" + facts[i].EventID
		latest = max(latest, facts[i].AtMS)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	s.active.Add(1)
	defer s.active.Add(-1)
	operation := s.engine.observeRequestOperation(ctx, "facts_ingest")
	defer func() { operation.finish(err) }()
	operation.stage("capacity")
	if err = s.engine.enforceStorageBudget(ctx); err == nil {
		operation.stage("fact_commit")
		err = s.engine.Import(ctx, src.ID+"::"+digest, digest, facts)
	}
	if err != nil {
		s.importFailures.Add(1)
		w.Header().Set("Retry-After", "5")
		writeJSON(w, 503, map[string]string{"code": "fact_ingest_unavailable", "error": "fact ingestion unavailable"})
		return
	}
	operation.stage("exporter_state")
	if err = s.engine.recordExporterState(ctx, src.ID, epoch, instance, sequence, pending, dropped, latest); err != nil {
		s.importFailures.Add(1)
		http.Error(w, "exporter acknowledgement unavailable", 503)
		return
	}
	s.lastCollect.Store(time.Now().UnixMilli())
	writeJSON(w, 200, map[string]any{"format": "facts-ack-v1", "sha256": digest, "received": len(facts)})
}

func (s *Service) importStatus(allowed []string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	complete := len(allowed) > 0
	var handoffPending bool
	if err := s.engine.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM meta WHERE name='legacy_migration_phase' AND value<>'complete')").Scan(&handoffPending); err != nil || handoffPending {
		complete = false
	}
	var last int64
	sources := []map[string]any{}
	for _, id := range allowed {
		var received, latest, first, gap, dropped int64
		err := s.engine.DB.QueryRowContext(ctx, "SELECT last_received_ms,last_event_ms,first_received_ms,gap_after_ms,dropped_events FROM ingest_sources WHERE source_id=?", id).Scan(&received, &latest, &first, &gap, &dropped)
		healthy := err == nil && time.Since(time.UnixMilli(received)) < time.Minute
		var unfinished int64
		if err = s.engine.DB.QueryRowContext(ctx, `SELECT count(*) FROM ingest_producers WHERE source_id=? AND last_received_ms>? AND pending>0`, id, time.Now().Add(-time.Minute).UnixMilli()).Scan(&unfinished); err != nil || unfinished > 0 {
			healthy = false
		}
		complete = complete && healthy
		if last == 0 || received < last {
			last = received
		}
		sources = append(sources, map[string]any{"source_id": id, "last_scan_ms": received, "latest_fact_ms": latest, "collection_started_ms": first, "gap_after_ms": gap, "dropped_events": dropped, "scanning": false, "audit_pending": !healthy, "remaining_objects": 0})
	}
	return map[string]any{"backend": "postgresql", "last_scan_ms": last, "caught_up": complete, "audit_complete": complete, "remaining_objects": 0, "scanning": false, "sources": sources}
}

// Only windows entirely covered after the latest known collection gap may
// drive automatic changes. Old UI filters stay usable for partial statistics.
func (s *Service) historyWindowAvailable(ctx context.Context, source, window string) bool {
	start, err := rangeStart(window, time.Now(), s.engine.Location)
	if err != nil {
		return false
	}
	var boundary int64
	err = s.engine.DB.QueryRowContext(ctx, `SELECT greatest(first_received_ms,gap_after_ms,evicted_before_ms) FROM ingest_sources CROSS JOIN storage_state WHERE source_id=? AND singleton`, source).Scan(&boundary)
	if err != nil || start.UnixMilli() < boundary {
		return false
	}
	var pending bool
	return s.engine.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM meta WHERE name='legacy_migration_phase' AND value<>'complete')").Scan(&pending) == nil && !pending
}

func (e *Engine) recordExporterState(ctx context.Context, source, epoch, instance string, sequence, pending, dropped, latest int64) error {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?,8395017))", source); err != nil {
		return err
	}
	var previousDropped, previousSequence int64
	previousErr := tx.QueryRowContext(ctx, "SELECT dropped,last_sequence FROM ingest_producers WHERE source_id=? AND epoch=?", source, epoch).Scan(&previousDropped, &previousSequence)
	if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
		return previousErr
	}
	if previousErr == nil && sequence <= previousSequence {
		return nil
	}
	now := time.Now().UnixMilli()
	var gap int64
	if previousErr != nil || dropped > previousDropped {
		gap = now
	}
	droppedDelta := max(int64(0), dropped-previousDropped)
	if _, err = tx.ExecContext(ctx, `INSERT INTO ingest_sources(source_id,last_received_ms,first_received_ms,last_event_ms,gap_after_ms,dropped_events) VALUES(?,?,?,?,?,?) ON CONFLICT(source_id) DO UPDATE SET last_received_ms=excluded.last_received_ms,last_event_ms=greatest(ingest_sources.last_event_ms,excluded.last_event_ms),gap_after_ms=greatest(ingest_sources.gap_after_ms,excluded.gap_after_ms),dropped_events=ingest_sources.dropped_events+excluded.dropped_events`, source, now, now, latest, gap, droppedDelta); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ingest_producers(source_id,epoch,instance_id,first_received_ms,last_received_ms,pending,dropped,last_sequence) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(source_id,epoch) DO UPDATE SET last_received_ms=excluded.last_received_ms,pending=excluded.pending,dropped=greatest(ingest_producers.dropped,excluded.dropped),last_sequence=excluded.last_sequence`, source, epoch, instance, now, now, pending, dropped, sequence); err != nil {
		return err
	}
	return tx.Commit()
}

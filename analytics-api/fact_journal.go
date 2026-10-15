package main

// The journal records objects discovered by the existing full S3 audit. It is
// a replay accelerator, not a claim that undiscovered uploads do not exist.
// A checkpoint with a completed audit can replay this journal while a new full
// audit runs in the background. Legacy/unverified checkpoints still audit first.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var errJournalUnavailable = errors.New("fact_journal_unavailable")

type replayCheckpoint struct {
	Generation string `json:"generation"`
	Cursor     int64  `json:"cursor"`
	AuditMS    int64  `json:"audit_ms"`
}

func (s *Service) journalKey() string {
	return tokenHash(strings.Join([]string{s.cfg.SourceID, s.cfg.S3Endpoint, s.cfg.S3Bucket, strings.Trim(s.cfg.S3Prefix, "/")}, "\x00"))
}

func (c *controlStore) journalHead(ctx context.Context, key string) (replayCheckpoint, error) {
	var state replayCheckpoint
	var invalid string
	err := c.db.QueryRowContext(ctx, `SELECT generation,head,error_class FROM console_fact_journals WHERE source_key=$1`, key).Scan(&state.Generation, &state.Cursor, &invalid)
	if err != nil {
		return state, err
	}
	if invalid != "" {
		return state, errJournalUnavailable
	}
	return state, nil
}

func (c *controlStore) ensureJournal(ctx context.Context, key string) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO console_fact_journals(source_key,generation) VALUES($1,$2) ON CONFLICT DO NOTHING`, key, randomID())
	return err
}

// The per-source row lock serializes allocation AND commit. A PostgreSQL
// sequence alone would allow a later committed number to hide an earlier txn.
func (c *controlStore) appendJournal(ctx context.Context, key string, objects []types.Object) error {
	if len(objects) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var head int64
	var invalid string
	if err = tx.QueryRowContext(ctx, `SELECT head,error_class FROM console_fact_journals WHERE source_key=$1 FOR UPDATE`, key).Scan(&head, &invalid); err != nil {
		return err
	}
	if invalid != "" {
		return errJournalUnavailable
	}
	type entry struct {
		Key      string `json:"key"`
		ETag     string `json:"etag"`
		Bytes    int64  `json:"bytes"`
		Modified int64  `json:"modified"`
	}
	entries := make([]entry, 0, len(objects))
	seen := map[string]string{}
	for _, o := range objects {
		k, etag := aws.ToString(o.Key), aws.ToString(o.ETag)
		if k == "" || etag == "" || aws.ToInt64(o.Size) < 0 || aws.ToInt64(o.Size) > maxObjectBytes {
			return errJournalUnavailable
		}
		if previous, ok := seen[k]; ok {
			if previous != etag {
				return errors.New("immutable object changed")
			}
			continue
		}
		seen[k] = etag
		entries = append(entries, entry{k, etag, aws.ToInt64(o.Size), aws.ToTime(o.LastModified).UnixMilli()})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	var changed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jsonb_to_recordset($2::jsonb) AS x(key TEXT,etag TEXT) JOIN console_fact_journal_objects j ON j.source_key=$1 AND j.object_key=x.key WHERE j.etag<>x.etag)`, key, string(raw)).Scan(&changed); err != nil {
		return err
	}
	if changed {
		return errors.New("immutable object changed")
	}
	var added int64
	if err = tx.QueryRowContext(ctx, `WITH incoming AS (
 SELECT x.* FROM jsonb_to_recordset($3::jsonb) AS x(key TEXT,etag TEXT,bytes BIGINT,modified BIGINT)
 WHERE NOT EXISTS(SELECT 1 FROM console_fact_journal_objects j WHERE j.source_key=$1 AND j.object_key=x.key)
 ), inserted AS (
 INSERT INTO console_fact_journal_objects(source_key,seq,object_key,etag,bytes,modified_ms)
 SELECT $1,$2+row_number() OVER(ORDER BY key),key,etag,bytes,modified FROM incoming RETURNING seq
 ) SELECT count(*) FROM inserted`, key, head, string(raw)).Scan(&added); err != nil {
		return err
	}
	head += added
	if _, err = tx.ExecContext(ctx, `UPDATE console_fact_journals SET head=$2 WHERE source_key=$1`, key, head); err != nil {
		return err
	}
	return tx.Commit()
}

func (e *Engine) replayState(key string) (replayCheckpoint, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.replay[key]
	return v, ok
}

func (e *Engine) setReplayState(key string, state replayCheckpoint) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.replay == nil {
		e.replay = map[string]replayCheckpoint{}
	}
	e.replay[key] = state
}

// Install the cursor only AFTER the entire batch commits. A crash/checkpoint
// between commit and cursor update just replays idempotently; it cannot skip.
func (s *Service) replayJournal(ctx context.Context, auditMS int64) error {
	if s.control == nil {
		return errJournalUnavailable
	}
	key := s.journalKey()
	head, err := s.control.journalHead(ctx, key)
	if err != nil {
		return err
	}
	if s.factClient == nil {
		s.factClient, err = newS3Client(ctx, s.cfg)
		if err != nil {
			return err
		}
	}
	state, ok := s.engine.replayState(key)
	if !ok || state.Generation != head.Generation {
		if auditMS == 0 {
			return errJournalUnavailable
		}
		state = replayCheckpoint{Generation: head.Generation}
	}
	if state.Cursor > head.Cursor {
		return errJournalUnavailable
	}
	for state.Cursor < head.Cursor {
		rows, err := s.control.db.QueryContext(ctx, `SELECT seq,object_key,etag,bytes,modified_ms FROM console_fact_journal_objects WHERE source_key=$1 AND seq>$2 AND seq<=$3 ORDER BY seq LIMIT 64`, key, state.Cursor, head.Cursor)
		if err != nil {
			return err
		}
		var batch []types.Object
		next := state.Cursor
		for rows.Next() {
			var seq, size, modified int64
			var name, etag string
			if err = rows.Scan(&seq, &name, &etag, &size, &modified); err != nil {
				break
			}
			if seq != next+1 {
				err = errJournalUnavailable
				break
			}
			next = seq
			batch = append(batch, types.Object{Key: aws.String(name), ETag: aws.String(etag), Size: aws.Int64(size), LastModified: aws.Time(time.UnixMilli(modified))})
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return errJournalUnavailable
		}
		keys := make([]string, 0, len(batch))
		for _, o := range batch {
			keys = append(keys, sourceObjectKey(s.cfg.SourceID, aws.ToString(o.Key)))
		}
		known, err := s.engine.ImportedObjectPage(ctx, keys)
		if err != nil {
			return err
		}
		var pending []types.Object
		for _, o := range batch {
			etag, ok := known[sourceObjectKey(s.cfg.SourceID, aws.ToString(o.Key))]
			if ok && etag != aws.ToString(o.ETag) {
				return errors.New("immutable object changed")
			}
			if !ok {
				pending = append(pending, o)
			}
		}
		if len(pending) > 0 {
			if err = s.importListedObjects(ctx, s.factClient, pending); err != nil {
				return err
			}
		}
		state.Cursor = next
		s.engine.setReplayState(key, state)
	}
	// Reject catalog replacement/error during replay. Newer appended entries are
	// deliberately outside this fixed replay boundary and will be read next time.
	current, err := s.control.journalHead(ctx, key)
	if err != nil {
		return err
	}
	if current.Generation != head.Generation || current.Cursor < head.Cursor {
		return errJournalUnavailable
	}
	if auditMS > 0 {
		state.AuditMS = auditMS
		s.engine.setReplayState(key, state)
	}
	s.lastCollect.Store(state.AuditMS)
	return nil
}

// A bounded-age verified snapshot can serve known history, but a discovery
// journal does NOT certify freshness. importStatus exposes audit_pending until
// this replica completes its background source audit. Missing identity falls
// back to the unchanged cold-start audit path.
func (s *Service) resumeVerifiedHistory(ctx context.Context) bool {
	if s.control == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	jobs, err := s.factSources(ctx)
	if err != nil || len(jobs) == 0 {
		return false
	}
	for _, child := range jobs {
		state, ok := s.engine.replayState(child.journalKey())
		if !ok || state.AuditMS <= 0 || time.Since(time.UnixMilli(state.AuditMS)) > time.Hour || state.AuditMS > time.Now().Add(time.Minute).UnixMilli() {
			return false
		}
		if err = child.replayJournal(ctx, 0); err != nil {
			return false
		}
	}
	last := time.Now().UnixMilli()
	s.importMu.Lock()
	s.sourceImports = map[string]*Service{}
	for _, child := range jobs {
		child.auditPending.Store(true)
		if child.lastCollect.Load() < last {
			last = child.lastCollect.Load()
		}
		s.sourceImports[child.cfg.SourceID] = child
	}
	s.importMu.Unlock()
	s.lastCollect.Store(last)
	fmt.Printf("analytics startup mode=verified_journal sources=%d audit_pending=true\n", len(jobs))
	return true
}

func sameReplay(a, b map[string]replayCheckpoint) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (e *Engine) sameReplayState(states map[string]replayCheckpoint) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return sameReplay(e.replay, states)
}

func (e *Engine) replayBehind(states map[string]replayCheckpoint) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, old := range states {
		current, ok := e.replay[key]
		if !ok || current.Generation != old.Generation || current.Cursor < old.Cursor || current.AuditMS < old.AuditMS {
			return true
		}
	}
	return false
}

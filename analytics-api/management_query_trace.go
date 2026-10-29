package main

import (
	"context"
	"database/sql"
	"log"
	"time"
)

type managementTraceKey struct{}

func managementTraceContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, managementTraceKey{}, true)
}
func managementTraceEnabled(ctx context.Context) bool {
	v, _ := ctx.Value(managementTraceKey{}).(bool)
	return v
}

// A transaction pins its backend until rollback, so an external read-only
// pg_stat_activity sample can be aligned with this request's stages. Include
// the transaction timestamp already read alongside its PID: the pool can reuse
// one PID for consecutive requests. Pool queries cannot attest a later call's
// connection. These are private diagnostics, never proof or response fields.
func managementDatabaseSession(ctx context.Context, db controlQueryDB, phase string, pid int, started time.Time) {
	if tx, pinned := db.(*sql.Tx); pinned && tx != nil && pid > 0 && !started.IsZero() && managementTraceEnabled(ctx) {
		log.Printf("management_database_session phase=%s backend_pid=%d request_id=%s xact_started_ms=%d", phase, pid, consoleRequestID(ctx), started.UnixMilli())
	}
}

// Report which already-read fence disagreed, never the revision values, source
// identity or credentials. This adds no reads and does not relax the fence.
func managementGatewayFenceChanged(ctx context.Context, document string, raw map[string]any, facts managementGatewayFacts) bool {
	revisionChanged := raw["revision"] != facts.Revision
	configChanged := raw["config_revision"] != facts.ConfigRevision
	if (revisionChanged || configChanged) && managementTraceEnabled(ctx) {
		log.Printf("management_gateway_fence_rejected document=%s revision_changed=%t config_changed=%t request_id=%s", document, revisionChanged, configChanged, consoleRequestID(ctx))
	}
	return revisionChanged || configChanged
}

// Only fixed stage names, duration and the safe request ID are logged. No
// source URL, owner, filter, model, credential or query parameters escape.
func managementReadSpan(ctx context.Context, stage string) func() {
	if !managementTraceEnabled(ctx) {
		return func() {}
	}
	start := time.Now()
	return func() {
		log.Printf("management_read_stage stage=%s elapsed_ms=%d canceled=%t request_id=%s", stage, time.Since(start).Milliseconds(), ctx.Err() != nil, consoleRequestID(ctx))
	}
}

type managementReadTimeline struct {
	ctx context.Context
	end func()
}

func newManagementReadTimeline(ctx context.Context, stage string) *managementReadTimeline {
	return &managementReadTimeline{ctx: ctx, end: managementReadSpan(ctx, stage)}
}
func (t *managementReadTimeline) next(stage string) {
	t.end()
	t.end = managementReadSpan(t.ctx, stage)
}
func (t *managementReadTimeline) finish() { t.end() }

// Streaming can wait on PostgreSQL between rows. Record JSON decoding
// separately so a slow stream cannot be mistaken for expensive Go decoding.
// Counters contain no identities, payloads, filters, or SQL text.
type managementRowsTrace struct {
	ctx          context.Context
	stage        string
	start        time.Time
	ready        time.Duration
	advance      time.Duration
	decode       time.Duration
	rows, bytes  int
	deriveDecode bool
}

func newManagementRowsTrace(ctx context.Context, stage string) *managementRowsTrace {
	if !managementTraceEnabled(ctx) {
		return nil
	}
	return &managementRowsTrace{ctx: ctx, stage: stage, start: time.Now()}
}

// Large streams already time database/sql row advancement. Derive the time
// spent between advances in one batch at finish instead of adding two clock
// reads per row to a hot management request.
func newManagementRowsTraceDerivedDecode(ctx context.Context, stage string) *managementRowsTrace {
	trace := newManagementRowsTrace(ctx, stage)
	if trace != nil {
		trace.deriveDecode = true
	}
	return trace
}
func (t *managementRowsTrace) queryReady() {
	if t != nil {
		t.ready = time.Since(t.start)
	}
}

// Next includes driver work and any wait for more database/network data; it
// is NOT a disk-I/O measurement. Keep it separate from application JSON decode
// and preserve the exact row/error/cancellation behavior of database/sql.
func (t *managementRowsTrace) next(rows *sql.Rows) bool {
	if t == nil {
		return rows.Next()
	}
	start := time.Now()
	more := rows.Next()
	t.advance += time.Since(start)
	return more
}

func (t *managementRowsTrace) decodeStart() time.Time {
	if t == nil {
		return time.Time{}
	}
	return time.Now()
}
func (t *managementRowsTrace) decoded(start time.Time, bytes int) {
	if t != nil {
		t.rows++
		t.bytes += bytes
		t.decode += time.Since(start)
	}
}
func (t *managementRowsTrace) counted(bytes int) {
	if t != nil {
		t.rows++
		t.bytes += bytes
	}
}
func (t *managementRowsTrace) finish() {
	if t != nil {
		elapsed := time.Since(t.start)
		decode := t.decode
		if t.deriveDecode {
			decode = elapsed - t.ready - t.advance
			if decode < 0 {
				decode = 0
			}
		}
		log.Printf("management_rows_read stage=%s query_ready_ms=%d decode_ms=%d elapsed_ms=%d rows=%d bytes=%d advance_ms=%d canceled=%t request_id=%s", t.stage, t.ready.Milliseconds(), decode.Milliseconds(), elapsed.Milliseconds(), t.rows, t.bytes, t.advance.Milliseconds(), t.ctx.Err() != nil, consoleRequestID(t.ctx))
	}
}

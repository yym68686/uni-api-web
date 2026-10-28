package main

import (
	"context"
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
	ctx         context.Context
	stage       string
	start       time.Time
	ready       time.Duration
	decode      time.Duration
	rows, bytes int
}

func newManagementRowsTrace(ctx context.Context, stage string) *managementRowsTrace {
	if !managementTraceEnabled(ctx) {
		return nil
	}
	return &managementRowsTrace{ctx: ctx, stage: stage, start: time.Now()}
}
func (t *managementRowsTrace) queryReady() {
	if t != nil {
		t.ready = time.Since(t.start)
	}
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
func (t *managementRowsTrace) finish() {
	if t != nil {
		log.Printf("management_rows_read stage=%s query_ready_ms=%d decode_ms=%d elapsed_ms=%d rows=%d bytes=%d canceled=%t request_id=%s", t.stage, t.ready.Milliseconds(), t.decode.Milliseconds(), time.Since(t.start).Milliseconds(), t.rows, t.bytes, t.ctx.Err() != nil, consoleRequestID(t.ctx))
	}
}

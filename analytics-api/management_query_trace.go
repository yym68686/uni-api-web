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

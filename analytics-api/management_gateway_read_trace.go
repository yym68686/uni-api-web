package main

import (
	"context"
	"log"
	"net/http"
	"time"
)

// These four read-only documents are the complete atomic snapshot and its
// independent credential/revision fences. Never log a caller-supplied path,
// URL, source identity, revision, credential, body, or transport error.
func managementGatewayReadDocument(method, path string) string {
	if method != http.MethodGet {
		return ""
	}
	switch path {
	case "/v1/channel-settings/management-snapshot":
		return "snapshot"
	case "/v1/channel-settings/management-revision":
		return "snapshot_revision"
	case "/v1/channel-controls":
		return "controls"
	case "/v1/channel-controls/revision":
		return "controls_revision"
	}
	return ""
}

type managementGatewayReadTrace struct {
	ctx                     context.Context
	document, phase         string
	start, previous         time.Time
	transport, body, decode time.Duration
	code, bytes             int
}

func newManagementGatewayReadTrace(ctx context.Context, method, path string) *managementGatewayReadTrace {
	if !managementTraceEnabled(ctx) {
		return nil
	}
	document := managementGatewayReadDocument(method, path)
	if document == "" {
		return nil
	}
	return &managementGatewayReadTrace{ctx: ctx, document: document, phase: "transport", start: time.Now()}
}

// Do includes connection/pool/round-trip/header work. It is not an upstream
// CPU measurement; body reading can likewise include network/server waits.
func (t *managementGatewayReadTrace) headers(code int) {
	if t != nil {
		now := time.Now()
		t.transport, t.previous, t.code = now.Sub(t.start), now, code
		if code != 0 {
			t.phase = "status"
		}
	}
}

func (t *managementGatewayReadTrace) read(bytes int) {
	if t != nil {
		now := time.Now()
		t.body, t.previous, t.bytes, t.phase = now.Sub(t.previous), now, bytes, "body"
	}
}

func (t *managementGatewayReadTrace) decoded(ok bool) {
	if t != nil {
		t.decode, t.phase = time.Since(t.previous), "decode"
		if ok {
			t.phase = "complete"
		}
	}
}

func (t *managementGatewayReadTrace) finish() {
	if t != nil {
		id := consoleRequestID(t.ctx)
		if !consoleRequestIDPattern.MatchString(id) {
			id = "unavailable"
		}
		log.Printf("management_gateway_read document=%s phase=%s status=%d transport_ms=%d body_ms=%d decode_ms=%d elapsed_ms=%d bytes=%d canceled=%t request_id=%s", t.document, t.phase, t.code, t.transport.Milliseconds(), t.body.Milliseconds(), t.decode.Milliseconds(), time.Since(t.start).Milliseconds(), t.bytes, t.ctx.Err() != nil, id)
	}
}

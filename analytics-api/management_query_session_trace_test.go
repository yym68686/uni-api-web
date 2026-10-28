package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
)

func TestManagementDatabaseSessionTraceRequiresPinnedTracedConnection(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	ctx := context.WithValue(managementTraceContext(context.Background()), consoleRequestIDKey{}, "session-trace-123")
	// A diagnostic must never imply that a pool's next call uses the same PID.
	managementDatabaseSession(ctx, (*sql.DB)(nil), "view", 123)
	managementDatabaseSession(ctx, (*sql.Tx)(nil), "view", 123)
	managementDatabaseSession(context.Background(), &sql.Tx{}, "view", 123)
	managementDatabaseSession(ctx, &sql.Tx{}, "view", 0)
	if output.Len() != 0 {
		t.Fatal("unreliable connection attribution logged")
	}
	managementDatabaseSession(ctx, &sql.Tx{}, "view", 123)
	if !strings.Contains(output.String(), "management_database_session phase=view backend_pid=123 request_id=session-trace-123") {
		t.Fatal("missing fixed connection attribution")
	}
}

func TestManagementSessionTraceLeavesProofAndViewUnchanged(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err = tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	plainProof, plainAt, err := s.managementReadProof(ctx, tx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	plainView, err := s.readManagementDatabaseFrom(ctx, tx, "owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	traced := context.WithValue(managementTraceContext(ctx), consoleRequestIDKey{}, "pinned-read-123")
	proof, at, err := s.managementReadProof(traced, tx, "owner")
	if err != nil || !reflect.DeepEqual(plainProof, proof) || !plainAt.Equal(at) {
		t.Fatal("proof/time changed", err)
	}
	view, err := s.readManagementDatabaseFrom(traced, tx, "owner", nil)
	if err != nil || mustJSON(view) != mustJSON(plainView) {
		t.Fatal("complete view changed", err)
	}
	for _, phase := range []string{"proof", "view"} {
		if !strings.Contains(output.String(), fmt.Sprintf("management_database_session phase=%s backend_pid=%d request_id=pinned-read-123", phase, pid)) {
			t.Fatal("diagnostic did not identify pinned backend", phase)
		}
	}
	for _, raw := range []string{mustJSON(proof), mustJSON(view)} {
		if strings.Contains(raw, "backend_pid") || strings.Contains(raw, "pinned-read-123") {
			t.Fatal("private session metadata entered public values")
		}
	}
	for _, private := range []string{"https://a.test", "fixture", "owner", "a-1-gpt", "SELECT", "console_sub"} {
		if strings.Contains(output.String(), private) {
			t.Fatal("diagnostic emitted private data", private)
		}
	}
}

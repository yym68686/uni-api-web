package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
	"time"
)

func TestControlSchemaRetriesRolledBackDeadlocks(t *testing.T) {
	calls := 0
	err := retryControlSchema(context.Background(), func() error {
		calls++
		if calls < 3 {
			return &pgconn.PgError{Code: "40P01"}
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatal(err, calls)
	}
}
func TestControlSchemaDoesNotRetryPermanentErrors(t *testing.T) {
	for _, code := range []string{"42501", "42601", "23505"} {
		calls := 0
		failure := &pgconn.PgError{Code: code}
		err := retryControlSchema(context.Background(), func() error { calls++; return failure })
		if err != failure || calls != 1 {
			t.Fatal(err, calls)
		}
	}
}
func TestControlSchemaRetryIsBoundedAndCancellable(t *testing.T) {
	calls := 0
	failure := &pgconn.PgError{Code: "40P01"}
	err := retryControlSchema(context.Background(), func() error { calls++; return failure })
	if err != failure || calls != 5 {
		t.Fatal(err, calls)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	calls = 0
	err = retryControlSchema(ctx, func() error { calls++; return failure })
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatal(err, calls)
	}
}

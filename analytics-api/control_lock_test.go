package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestControlLocksUseDedicatedPoolAndSerializeAcrossStores(t *testing.T) {
	dsn := os.Getenv("TEST_CONTROL_DATABASE_URL")
	if dsn == "" {
		t.Skip("local PostgreSQL required")
	}
	store, err := newControlStore(dsn, strings.Repeat("l", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	replica, err := newControlStore(dsn, strings.Repeat("l", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer replica.Close()

	store.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, second := randomID(), randomID()
	for _, source := range []string{first, second} {
		unlock, err := store.lockControls(ctx, source)
		if err != nil {
			t.Fatal("distinct sources could not lock concurrently", err)
		}
		defer unlock()
	}
	query, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	var one int
	if err := store.db.QueryRowContext(query, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatal("source locks exhausted the SQL pool used by their owners", err)
	}
	if unlock, err := replica.lockControls(ctx, first); !errors.Is(err, errControlsBusy) {
		if unlock != nil {
			unlock()
		}
		t.Fatal("a second store bypassed the same-source lock", err)
	}

	store.controlLocks.SetMaxOpenConns(2)
	if unlock, err := store.tryControlMaintenance(ctx, randomID()); !errors.Is(err, context.DeadlineExceeded) {
		if unlock != nil {
			unlock()
		}
		t.Fatal("maintenance waited for a saturated lock pool without its own deadline", err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatal("maintenance exhausted the entire parent deadline", err)
	}
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func assertBackendRetired(t *testing.T, observer *sql.Conn, pid int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		var exists bool
		if err := observer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			return
		}
		if !waitStartup(ctx, 10*time.Millisecond) {
			t.Fatal("finished analytical backend still retains its process and private memory", pid)
		}
	}
}

func TestAnalyticalSnapshotRetiresBackendWithoutInterruptingOtherTransactions(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	if err := e.Import(ctx, "before-reading", "one", []Fact{attributedFact("before-reading", "caller", at)}); err != nil {
		t.Fatal(err)
	}
	observer, err := e.DB.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	var observerPID int
	if err = observer.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&observerPID); err != nil {
		t.Fatal(err)
	}
	tx, finish, err := e.DB.analyticalSnapshot(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	var pid int
	if err = tx.QueryRow("SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	// Exercise the billing lifecycle: temp state and a repeatable snapshot must
	// survive concurrent ingestion, then disappear with only this session.
	if _, err = tx.Exec("CREATE TEMP TABLE retained_buffers AS SELECT n FROM generate_series(1,10000) n"); err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err = tx.QueryRow("SELECT count(*) FROM facts").Scan(&before); err != nil {
		t.Fatal(err)
	}
	f := attributedFact("while-reading", "caller", at)
	if err = e.Import(ctx, "while-reading", "one", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow("SELECT count(*) FROM facts").Scan(&after); err != nil || after != before {
		t.Fatal("analytical snapshot lost its transaction isolation", before, after, err)
	}
	finish()
	finish() // Cancellation and handler cleanup can both finish the same read.
	assertBackendRetired(t, observer, pid)
	var currentPID, count int
	if err = observer.QueryRowContext(ctx, "SELECT pg_backend_pid(),(SELECT count(*) FROM "+e.historyPath+".facts)").Scan(&currentPID, &count); err != nil || currentPID != observerPID || count != 2 {
		t.Fatal("analytical cleanup interrupted another connection or lost ingestion", currentPID, count, err)
	}
}

func TestAnalyticalSnapshotCancellationRetiresBackendAndLaterReadsRecover(t *testing.T) {
	e := stateTestEngine(t)
	observer, err := e.DB.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	tx, finish, err := e.DB.analyticalSnapshot(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	var pid int
	if err = tx.QueryRow("SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	cancel()
	_, err = tx.ExecContext(ctx, "SELECT 1")
	if !errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal("cancellation was replaced by a retryable connection sentinel", err)
	}
	finish()
	assertBackendRetired(t, observer, pid)
	view, closeSnapshot, err := e.readSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeSnapshot()
	if err = view.DB.QueryRow("SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	closeSnapshot()
	assertBackendRetired(t, observer, pid)
}

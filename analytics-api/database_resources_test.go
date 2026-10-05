package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSpendGateHonorsCancellationAndReleases(t *testing.T) {
	e := stateTestEngine(t)
	release, err := e.acquireAnalytical(context.Background(), "channel_spend")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = e.acquireAnalytical(ctx, "channel_spend"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if e.analyticalWaiting.Load() != 0 {
		t.Fatal("queued request leaked")
	}
	release()
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err = e.acquireAnalytical(ctx, "channel_spend"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release, err = e.acquireAnalytical(context.Background(), "channel_spend")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestDatabaseResourceSettingsAndDiagnostics(t *testing.T) {
	dir := t.TempDir()
	e, err := OpenEngine(filepath.Join(dir, "history.duckdb"), Config{Timezone: "UTC", DataDir: dir, DatabaseMemoryLimitMB: 128, DatabaseThreads: 1, DatabaseTempDir: filepath.Join(dir, "spill's"), DatabaseTempLimitMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	var threads int
	var temp string
	if err = e.DB.QueryRow("SELECT current_setting('threads'),current_setting('temp_directory')").Scan(&threads, &temp); err != nil {
		t.Fatal(err)
	}
	if threads != 1 || temp != filepath.Join(dir, "spill's") {
		t.Fatal(threads, temp)
	}
	o := e.observeOperation("import_batch")
	o.stage("day_rollups")
	e.operationsMu.Lock()
	phase := e.operations[o]
	e.operationsMu.Unlock()
	if phase != "import_batch/day_rollups" {
		t.Fatal(phase)
	}
	e.logMemory("test")
	o.finish(nil)
	if len(e.operations) != 0 {
		t.Fatal("operation leaked")
	}
	stop := e.startMemoryDiagnostics(context.Background(), time.Hour)
	stop()
	stop()
}

// The memory gate covers expensive read plans, not the collector. A queued page
// must cancel without blocking new facts, and later reads must still succeed.
func TestAnalyticalQueueDoesNotBlockImportOrLeakAfterCancellation(t *testing.T) {
	e := stateTestEngine(t)
	release, err := e.acquireAnalytical(context.Background(), "channel_spend")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := e.Query(ctx, QueryFilter{Range: "all"})
		done <- err
	}()
	f := attributedFact("while-queued", "key", time.Now())
	if err = e.Import(context.Background(), "while-queued", "one", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	release()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = e.Query(ctx, QueryFilter{Range: "all"}); err != nil {
		t.Fatal(err)
	}
	if e.analyticalWaiting.Load() != 0 {
		t.Fatal("cancelled query left a waiter")
	}
}

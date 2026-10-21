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

func TestCriticalAnalyticsPreemptsSpend(t *testing.T) {
	e := stateTestEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	spendCtx, unregister := e.registerSpend(ctx)
	defer unregister()
	queuedSpendCtx, unregisterQueued := e.registerSpend(ctx)
	defer unregisterQueued()
	if spendCtx.Err() != nil {
		t.Fatal("billing canceled its peer")
	}
	release, err := e.acquireAnalytical(spendCtx, "channel_spend")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	type acquisition struct {
		release func()
		err     error
	}
	criticalDone := make(chan acquisition, 1)
	go func() {
		r, err := e.acquireAnalytical(ctx, "analytics_query")
		criticalDone <- acquisition{r, err}
	}()
	for _, c := range []context.Context{spendCtx, queuedSpendCtx} {
		select {
		case <-c.Done():
		case <-ctx.Done():
			t.Fatal("critical analytics query did not preempt spend")
		}
		if !errors.Is(context.Cause(c), errAnalyticsPriority) {
			t.Fatal(context.Cause(c))
		}
	}
	lateCtx, unregisterLate := e.registerSpend(ctx)
	defer unregisterLate()
	if !errors.Is(context.Cause(lateCtx), errAnalyticsPriority) {
		t.Fatal("late billing bypassed queued reader")
	}
	select {
	case <-criticalDone:
		t.Fatal("reader bypassed billing cleanup")
	default:
	}
	release()
	var r acquisition
	select {
	case r = <-criticalDone:
		if r.err != nil {
			t.Fatal(r.err)
		}
	case <-ctx.Done():
		t.Fatal("critical analytics query did not acquire the released slot")
	}
	defer r.release()
	duringCtx, unregisterDuring := e.registerSpend(ctx)
	defer unregisterDuring()
	if !errors.Is(context.Cause(duringCtx), errAnalyticsPriority) {
		t.Fatal("billing bypassed active reader")
	}
	r.release()
	nextCtx, unregisterNext := e.registerSpend(ctx)
	defer unregisterNext()
	if nextCtx.Err() != nil {
		t.Fatal("priority leaked after reader completed")
	}
	nextRelease, err := e.acquireAnalytical(nextCtx, "channel_spend")
	if err != nil {
		t.Fatal(err)
	}
	nextRelease()
	if e.analyticalWaiting.Load() != 0 {
		t.Fatal("waiter leaked")
	}
}

func TestCanceledAnalyticsReaderDoesNotPoisonBilling(t *testing.T) {
	e := stateTestEngine(t)
	release, err := e.acquireAnalytical(context.Background(), "channel_spend")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = e.acquireAnalytical(ctx, "analytics_query"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	spendCtx, unregister := e.registerSpend(context.Background())
	defer unregister()
	if spendCtx.Err() != nil {
		t.Fatal("canceled queued reader retained priority")
	}
	if _, err = e.acquireAnalytical(ctx, "analytics_query"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if spendCtx.Err() != nil {
		t.Fatal("already expired reader canceled billing")
	}
}

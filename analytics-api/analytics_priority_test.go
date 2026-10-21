package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Exercise the production path with DuckDB and an isolated PostgreSQL ledger:
// billing is blocked on receipt lookup, an interactive read preempts it, then
// billing recovers with the same exact amount after all transactions unwind.
func TestAnalyticsPreemptsBlockedReceiptLookupAndBillingRecovers(t *testing.T) {
	s, account, owner := attributionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	now := time.Now().Truncate(time.Second)
	fact := attributedFact("priority-fixture", "caller", now.Add(-time.Minute))
	storeAttributedLog(t, s, account, fact.EventID, "0.125", 1)
	if err := s.engine.Import(ctx, "priority-facts", "v1", []Fact{fact}); err != nil {
		t.Fatal(err)
	}
	lock, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err = lock.ExecContext(ctx, "LOCK TABLE console_sub_spend_logs IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	f := QueryFilter{SourceID: "source", KeyID: "caller"}
	from, to := now.Add(-time.Hour).Unix(), now.Unix()
	done := make(chan error, 1)
	go func() {
		_, err := s.attributedChannelSpend(ctx, owner, f, from, to)
		done <- err
	}()
	// Confirm PostgreSQL is actually waiting for our lock, not just that a
	// goroutine was scheduled or an operation stage was set.
	for {
		var waiting bool
		err = s.control.db.QueryRowContext(ctx, `SELECT EXISTS (
 SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
 AND wait_event_type='Lock' AND query LIKE 'SELECT l.account_id,l.key_id,l.log_id,l.request_id,l.actual_cost%'
)`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err = <-done:
			t.Fatalf("billing finished before blocked lookup: %v", err)
		case <-ctx.Done():
			t.Fatal("billing never reached receipt lookup")
		case <-time.After(5 * time.Millisecond):
		}
	}
	readCtx, stopRead := context.WithTimeout(ctx, 2*time.Second)
	defer stopRead()
	if _, err = s.engine.Query(readCtx, QueryFilter{Range: "all"}); err != nil {
		t.Fatalf("interactive read blocked behind optional billing: %v", err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, errAnalyticsPriority) {
			t.Fatalf("billing error = %v, want explicit priority deferral", err)
		}
	case <-ctx.Done():
		t.Fatal("preempted billing did not finish cleanup")
	}
	if err = lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	rows, err := s.attributedChannelSpend(ctx, owner, f, from, to)
	if err != nil || len(rows) != 1 || rows[0].Amount == nil || *rows[0].Amount != .125 || rows[0].Matched != 1 {
		t.Fatalf("billing did not recover the exact receipt: rows=%+v err=%v", rows, err)
	}
	s.engine.spendMu.Lock()
	registered, critical := len(s.engine.spendCancels), s.engine.criticalReads
	s.engine.spendMu.Unlock()
	if registered != 0 || critical != 0 || s.engine.analyticalWaiting.Load() != 0 {
		t.Fatal("priority registration or waiter leaked")
	}
}

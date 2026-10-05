package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type failingFactReader struct{ err error }

func (r failingFactReader) Read([]byte) (int, error) { return 0, r.err }

func TestStreamingFactDecodeRejectsIncompleteOrOversizedObjects(t *testing.T) {
	f := attributedFact("stream", "key", time.Now())
	raw, _ := json.Marshal(f)
	input := string(raw) + "\n"
	got, err := decodeFacts(strings.NewReader(input))
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	if _, err = decodeFacts(io.MultiReader(strings.NewReader(input), strings.NewReader("{"))); err == nil {
		t.Fatal("accepted malformed tail")
	}
	broken := errors.New("truncated response")
	if _, err = decodeFacts(io.MultiReader(strings.NewReader(input), failingFactReader{broken})); !errors.Is(err, broken) {
		t.Fatal(err)
	}
	// Blank lines keep the per-line cap below 1 MiB; the total object cap is
	// independent of scanner tokens and must not accept an EOF at the limit.
	if _, err = decodeFacts(strings.NewReader(strings.Repeat(" \n", maxObjectBytes/2+1))); err == nil || err.Error() != "object_too_large" {
		t.Fatal(err)
	}
	if _, err = decodeFacts(strings.NewReader(strings.Repeat(" ", 1<<20+1))); err == nil || err.Error() != "fact_line_too_large" {
		t.Fatal(err)
	}
}

func TestTraceAndBillingImportsDoNotRebuildMetricRollups(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	b := attributedFact("bill-only", "key", time.Now())
	a := b
	a.Kind = "attempt"
	a.EventID = "initial-attempt"
	a.Outcome = "success"
	if err := e.Import(ctx, "first", "one", []Fact{a}); err != nil {
		t.Fatal(err)
	}
	// Rename the table: a trace/billing-only batch must never read/write rollups.
	// Bring it back before reading metrics; the facts and import marker still commit.
	if _, err := e.DB.Exec("ALTER TABLE rollups RENAME TO preserved_rollups"); err != nil {
		t.Fatal(err)
	}
	tr := b
	tr.Kind = "trace"
	tr.EventID = "trace-only"
	tr.Stage = "request_received"
	if err := e.Import(ctx, "non-metric", "two", []Fact{b, tr}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("ALTER TABLE preserved_rollups RENAME TO rollups"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.DB.QueryRow("SELECT sum(n) FROM rollups WHERE level='minute'").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if imported, err := e.Imported(ctx, "non-metric", "two"); err != nil || !imported {
		t.Fatal(imported, err)
	}
}

func TestMixedMetricAndTraceImportRemainsAtomicAndIdempotent(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	b := attributedFact("mixed-bill", "key", time.Now())
	a := b
	a.Kind, a.EventID, a.Outcome = "attempt", "mixed-attempt", "success"
	tr := b
	tr.Kind, tr.EventID, tr.Stage = "trace", "mixed-trace", "request_received"
	facts := []Fact{b, tr, a}
	if err := e.Import(ctx, "mixed", "one", facts); err != nil {
		t.Fatal(err)
	}
	before := e.Revision.Load()
	if err := e.Import(ctx, "mixed", "one", facts); err != nil || e.Revision.Load() != before {
		t.Fatal("replay changed revision", err)
	}
	var count int
	if err := e.DB.QueryRow("SELECT sum(n) FROM rollups WHERE level='minute'").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	a.EventID = "cancelled-attempt"
	if err := e.Import(cancelled, "cancelled", "one", []Fact{a}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.Revision.Load() != before {
		t.Fatal("cancelled import changed revision")
	}
	if err := e.DB.QueryRow("SELECT count(*) FROM facts WHERE event_id='cancelled-attempt'").Scan(&count); err != nil || count != 0 {
		t.Fatal("cancelled fact committed", count, err)
	}
	if imported, err := e.Imported(ctx, "cancelled", "one"); err != nil || imported {
		t.Fatal("cancelled object committed", imported, err)
	}
}

package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The original unbounded alignment is the semantic oracle. This compares every
// projected value in both directions, including duplicates, not a checksum.
func TestBoundedBillingAlignmentMatchesFullHistory(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	to := from + 3600
	var facts []Fact
	for i := 0; i < 120; i++ {
		b := attributedFact(fmt.Sprintf("bill-%d", i), fmt.Sprintf("key-%d", i%3), time.Unix(from+int64(i%5-2)*1800, 0))
		b.Model = fmt.Sprintf("model-%d", i%2)
		b.UpstreamModel = fmt.Sprintf("upstream-%d", i%4)
		if i%7 == 0 {
			b.SourceID = "other"
		}
		if i%11 == 0 {
			b.Stream = false
			b.Endpoint = "/v1/chat/completions"
		}
		facts = append(facts, b)
		if i%4 == 0 {
			continue
		} // no completion / cancelled hedge
		a := b
		a.EventID = "attempt-" + b.EventID
		a.Kind = "attempt"
		a.AtMS = time.Unix(from+int64(i%7-3)*900, 0).UnixMilli()
		a.Outcome = "success"
		facts = append(facts, a)
		if i%3 == 0 { // reused caller identity, with a completion outside this window
			a.EventID += "-reused"
			a.AtMS = time.Unix(to+60, 0).UnixMilli()
			facts = append(facts, a)
		}
	}
	if err := e.Import(ctx, "fixtures", "one", facts); err != nil {
		t.Fatal(err)
	}
	// SQL NULL identities in older checkpoints must retain SQL join semantics.
	if _, err := e.DB.Exec("UPDATE facts SET request_id=NULL WHERE event_id IN ('bill-1','attempt-bill-1')"); err != nil {
		t.Fatal(err)
	}
	cases := []QueryFilter{
		{}, {SourceIDs: []string{"source"}}, {SourceIDs: []string{}, SourceID: "other"},
		{KeyID: "key-1"}, {Model: "model-0"}, {Provider: "shared", UpstreamModel: "upstream-2"},
		{Endpoint: "/v1/responses", Stream: "true"}, {Stream: "false"},
		{SourceIDs: []string{"source", "other"}, KeyID: "key-2", Model: "model-1"},
		{SourceIDs: []string{"absent"}},
	}
	for i, f := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			tx, err := e.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			where, args := billingWhere(f, from, to)
			if _, err = tx.ExecContext(ctx, "CREATE TEMP TABLE expected AS "+billingAlignedSQL+"SELECT "+billingProjection+",completions FROM aligned_billing WHERE "+where, args...); err != nil {
				t.Fatal(err)
			}
			sql, scope := scopedBillingAlignment(f, from, to)
			if _, err = tx.ExecContext(ctx, "CREATE TEMP TABLE actual AS "+sql+"SELECT "+billingProjection+",completions FROM aligned_billing WHERE "+where, append(scope, args...)...); err != nil {
				t.Fatal(err)
			}
			var diff int
			if err = tx.QueryRow("SELECT count(*) FROM ((SELECT * FROM expected EXCEPT ALL SELECT * FROM actual) UNION ALL (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected))").Scan(&diff); err != nil {
				t.Fatal(err)
			}
			if diff != 0 {
				t.Fatalf("%d different records for %+v", diff, f)
			}
		})
	}
}

func TestBillingClaimsRemainGlobalAcrossPagesAndKeys(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	now := time.Now().Add(-time.Minute)
	a := attributedFact("a", "selected", now)
	a.BillingRequestIDs = []string{"client:shared", "client:shared", "client:unique"}
	b := a
	b.EventID = "b"
	b.SourceID = "foreign"
	b.KeyID = "other"
	b.AtMS = now.Add(-48 * time.Hour).UnixMilli()
	b.BillingRequestIDs = []string{"client:shared"}
	c := b
	c.EventID = "c"
	c.UpstreamKeyHash = "different-credential"
	if err := e.Import(ctx, "claims", "one", []Fact{a, b, c}); err != nil {
		t.Fatal(err)
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("CREATE TEMP TABLE console_spend_facts AS SELECT " + billingProjection + " FROM facts WHERE event_id='a'"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(billingClaimsSQL); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query("SELECT rid,n FROM console_spend_claims ORDER BY rid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err = rows.Scan(&id, &n); err != nil {
			t.Fatal(err)
		}
		got[id] = n
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["client:shared"] != 2 || got["client:unique"] != 1 {
		t.Fatal(got)
	}
}

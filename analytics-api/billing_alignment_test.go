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
	// Missing companions, duplicate completions across the window boundary, and
	// headers emitted before the window must retain their old attribution.
	for _, offset := range []int64{-1, 0, 1, 3599, 3600} {
		a := attributedFact(fmt.Sprintf("legacy-%d", offset), "key-1", time.Unix(from+offset, 0))
		a.Kind = "attempt"
		facts = append(facts, a)
		if offset == 1 {
			b := a
			b.EventID = "early-header"
			b.Kind = "billing"
			b.AtMS = time.Unix(from-7200, 0).UnixMilli()
			facts = append(facts, b)
		}
	}
	if err := e.Import(ctx, "fixtures", "one", facts); err != nil {
		t.Fatal(err)
	}
	// SQL NULL identities in older checkpoints must retain SQL join semantics.
	for i, column := range []string{"request_id", "instance_id", "attempt_id", "provider", "key_id", "model", "upstream_model", "endpoint", "stream"} {
		if _, err := e.DB.Exec("UPDATE facts SET "+column+"=NULL WHERE event_id IN (?,?)", fmt.Sprintf("bill-%d", i+1), fmt.Sprintf("attempt-bill-%d", i+1)); err != nil {
			t.Fatal(err)
		}
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
			sql, parameters := scopedBillingSnapshot(f, from, to)
			if _, err = tx.ExecContext(ctx, "CREATE TEMP TABLE combined AS "+sql, parameters...); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.ExecContext(ctx, "CREATE TEMP TABLE actual AS SELECT "+billingProjection+",completions FROM combined WHERE event_id IS NOT NULL"); err != nil {
				t.Fatal(err)
			}
			var diff int
			if err = tx.QueryRow("SELECT count(*) FROM ((SELECT * FROM expected EXCEPT ALL SELECT * FROM actual) UNION ALL (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)) AS differences").Scan(&diff); err != nil {
				t.Fatal(err)
			}
			if diff != 0 {
				t.Fatalf("%d different records for %+v", diff, f)
			}
			legacy := `WITH selected AS (SELECT * FROM facts WHERE kind='attempt' AND ` + where + `) SELECT a.source_id,a.provider,a.model,a.upstream_model,count(*) AS n FROM selected a WHERE NOT EXISTS(SELECT 1 FROM facts b WHERE b.kind='billing' AND b.source_id=a.source_id AND b.instance_id=a.instance_id AND b.request_id=a.request_id AND b.attempt_id=a.attempt_id AND b.provider=a.provider AND b.key_id=a.key_id AND b.model=a.model AND b.upstream_model=a.upstream_model AND b.endpoint=a.endpoint AND b.stream=a.stream) GROUP BY a.source_id,a.provider,a.model,a.upstream_model`
			if _, err = tx.ExecContext(ctx, "CREATE TEMP TABLE expected_legacy AS "+legacy, args...); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.ExecContext(ctx, `CREATE TEMP TABLE actual_legacy AS SELECT source_id,provider,model,upstream_model,sum(legacy_count)::bigint AS n FROM combined WHERE event_id IS NULL GROUP BY source_id,provider,model,upstream_model`); err != nil {
				t.Fatal(err)
			}
			if err = tx.QueryRow("SELECT count(*) FROM ((SELECT * FROM expected_legacy EXCEPT ALL SELECT * FROM actual_legacy) UNION ALL (SELECT * FROM actual_legacy EXCEPT ALL SELECT * FROM expected_legacy)) AS differences").Scan(&diff); err != nil || diff != 0 {
				t.Fatalf("missing-correlation counts differ for %+v: rows=%d err=%v", f, diff, err)
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
	facts := []Fact{a, b, c}
	for i := 0; i < 1024; i++ {
		facts = append(facts, attributedFact(fmt.Sprintf("padding-%04d", i), "selected", now))
	}
	if err := e.Import(ctx, "claims", "one", facts); err != nil {
		t.Fatal(err)
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("CREATE TEMP TABLE console_spend_facts AS SELECT " + billingProjection + " FROM facts WHERE event_id='a' OR event_id LIKE 'padding-%'"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(billingClaimsSQL); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("ALTER TABLE console_spend_claims RENAME TO expected_claims"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(billingClaimsSeekSQL); err != nil {
		t.Fatal(err)
	}
	var different bool
	if err = tx.QueryRow(`SELECT EXISTS((SELECT * FROM expected_claims EXCEPT ALL SELECT * FROM console_spend_claims) UNION ALL (SELECT * FROM console_spend_claims EXCEPT ALL SELECT * FROM expected_claims))`).Scan(&different); err != nil || different {
		t.Fatal("indexed claims differ from full global history", different, err)
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
	if len(got) != 1026 || got["client:shared"] != 2 || got["client:unique"] != 1 {
		t.Fatal(got)
	}
}

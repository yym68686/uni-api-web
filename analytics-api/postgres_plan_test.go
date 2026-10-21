package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresBillingPlan(t *testing.T) {
	schema := os.Getenv("PG_PLAN_SCHEMA")
	if schema == "" {
		t.Skip("isolated plan investigation")
	}
	e, err := OpenEngine(schema, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	f := QueryFilter{SourceID: "source", KeyID: "caller"}
	now := time.Now()
	from, to := now.Add(-24*time.Hour).Unix(), now.Unix()
	q, a := scopedBillingAlignment(f, from, to)
	where, b := billingWhere(f, from, to)
	rows, err := e.DB.QueryContext(context.Background(), "EXPLAIN "+q+"SELECT * FROM aligned_billing WHERE "+where, append(a, b...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	t.Log(strings.Join(plan, "\n"))
}

// Explicit opt-in against a private copy of verified production facts; it never
// reads a production DSN or writes to a source gateway.
func TestPostgresVolumeFixtureReads(t *testing.T) {
	dsn := os.Getenv("PG_VOLUME_FIXTURE_URL")
	if dsn == "" {
		t.Skip("private volume fixture")
	}
	e, err := OpenEngine("analytics", Config{ControlDatabaseURL: dsn, Timezone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if os.Getenv("PG_VOLUME_PREPARE_HOURS") == "1" {
		rows, err := e.DB.Query("SELECT start_ms FROM fact_partitions ORDER BY start_ms")
		if err != nil {
			t.Fatal(err)
		}
		var hours []int64
		for rows.Next() {
			var h int64
			if err = rows.Scan(&h); err != nil {
				t.Fatal(err)
			}
			hours = append(hours, h)
		}
		rows.Close()
		for _, h := range hours {
			tx, err := e.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err = e.rebuildRollup(context.Background(), tx, "hour", h, h+3600000); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
	}
	var latest int64
	if err = e.DB.QueryRow("SELECT max(last_ms) FROM fact_source_ranges").Scan(&latest); err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"3h", "24h", "all"} {
		for _, model := range []string{"", "gpt-5.5"} {
			began := time.Now()
			result, err := e.Query(context.Background(), QueryFilter{Range: window, Model: model, SourceID: "primary", To: latest / 1000})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("range=%s model_filtered=%t elapsed=%s channels=%d coverage=%s", window, model != "", time.Since(began), len(result.Data), result.Coverage)
		}
	}
}

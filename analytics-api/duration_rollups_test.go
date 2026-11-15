package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestChannelTotalTimeUsesScopedAttemptDurations(t *testing.T) {
	for _, age := range []time.Duration{2 * time.Minute, 72 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			e := stateTestEngine(t)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Minute)
			base := Fact{Schema: 1, SourceID: "source", KeyID: "caller", Kind: "attempt", Provider: "channel", Model: "alias", UpstreamModel: "actual", Endpoint: "/v1/responses", Stream: true, Outcome: "success", AtMS: now.Add(-age).UnixMilli()}
			facts := []Fact{}
			for i, duration := range []float64{1000, 3000, 5000, 9000} {
				f := base
				f.EventID = fmt.Sprint("duration-", i)
				f.DurationMS = &duration
				f.AtMS += int64(i)
				f.Outcome = []string{"success", "failed", "client_cancelled", "hedge_cancelled"}[i]
				facts = append(facts, f)
			}
			high := 999999.0
			for _, kind := range []string{"request", "dispatch", "trace", "billing"} {
				f := base
				f.EventID, f.Kind, f.DurationMS = kind, kind, &high
				facts = append(facts, f)
			}
			f := base
			f.EventID, f.Outcome, f.DurationMS = "skipped", "skipped", &high
			facts = append(facts, f)
			f.EventID, f.Outcome, f.DurationMS = "missing-clock", "success", nil
			facts = append(facts, f)
			for _, dimension := range []string{"source", "key", "provider", "model", "upstream", "endpoint", "stream"} {
				f = base
				f.EventID, f.DurationMS = dimension, &high
				switch dimension {
				case "source":
					f.SourceID = "other"
				case "key":
					f.KeyID = "other"
				case "provider":
					f.Provider = "other"
				case "model":
					f.Model = "other"
				case "upstream":
					f.UpstreamModel = "other"
				case "endpoint":
					f.Endpoint = "/v1/messages"
				case "stream":
					f.Stream = false
				}
				facts = append(facts, f)
			}
			for range 2 {
				if err := e.Import(ctx, "duration", "immutable", facts); err != nil {
					t.Fatal(err)
				}
			}
			filter := QueryFilter{SourceIDs: []string{"source"}, SourceID: "source", KeyID: "caller", Provider: "channel", Model: "alias", UpstreamModel: "actual", Endpoint: "/v1/responses", Stream: "true", To: now.Unix()}
			for _, window := range []string{"7d", "all"} {
				filter.Range = window
				result, err := e.Query(ctx, filter)
				if err != nil || len(result.Data) != 1 {
					t.Fatal(result, err)
				}
				got := result.Data[0].Stats["duration"].(map[string]any)
				if got["sample_count"] != int64(4) || got["p50_ms"] != 3000.0 || got["p95_ms"] != 10000.0 || got["mean_ms"] != 4500.0 || *got["last_ms"].(*float64) != 9000 {
					t.Fatalf("%s duration mixed populations or quantiles: %#v", window, got)
				}
				if result.Data[0].Stats["started"] != int64(5) {
					t.Fatal("missing duration changed attempt counts", result.Data[0].Stats)
				}
			}
			if age < time.Hour {
				filter.Range = "15m"
				result, err := e.Query(ctx, filter)
				if err != nil || result.Data[0].Stats["duration"].(map[string]any)["sample_count"] != int64(4) {
					t.Fatal(result, err)
				}
			}
			// The unfiltered row merges endpoint/stream populations BEFORE taking
			// quantiles, rather than averaging their respective p50/p95 values.
			filter.Range, filter.Endpoint, filter.Stream = "all", "", ""
			merged, err := e.Query(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			got := merged.Data[0].Stats["duration"].(map[string]any)
			if got["sample_count"] != int64(6) || got["p50_ms"] != 5000.0 || got["p95_ms"] != 1200000.0 {
				t.Fatal("merged quantiles averaged separate populations", got)
			}
		})
	}
}

func TestDurationRollupMigrationBackfillAndRebuildPreserveExistingMetrics(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "duration-migration")
	e, err := OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { e.Close() }()
	at := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	duration, output := 0.0, 123.0
	f := Fact{Schema: 1, EventID: "legacy-duration", Kind: "attempt", AtMS: at.UnixMilli(), Provider: "channel", Model: "model", Outcome: "success", DurationMS: &duration, FirstOutputMS: &output}
	if err = e.Import(ctx, "legacy-duration", "etag", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	before, err := e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate exactly the previous deployed schema, not a fresh empty install.
	if _, err = e.DB.Exec(`ALTER TABLE rollups DROP COLUMN duration_count,DROP COLUMN duration_sum,DROP COLUMN duration_bins,DROP COLUMN last_duration,DROP COLUMN last_duration_ms;DELETE FROM schema_versions WHERE version='0006'`); err != nil {
		t.Fatal(err)
	}
	e.Close()
	e, err = OpenEngine(path, Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal("existing schema upgrade", err)
	}
	legacy, err := e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil || legacy.Data[0].Stats["duration"].(map[string]any)["sample_count"] != int64(0) {
		t.Fatal("unprepared history was fabricated", legacy, err)
	}
	var preparedRevision uint64
	for i := range 2 {
		if err = e.backfillDurationRollups(ctx, 0); err != nil {
			t.Fatal(err)
		}
		revision, err := e.currentRevision(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 && revision != preparedRevision {
			t.Fatal("idempotent preparation rewrote committed metadata", preparedRevision, revision)
		}
		preparedRevision = revision
	}
	after, err := e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil || !reflect.DeepEqual(before.Data, after.Data) {
		t.Fatal("preparation changed data or counted twice", before.Data, after.Data, err)
	}
	// Late arrivals still merge with the prepared zero value, and FIFO's
	// minute/hour/day rebuilds keep exactly the same duration population.
	f.EventID, f.AtMS = "later-duration", at.UnixMilli()+1
	later := 700.0
	f.DurationMS = &later
	if err = e.Import(ctx, "later-duration", "etag", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"minute", "hour", "day"} {
		start := at.Truncate(time.Minute).UnixMilli()
		width := int64(60000)
		if level == "hour" {
			start, width = at.Truncate(time.Hour).UnixMilli(), 3600000
		}
		if level == "day" {
			start, width = time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC).UnixMilli(), 86400000
		}
		tx, err := e.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = e.rebuildRollup(ctx, tx, level, start, start+width); err != nil {
			tx.Rollback()
			t.Fatal(level, err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	after, err = e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	got := after.Data[0].Stats["duration"].(map[string]any)
	if got["sample_count"] != int64(2) || got["mean_ms"] != 350.0 || got["p50_ms"] != 1.0 || got["p95_ms"] != 750.0 {
		t.Fatal("zero/late/rebuild", got)
	}
}

func TestDurationPreparationYieldsToLiveIngestion(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock(8395015)"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err = e.prepareDurationBucket(ctx, "minute", 0, 60000); !errors.Is(err, errDurationPreparationBusy) || time.Since(start) > time.Second {
		t.Fatal("maintenance blocked live work", err)
	}
}

func TestDurationPreparationPreservesStorageReserve(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	duration := 700.0
	if err := e.Import(ctx, "duration-headroom", "etag", []Fact{{Schema: 1, EventID: "duration-headroom", Kind: "attempt", AtMS: at.UnixMilli(), Provider: "p", Model: "m", Outcome: "success", DurationMS: &duration}}); err != nil {
		t.Fatal(err)
	}
	before, err := e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.DB.Exec("UPDATE storage_state SET ingest_limit_bytes=1 WHERE singleton"); err != nil {
		t.Fatal(err)
	}
	if err = e.prepareDurationBucket(ctx, "minute", at.UnixMilli(), at.UnixMilli()+60000); !errors.Is(err, errDurationPreparationHeadroom) {
		t.Fatal("maintenance consumed ingestion's storage reserve", err)
	}
	after, err := e.Query(ctx, QueryFilter{Range: "all"})
	if err != nil || !reflect.DeepEqual(before.Data, after.Data) {
		t.Fatal("refused preparation changed metrics", err)
	}
}

func TestDurationMixedOldWriterRowsAndInvalidRetainedMeasurements(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	duration := 100.0
	base := Fact{Schema: 1, EventID: "old-writer", Kind: "attempt", AtMS: at.UnixMilli(), SourceID: "s", KeyID: "k", Provider: "p", Model: "m", Outcome: "success", DurationMS: &duration}
	if err := e.Import(ctx, "old-writer", "etag", []Fact{base}); err != nil {
		t.Fatal(err)
	}
	// A previous binary's explicit column lists leave additive columns absent;
	// the new writer must still merge valid new measurements into these rows.
	if _, err := e.DB.Exec("UPDATE rollups SET duration_count=0,duration_sum=0,duration_bins=NULL,last_duration=NULL,last_duration_ms=NULL"); err != nil {
		t.Fatal(err)
	}
	base.EventID, base.AtMS = "new-writer", base.AtMS+1
	later := 700.0
	base.DurationMS = &later
	if err := e.Import(ctx, "new-writer", "etag", []Fact{base}); err != nil {
		t.Fatal(err)
	}
	filter := QueryFilter{Range: "all", SourceID: "s", SourceIDs: []string{"s"}, KeyID: "k"}
	result, err := e.Query(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Data[0].Stats["duration"].(map[string]any)
	if got["sample_count"] != int64(1) || got["p50_ms"] != 750.0 || *got["last_ms"].(*float64) != 700 {
		t.Fatal("old row nulls poisoned new histogram", got)
	}
	if err := e.backfillDurationRollups(ctx, 0); err != nil {
		t.Fatal(err)
	}
	result, err = e.Query(ctx, filter)
	if err != nil || result.Data[0].Stats["duration"].(map[string]any)["sample_count"] != int64(2) {
		t.Fatal("old and new populations were not reconciled", result, err)
	}
	// Ingest rejects these values; historical preparation independently guards
	// legacy/raw SQL data rather than relying on today's HTTP validator.
	for _, value := range []string{"'-1'", "'NaN'", "'Infinity'", "'-Infinity'", "NULL"} {
		if _, err = e.DB.Exec("UPDATE facts SET duration_ms=" + value + "::double precision"); err != nil {
			t.Fatal(err)
		}
		for _, bucket := range []struct {
			level      string
			start, end time.Time
		}{{"minute", at, at.Add(time.Minute)}, {"hour", at.Truncate(time.Hour), at.Truncate(time.Hour).Add(time.Hour)}, {"day", at.Truncate(24 * time.Hour), at.Truncate(24 * time.Hour).Add(24 * time.Hour)}} {
			if err = e.prepareDurationBucket(ctx, bucket.level, bucket.start.UnixMilli(), bucket.end.UnixMilli()); err != nil {
				t.Fatal(value, bucket.level, err)
			}
		}
		result, err = e.Query(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		got = result.Data[0].Stats["duration"].(map[string]any)
		if got["sample_count"] != int64(0) || got["p50_ms"] != nil || got["mean_ms"] != nil || got["last_ms"].(*float64) != nil {
			t.Fatal("invalid retained duration entered distribution", value, got)
		}
		if result.Data[0].Stats["started"] != int64(2) {
			t.Fatal("discarding invalid clocks changed attempt counts")
		}
	}
}

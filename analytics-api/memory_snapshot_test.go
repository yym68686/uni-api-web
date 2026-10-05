package main

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
	"time"
)

// Opt-in offline experiment. The database is opened read-only; all materialized
// tables and spills are temporary. Never point this at the live writer's file.
// Run one mode per fresh process/container to compare RSS/cgroup high-water marks.
func TestOfflineSpendMemory(t *testing.T) {
	path := os.Getenv("ANALYTICS_MEMORY_SNAPSHOT")
	if path == "" {
		t.Skip("set ANALYTICS_MEMORY_SNAPSHOT to an immutable offline checkpoint")
	}
	if os.Getenv("ANALYTICS_MEMORY_TEST_EVICT") == "1" {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		discardCheckpointCache(file, 0, 0)
		file.Close()
	}
	db, err := sql.Open("duckdb", path+"?access_mode=read_only")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	var version string
	if err = db.QueryRow("SELECT version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("duckdb=%s", version)
	limit := 1024
	if value := os.Getenv("ANALYTICS_MEMORY_TEST_MB"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(fmt.Sprintf("SET memory_limit='%dMiB';SET threads=2;SET temp_directory=%s;SET max_temp_directory_size='4GiB'", limit, sqlPath(t.TempDir()))); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var end int64
	if err = db.QueryRowContext(ctx, "SELECT max(at_ms) FROM facts").Scan(&end); err != nil {
		t.Fatal(err)
	}
	to := end/1000 + 1
	from := to - 86400
	f := QueryFilter{SourceID: "primary", Model: "gpt-6-sol"}
	if os.Getenv("ANALYTICS_MEMORY_ALL_MODELS") == "1" {
		f.Model = ""
	}
	where, args := billingWhere(f, from, to)
	mode := os.Getenv("ANALYTICS_MEMORY_TEST_MODE")
	baseline := func() (string, []any) {
		scope, sargs := billingWhere(f, 0, 1)
		// Strip only the synthetic time predicates; preserve source authorization.
		scope = scope[len("at_ms>=? AND at_ms<? AND "):]
		return `WITH candidate_billing AS MATERIALIZED (SELECT * FROM facts WHERE kind='billing' AND ` + scope + `),
  completion_keys AS (SELECT DISTINCT ` + billingIdentityColumns + ` FROM candidate_billing),
  completions AS (SELECT a.source_id,a.instance_id,a.request_id,a.attempt_id,a.provider,a.key_id,a.model,a.upstream_model,a.endpoint,a.stream,max(a.at_ms) completed_at,count(*) n FROM facts a JOIN completion_keys USING(` + billingIdentityColumns + `) WHERE a.kind='attempt' GROUP BY ALL),
  aligned_billing AS (SELECT b.* EXCLUDE(at_ms),coalesce(a.completed_at,b.at_ms) at_ms,coalesce(a.n,0) completions FROM candidate_billing b LEFT JOIN completions a USING(` + billingIdentityColumns + `)) `, sargs[2:]
	}
	start := time.Now()
	alignment, scopeArgs := scopedBillingAlignment(f, from, to)
	if mode == "baseline" {
		alignment, scopeArgs = baseline()
	}
	if _, err = db.ExecContext(ctx, "CREATE TEMP TABLE console_spend_facts AS "+alignment+"SELECT * FROM aligned_billing WHERE "+where, append(scopeArgs, args...)...); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	var rows int64
	if err = db.QueryRow("SELECT count(*) FROM console_spend_facts").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	var bytes int64
	if err = db.QueryRow("SELECT coalesce(sum(memory_usage_bytes),0) FROM duckdb_memory() WHERE tag='IN_MEMORY_TABLE'").Scan(&bytes); err != nil {
		t.Fatal(err)
	}
	t.Logf("mode=%s budget_mib=%d rows=%d alignment_ms=%d temp_relation_bytes=%d", mode, limit, rows, elapsed.Milliseconds(), bytes)
	if mode == "compare" {
		oracle, oargs := baseline()
		if _, err = db.ExecContext(ctx, "CREATE TEMP TABLE oracle AS "+oracle+"SELECT * FROM aligned_billing WHERE "+where, append(oargs, args...)...); err != nil {
			t.Fatal(err)
		}
		columns := billingProjection + ",completions"
		var diff int
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM ((SELECT "+columns+" FROM oracle EXCEPT ALL SELECT "+columns+" FROM console_spend_facts) UNION ALL (SELECT "+columns+" FROM console_spend_facts EXCEPT ALL SELECT "+columns+" FROM oracle))").Scan(&diff); err != nil {
			t.Fatal(err)
		}
		if diff != 0 {
			t.Fatalf("different rows=%d", diff)
		}
		t.Log("exact bidirectional multiset comparison passed")
	}
	// Real global claim aggregation, not just the initial alignment fragment.
	start = time.Now()
	if _, err = db.ExecContext(ctx, billingClaimsSQL); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM console_spend_claims").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	t.Logf("claims=%d claims_ms=%d process=%v cgroup=%v", rows, time.Since(start).Milliseconds(), processMemory(), cgroupMemory())
}

// Measures copy + compression + upload-body read separately from the database
// writer. No S3 calls are made; it does not measure real storage upload latency.
func TestOfflineCheckpointMemory(t *testing.T) {
	path := os.Getenv("ANALYTICS_MEMORY_SNAPSHOT")
	if path == "" {
		t.Skip("offline snapshot required")
	}
	src, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if os.Getenv("ANALYTICS_MEMORY_TEST_EVICT") == "1" {
		discardCheckpointCache(src, 0, 0)
	}
	window, err := strconv.ParseInt(os.Getenv("ANALYTICS_CHECKPOINT_CACHE_WINDOW_MB"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	raw, err := os.Create(dir + "/raw.duckdb")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	began := time.Now()
	rawHash := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(&checkpointCacheWriter{file: raw, window: window << 20}, rawHash), contextReader{context.Background(), src}, make([]byte, checkpointBufferSize))
	if err != nil {
		t.Fatal(err)
	}
	if err = raw.Sync(); err != nil {
		t.Fatal(err)
	}
	copied := time.Since(began)
	if _, err = raw.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	packed, err := os.Create(dir + "/raw.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Close()
	gz, _ := gzip.NewWriterLevel(&checkpointCacheWriter{file: packed, window: window << 20}, gzip.BestSpeed)
	if _, err = io.CopyBuffer(gz, &checkpointCacheReader{file: raw, window: window << 20}, make([]byte, checkpointBufferSize)); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if window > 0 {
		if err = packed.Sync(); err != nil {
			t.Fatal(err)
		}
		discardCheckpointCache(packed, 0, 0)
	}
	if _, err = packed.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	compressed, err := io.CopyBuffer(io.Discard, &checkpointCacheReader{file: packed, window: window << 20}, make([]byte, checkpointBufferSize))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("window_mib=%d raw_bytes=%d compressed_bytes=%d copy_ms=%d total_ms=%d process=%v cgroup=%v", window, n, compressed, copied.Milliseconds(), time.Since(began).Milliseconds(), processMemory(), cgroupMemory())
}

// Exercise a real incremental write and metric query on a writable disposable
// copy. Unlike the read-only SQL benchmark, this loads the production PK indexes.
func TestOfflineIncrementalImportMemory(t *testing.T) {
	path := os.Getenv("ANALYTICS_MEMORY_SNAPSHOT")
	if path == "" {
		t.Skip("offline snapshot required")
	}
	budget, err := strconv.Atoi(os.Getenv("ANALYTICS_MEMORY_TEST_MB"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dstPath := dir + "/history.duckdb"
	src, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.CopyBuffer(&checkpointCacheWriter{file: dst, window: 32 << 20}, src, make([]byte, checkpointBufferSize)); err != nil {
		t.Fatal(err)
	}
	if err = dst.Sync(); err != nil {
		t.Fatal(err)
	}
	discardCheckpointCache(dst, 0, 0)
	dst.Close()
	e, err := OpenEngine(dstPath, Config{Timezone: "Asia/Shanghai", DataDir: dir, DatabaseMemoryLimitMB: budget, DatabaseTempLimitMB: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	var at int64
	if err = e.DB.QueryRow("SELECT max(at_ms) FROM facts").Scan(&at); err != nil {
		t.Fatal(err)
	}
	kind := os.Getenv("ANALYTICS_MEMORY_IMPORT_KIND")
	if kind == "" {
		kind = "attempt"
	}
	if kind != "attempt" && kind != "trace" && kind != "billing" {
		t.Fatal("unsupported offline fact kind")
	}
	facts := make([]Fact, 100)
	for i := range facts {
		facts[i] = attributedFact(fmt.Sprintf("offline-memory-%d", i), "offline-test-key", time.UnixMilli(at-int64(i)*1000))
		facts[i].Kind = kind
		facts[i].SourceID = "primary"
		facts[i].Model = "gpt-6-sol"
		facts[i].Outcome = "success"
		if kind == "trace" {
			facts[i].Stage = "request_received"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	began := time.Now()
	if err = e.Import(ctx, "offline-memory-import", "one", facts); err != nil {
		t.Fatalf("import class=%s", databaseErrorClass(err))
	}
	t.Logf("kind=%s budget_mib=%d import_ms=%d process=%v cgroup=%v", kind, budget, time.Since(began).Milliseconds(), processMemory(), cgroupMemory())
	before := e.Revision.Load()
	if err = e.Import(ctx, "offline-memory-import", "one", facts); err != nil {
		t.Fatal(err)
	}
	if e.Revision.Load() != before {
		t.Fatal("idempotent replay changed revision")
	}
	if kind != "attempt" {
		return // Keep non-metric import high-water marks separate from queries.
	}
	if _, err = e.Query(ctx, QueryFilter{Range: "24h", SourceID: "primary", Model: "gpt-6-sol", To: at/1000 + 1}); err != nil {
		t.Fatalf("query class=%s", databaseErrorClass(err))
	}
	t.Logf("metrics completed process=%v cgroup=%v", processMemory(), cgroupMemory())
}

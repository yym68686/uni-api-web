package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// Opt-in isolated measurement, never downloads or scans production facts.
func TestWarmCheckpointMillionObjectScale(t *testing.T) {
	if os.Getenv("RUN_STARTUP_SCALE") != "1" {
		t.Skip("set RUN_STARTUP_SCALE=1 for isolated 1.2m-object fixture")
	}
	ctx := context.Background()
	source := stateTestEngine(t)
	if _, err := source.DB.Exec(`INSERT INTO imported_objects SELECT 'facts/'||sha256(CAST(i AS VARCHAR))||'.jsonl','etag-'||CAST(i AS VARCHAR),current_timestamp,1 FROM range(1200000) t(i)`); err != nil {
		t.Fatal(err)
	}
	source.cfg.WarmCacheDir = t.TempDir()
	cp := newCheckpointStore(&fakeStateObjects{}, Config{Timezone: "UTC"})
	path := filepath.Join(t.TempDir(), "history.duckdb")
	m, err := source.physicalCheckpoint(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = cp.publishWarm(ctx, source, path, m); err != nil {
		t.Fatal(err)
	}
	var times []time.Duration
	for i := 0; i < 20; i++ {
		dir, err := os.MkdirTemp("", "uni-warm-scale-")
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		e, err := OpenEngine(filepath.Join(dir, "history.duckdb"), Config{Timezone: "UTC", WarmCacheDir: source.cfg.WarmCacheDir})
		if err != nil {
			os.RemoveAll(dir)
			t.Fatal(err)
		}
		ok, err := cp.restoreWarm(ctx, e)
		elapsed := time.Since(started)
		var n int
		e.DB.QueryRow(`SELECT count(*) FROM imported_objects`).Scan(&n)
		e.Close()
		os.RemoveAll(dir)
		if !ok || err != nil || n != 1200000 {
			t.Fatal(ok, err, n)
		}
		times = append(times, elapsed)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	t.Logf("verified independent warm database: objects=1200000 bytes=%d rounds=20 p50=%s p95=%s max=%s (synthetic index, not full production facts)", m.Bytes, times[9], times[18], times[19])
}

// This measures the actual full-byte verification/copy lower bound on this
// machine without retaining many GiB in Go memory or touching production.
func TestWarmSixGiBVerifiedCopyScale(t *testing.T) {
	if os.Getenv("RUN_STARTUP_COPY_SCALE") != "1" {
		t.Skip("opt-in large local copy")
	}
	dir := t.TempDir()
	src, err := os.Create(filepath.Join(dir, "source"))
	if err != nil {
		t.Fatal(err)
	}
	const size = int64(6400) * 1024 * 1024
	if err = src.Truncate(size); err != nil {
		t.Fatal(err)
	}
	// Sparse zeros are deliberately a best-case filesystem fixture, not a
	// compressed checkpoint or a claim about production storage throughput.
	hash := sha256.New()
	if _, err = io.Copy(hash, src); err != nil {
		t.Fatal(err)
	}
	m := physicalManifest{Bytes: size, SHA256: hex.EncodeToString(hash.Sum(nil))}
	src.Seek(0, 0)
	dst, err := os.OpenFile(filepath.Join(dir, "copy"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = copyVerifiedCheckpoint(context.Background(), src, dst, m)
	elapsed := time.Since(started)
	src.Close()
	dst.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("verified local copy bytes=%d duration=%s (best-case sparse fixture, not production)", size, elapsed)
}

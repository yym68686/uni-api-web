package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpointRestoresAggregatesAndReplayStateWithoutRevertingSettings(t *testing.T) {
	ctx := context.Background()
	source := stateTestEngine(t)
	f := Fact{Schema: 1, EventID: "request-one", Kind: "request", AtMS: time.Now().Add(-time.Minute).UnixMilli(), Provider: "channel", Model: "custom", Outcome: "success"}
	if err := source.Import(ctx, "one.jsonl", "e1", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	objects := &fakeStateObjects{}
	snapshots := newCheckpointStore(objects, Config{S3Bucket: "facts", S3Prefix: "v1", StateBucket: "state", StatePrefix: "analytics", Timezone: "UTC"})
	if err := snapshots.save(ctx, source); err != nil {
		t.Fatal(err)
	}
	target := stateTestEngine(t)
	if err := target.SavePrice(ctx, Price{Model: "custom", Input: 123, Verified: true}); err != nil {
		t.Fatal(err)
	}
	if restored, err := snapshots.restore(ctx, target); err != nil || !restored {
		t.Fatal(restored, err)
	}
	if err := target.Import(ctx, "one.jsonl", "e1", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	result, err := target.Query(ctx, QueryFilter{Range: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total["requests"] != float64(1) {
		t.Fatalf("replay duplicated facts: %v", result.Total)
	}
	var price float64
	if err := target.DB.QueryRow("SELECT input FROM prices WHERE model='custom'").Scan(&price); err != nil || price != 123 {
		t.Fatalf("checkpoint replaced current settings: %v %v", price, err)
	}
	// An older replica must not replace a more complete snapshot.
	revision := objects.revision
	if err := snapshots.save(ctx, stateTestEngine(t)); err != nil {
		t.Fatal(err)
	}
	if objects.revision != revision {
		t.Fatal("empty cache replaced the existing checkpoint")
	}
	objects.body[0] ^= 1
	fresh := stateTestEngine(t)
	if restored, err := snapshots.restore(ctx, fresh); err == nil || restored {
		t.Fatal("corrupt checkpoint accepted")
	}
	var count int
	if err := fresh.DB.QueryRow("SELECT count(*) FROM imported_objects").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed restore mutated replay state: %d %v", count, err)
	}
}

func TestCheckpointArchiveRejectsPathsOutsideKnownTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tarfile := tar.NewWriter(gz)
	if err := tarfile.WriteHeader(&tar.Header{Name: "../escaped", Size: 1, Mode: 0600, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = tarfile.Write([]byte("x"))
	_ = tarfile.Close()
	_ = gz.Close()
	_ = f.Close()
	if err := unpackCheckpoint(path, dir); err == nil {
		t.Fatal("unsafe archive member accepted")
	}
}

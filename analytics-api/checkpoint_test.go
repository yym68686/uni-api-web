package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

func TestErrorEvidenceCheckpointRestoresPreviousVersionsWithoutReplayingHistory(t *testing.T) {
	for _, version := range []string{"v3", "v2"} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			source := stateTestEngine(t)
			f := Fact{Schema: 1, EventID: "old-rejection", Kind: "billing", AtMS: time.Now().UnixMilli(), Status: 403}
			if err := source.Import(ctx, "already-imported", "etag", []Fact{f}); err != nil {
				t.Fatal(err)
			}
			if _, err := source.DB.Exec(`CREATE TABLE previous_facts AS SELECT * EXCLUDE(upstream_error_sha256) FROM facts; DROP TABLE facts; ALTER TABLE previous_facts RENAME TO facts`); err != nil {
				t.Fatal(err)
			}
			objects := &fakeStateObjects{}
			store := newCheckpointStore(objects, Config{StateBucket: "state"})
			old := *store
			old.key, old.source = store.legacyKey, store.legacySource
			if version == "v2" {
				old.key, old.source = store.olderKey, store.olderSource
			}
			if err := old.save(ctx, source); err != nil {
				t.Fatal(err)
			}
			store.client = &restoreTestObjects{objects, func(ctx context.Context, req *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
				if aws.ToString(req.Key) != old.key {
					return nil, &smithy.GenericAPIError{Code: "NoSuchKey"}
				}
				return objects.GetObject(ctx, req)
			}}
			target := stateTestEngine(t)
			if ok, err := store.restore(ctx, target); err != nil || !ok {
				t.Fatal(ok, err)
			}
			var n int
			var digest string
			if err := target.DB.QueryRow(`SELECT count(*) FROM imported_objects`).Scan(&n); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			if err := target.DB.QueryRow(`SELECT coalesce(upstream_error_sha256,'') FROM facts`).Scan(&digest); err != nil || digest != "" {
				t.Fatal(digest, err)
			}
		})
	}
}

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

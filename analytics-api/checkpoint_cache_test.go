package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpointCacheIOPreservesBytesAndRetrySeek(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "cache-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	want := bytes.Repeat([]byte("immutable-checkpoint\n"), 20000)
	w := &checkpointCacheWriter{file: file, window: 4096}
	if _, err = io.CopyBuffer(w, bytes.NewBuffer(want), make([]byte, 8192)); err != nil {
		t.Fatal(err)
	}
	if w.synced == 0 {
		t.Fatal("flush was not exercised")
	}
	r := &checkpointCacheReader{file: file, window: 4096}
	for retry := 0; retry < 2; retry++ {
		if _, err = r.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("retry corrupted checkpoint", err)
		}
	}
	if _, err = r.Seek(8, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, want[8:]) {
		t.Fatal("partial seek", err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("failed")); err == nil {
		t.Fatal("closed write accepted")
	}
}

func TestPhysicalCheckpointWithCacheWindowPreservesManifest(t *testing.T) {
	e := stateTestEngine(t)
	e.cfg.CheckpointCacheWindowMB = 1
	f := attributedFact("checkpoint-cache", "key", time.Now())
	if err := e.Import(context.Background(), "cache-object", "one", []Fact{f}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "history.duckdb")
	m, err := e.physicalCheckpoint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	if m.Bytes != int64(len(raw)) || m.SHA256 != hex.EncodeToString(hash[:]) || m.Rows["facts"] != 1 {
		t.Fatal(m)
	}
}

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWarmCheckpointIndependentFilesAndCorruptionFallback(t *testing.T) {
	ctx := context.Background()
	store, _ := checkpointFixture(t)
	source := stateTestEngine(t)
	if ok, err := store.restore(ctx, source); !ok || err != nil {
		t.Fatal(ok, err)
	}
	source.cfg.WarmCacheDir = t.TempDir()
	path := filepath.Join(t.TempDir(), "history.duckdb")
	m, err := source.physicalCheckpoint(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.publishWarm(ctx, source, path, m); err != nil {
		t.Fatal(err)
	}
	target := stateTestEngine(t)
	target.cfg.WarmCacheDir = source.cfg.WarmCacheDir
	if ok, err := store.restoreWarm(ctx, target); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, err = target.DB.Exec(`DELETE FROM facts`); err != nil {
		t.Fatal(err)
	}
	other := stateTestEngine(t)
	other.cfg.WarmCacheDir = source.cfg.WarmCacheDir
	if ok, err := store.restoreWarm(ctx, other); !ok || err != nil {
		t.Fatal(ok, err)
	}
	var n int
	other.DB.QueryRow(`SELECT count(*) FROM facts`).Scan(&n)
	if n != 1 {
		t.Fatal("replica changed immutable cache", n)
	}
	if err = os.WriteFile(filepath.Join(source.cfg.WarmCacheDir, m.SHA256+".duckdb"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	broken := stateTestEngine(t)
	broken.cfg.WarmCacheDir = source.cfg.WarmCacheDir
	if ok, err := store.restoreWarm(ctx, broken); ok || err == nil {
		t.Fatal("corrupt warm copy accepted")
	}
	if ok, err := store.restore(ctx, broken); !ok || err != nil {
		t.Fatal("S3 fallback failed", ok, err)
	}
}

func TestCheckpointPublicationDoesNotRegressNewerReplica(t *testing.T) {
	ctx := context.Background()
	store, objects := checkpointFixture(t)
	older, newer := stateTestEngine(t), stateTestEngine(t)
	for _, e := range []*Engine{older, newer} {
		if ok, err := store.restore(ctx, e); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	newer.setReplayState("source", replayCheckpoint{Generation: "g", Cursor: 5, AuditMS: 20})
	if err := store.save(ctx, newer); err != nil {
		t.Fatal(err)
	}
	before := objects.revision
	older.setReplayState("source", replayCheckpoint{Generation: "g", Cursor: 4, AuditMS: 10})
	if err := store.save(ctx, older); err != nil {
		t.Fatal(err)
	}
	if objects.revision != before {
		t.Fatal("older replay position replaced checkpoint")
	}
	if err := newer.Import(ctx, "new-object", "e2", []Fact{{Schema: 1, EventID: "second", Kind: "request", AtMS: time.Now().UnixMilli()}}); err != nil {
		t.Fatal(err)
	}
	if err := store.save(ctx, newer); err != nil {
		t.Fatal(err)
	}
	before = objects.revision
	older.setReplayState("source", replayCheckpoint{Generation: "g", Cursor: 6, AuditMS: 30})
	if err := store.save(ctx, older); err != nil {
		t.Fatal(err)
	}
	if objects.revision != before {
		t.Fatal("smaller history replaced newer checkpoint")
	}
}

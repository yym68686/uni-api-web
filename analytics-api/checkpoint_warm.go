package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type warmCheckpoint struct {
	Source   string           `json:"source"`
	File     string           `json:"file"`
	Manifest physicalManifest `json:"manifest"`
}

// Only immutable, verified copies live in this optional shared directory.
// Each replica still writes its own /data database. Never attach the shared file.
func (s *checkpointStore) publishWarm(ctx context.Context, e *Engine, path string, manifest physicalManifest) error {
	root := e.cfg.WarmCacheDir
	if root == "" {
		return nil
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(root, "prepare-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(filepath.Join(dir, "history.duckdb"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = copyVerifiedCheckpoint(ctx, src, dst, manifest); err != nil {
		dst.Close()
		return err
	}
	if err = dst.Close(); err != nil {
		return err
	}
	lock, err := warmLock(root, true)
	if err != nil {
		return err
	}
	defer closeWarmLock(lock)
	var old warmCheckpoint
	if b, readErr := os.ReadFile(filepath.Join(root, "current.json")); readErr == nil {
		_ = json.Unmarshal(b, &old)
		if old.Source == s.physicalSource && old.Manifest.Schema == manifest.Schema && old.Manifest.Version == manifest.Version {
			if old.Manifest.Rows["imported_objects"] > manifest.Rows["imported_objects"] {
				return nil
			}
			for key, previous := range old.Manifest.Replay {
				current, ok := manifest.Replay[key]
				if ok && current.Generation == previous.Generation && (current.Cursor < previous.Cursor || current.AuditMS < previous.AuditMS) {
					return nil
				}
			}
		}
	}
	name := manifest.SHA256 + ".duckdb"
	if err = os.Rename(filepath.Join(dir, "history.duckdb"), filepath.Join(root, name)); err != nil {
		return err
	}
	doc := warmCheckpoint{Source: s.physicalSource, File: name, Manifest: manifest}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	pointer := filepath.Join(dir, "current.json")
	if err = os.WriteFile(pointer, raw, 0600); err != nil {
		return err
	}
	f, err := os.OpenFile(pointer, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	err = f.Sync()
	f.Close()
	if err != nil {
		return err
	}
	if err = os.Rename(pointer, filepath.Join(root, "current.json")); err != nil {
		return err
	}
	d, err := os.Open(root)
	if err != nil {
		return err
	}
	err = d.Sync()
	d.Close()
	if err != nil {
		return err
	}
	// Shared lock holders have finished copying. Keep the former generation as
	// an operational recovery artifact; remove only our older digest-named files.
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		n := entry.Name()
		if validWarmName(n) && n != name && n != old.File {
			_ = os.Remove(filepath.Join(root, n))
		}
	}
	return nil
}

func validWarmName(name string) bool {
	if len(name) != 71 || filepath.Ext(name) != ".duckdb" {
		return false
	}
	_, err := hex.DecodeString(name[:64])
	return err == nil
}

func warmLock(root string, exclusive bool) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(root, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	mode := unix.LOCK_SH
	if exclusive {
		mode = unix.LOCK_EX
	}
	// Cache contention must never block serving or a checkpoint indefinitely.
	if err = unix.Flock(int(f.Fd()), mode|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func closeWarmLock(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }

func copyVerifiedCheckpoint(ctx context.Context, src, dst *os.File, m physicalManifest) error {
	if m.Bytes < 0 || len(m.SHA256) != 64 {
		return errors.New("invalid warm manifest")
	}
	info, err := src.Stat()
	if err != nil {
		return err
	}
	if info.Size() != m.Bytes {
		return errors.New("warm checkpoint size mismatch")
	}
	hash := sha256.New()
	var n int64
	if cloneCheckpointFile(dst, src) == nil {
		if _, err = dst.Seek(0, io.SeekStart); err != nil {
			return err
		}
		// Reflink is independent copy-on-write, not a hard link. Verify every byte
		// even on the warm path; a damaged cache cannot fabricate a ready replica.
		n, err = io.CopyBuffer(hash, contextReader{ctx, dst}, make([]byte, checkpointBufferSize))
	} else {
		if _, err = src.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err = dst.Truncate(0); err != nil {
			return err
		}
		if _, err = dst.Seek(0, 0); err != nil {
			return err
		}
		n, err = io.CopyBuffer(io.MultiWriter(dst, hash), contextReader{ctx, src}, make([]byte, checkpointBufferSize))
	}
	if err != nil {
		return err
	}
	if n != m.Bytes || hex.EncodeToString(hash.Sum(nil)) != m.SHA256 {
		return errors.New("warm checkpoint integrity mismatch")
	}
	return dst.Sync()
}

func (s *checkpointStore) restoreWarm(ctx context.Context, e *Engine) (bool, error) {
	if e.cfg.WarmCacheDir == "" {
		return false, nil
	}
	started := time.Now()
	lock, err := warmLock(e.cfg.WarmCacheDir, false)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer closeWarmLock(lock)
	pointer, err := os.Open(filepath.Join(e.cfg.WarmCacheDir, "current.json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	raw, err := io.ReadAll(io.LimitReader(pointer, (64<<10)+1))
	pointer.Close()
	if err != nil {
		return false, err
	}
	if len(raw) > 64<<10 {
		return false, errors.New("warm manifest too large")
	}
	var doc warmCheckpoint
	if json.Unmarshal(raw, &doc) != nil || doc.Source != s.physicalSource || !validWarmName(doc.File) || doc.File != doc.Manifest.SHA256+".duckdb" {
		return false, errors.New("incompatible warm checkpoint")
	}
	var version string
	if err = e.DB.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		return false, err
	}
	schema, err := historySchema(ctx, e.DB, "history")
	if err != nil {
		return false, err
	}
	if version != doc.Manifest.Version || schema != doc.Manifest.Schema {
		return false, errors.New("incompatible warm database")
	}
	src, err := os.Open(filepath.Join(e.cfg.WarmCacheDir, doc.File))
	if err != nil {
		return false, err
	}
	defer src.Close()
	dir, err := os.MkdirTemp(filepath.Dir(e.historyPath), "warm-restore-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "history.duckdb")
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return false, err
	}
	err = copyVerifiedCheckpoint(ctx, src, dst, doc.Manifest)
	dst.Close()
	if err != nil {
		return false, err
	}
	if err = validatePhysical(ctx, path, doc.Manifest); err != nil {
		return false, err
	}
	if err = e.installPhysical(ctx, path); err != nil {
		return false, err
	}
	e.mu.Lock()
	e.replay = doc.Manifest.Replay
	e.mu.Unlock()
	log.Printf("analytics checkpoint format=warm outcome=restored bytes=%d duration_ms=%d", doc.Manifest.Bytes, time.Since(started).Milliseconds())
	return true, nil
}

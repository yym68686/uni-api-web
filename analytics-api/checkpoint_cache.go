package main

import (
	"io"
	"os"
)

// An optional bounded write-back window for checkpoint scratch files. fsync
// precedes the reclaim hint so dirty data is never treated as disposable. Never
// wrap the live DuckDB database: its writer and indexes own their caching policy.
type checkpointCacheWriter struct {
	file                    *os.File
	window, written, synced int64
}

func (w *checkpointCacheWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	w.written += int64(n)
	if err == nil && w.window > 0 && w.written-w.synced >= w.window {
		if err = w.file.Sync(); err == nil {
			discardCheckpointCache(w.file, w.synced, w.written-w.synced)
			w.synced = w.written
		}
	}
	return n, err
}

// Scratch files are immutable before reading. Drop consumed pages at window
// boundaries and the tail at EOF, retaining Seek for SDK upload retries.
type checkpointCacheReader struct {
	file                      *os.File
	window, offset, reclaimed int64
}

func (r *checkpointCacheReader) Read(p []byte) (int, error) {
	n, err := r.file.Read(p)
	r.offset += int64(n)
	if r.window > 0 && (r.offset-r.reclaimed >= r.window || err == io.EOF) {
		if r.offset > r.reclaimed {
			discardCheckpointCache(r.file, r.reclaimed, r.offset-r.reclaimed)
		}
		r.reclaimed = r.offset
	}
	return n, err
}
func (r *checkpointCacheReader) Seek(offset int64, whence int) (int64, error) {
	at, err := r.file.Seek(offset, whence)
	if err == nil {
		r.offset = at
		r.reclaimed = at
	}
	return at, err
}

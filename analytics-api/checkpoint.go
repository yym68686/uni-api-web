package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const maxCheckpointBytes int64 = 1 << 30

var checkpointTables = []string{"facts", "rollups", "imported_objects"}

type checkpointClient interface {
	stateObjectClient
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}
type checkpointStore struct {
	client                  checkpointClient
	bucket, key, source     string
	legacyKey, legacySource string
}

func newCheckpointStore(client checkpointClient, cfg Config) *checkpointStore {
	sum := sha256.Sum256([]byte(strings.Join([]string{cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Prefix, cfg.Timezone}, "\x00")))
	id := hex.EncodeToString(sum[:])
	return &checkpointStore{client: client, bucket: cfg.StateBucket, key: strings.Trim(cfg.StatePrefix, "/") + "/cache-v3-" + id + ".tar.gz", source: "v3-" + id, legacyKey: strings.Trim(cfg.StatePrefix, "/") + "/cache-v2-" + id + ".tar.gz", legacySource: "v2-" + id}
}

func sqlPath(path string) string { return "'" + strings.ReplaceAll(path, "'", "''") + "'" }

// A checkpoint contains only derived facts/rollups and replay checkpoints.
// Settings have their own conditional S3 document and are never rolled back
// when a replica starts from an older query cache.
func (e *Engine) checkpoint(ctx context.Context, dir string) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int64
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM imported_objects").Scan(&n); err != nil {
		return 0, err
	}
	for _, table := range checkpointTables {
		if _, err = tx.ExecContext(ctx, "COPY "+table+" TO "+sqlPath(filepath.Join(dir, table+".parquet"))+" (FORMAT PARQUET, COMPRESSION ZSTD)"); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit()
}

func packCheckpoint(dir string) (string, string, error) {
	return packCheckpointContext(context.Background(), dir)
}

func packCheckpointContext(ctx context.Context, dir string) (string, string, error) {
	path := filepath.Join(dir, "checkpoint.tar.gz")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", "", err
	}
	hash := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, hash))
	archive := tar.NewWriter(gz)
	fail := func(err error) (string, string, error) {
		_ = archive.Close()
		_ = gz.Close()
		_ = f.Close()
		return "", "", err
	}
	for _, table := range checkpointTables {
		name := table + ".parquet"
		part, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return fail(err)
		}
		st, err := part.Stat()
		if err != nil {
			part.Close()
			return fail(err)
		}
		if err = archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: st.Size(), Typeflag: tar.TypeReg}); err == nil {
			_, err = io.Copy(archive, contextReader{ctx, part})
		}
		part.Close()
		if err != nil {
			return fail(err)
		}
	}
	if err = archive.Close(); err != nil {
		return fail(err)
	}
	if err = gz.Close(); err != nil {
		return fail(err)
	}
	if err = f.Close(); err != nil {
		return "", "", err
	}
	return path, hex.EncodeToString(hash.Sum(nil)), nil
}

func unpackCheckpoint(path, dir string) error {
	return unpackCheckpointContext(context.Background(), path, dir)
}

func unpackCheckpointContext(ctx context.Context, path, dir string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(contextReader{ctx, f})
	if err != nil {
		return err
	}
	defer gz.Close()
	archive := tar.NewReader(gz)
	seen := map[string]bool{}
	var total int64
	for {
		h, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		allowed := false
		for _, table := range checkpointTables {
			if h.Name == table+".parquet" {
				allowed = true
			}
		}
		if !allowed || seen[h.Name] || h.Typeflag != tar.TypeReg || h.Size < 0 {
			return errors.New("invalid checkpoint member")
		}
		total += h.Size
		if total > 4*maxCheckpointBytes {
			return errors.New("checkpoint expands beyond limit")
		}
		seen[h.Name] = true
		part, err := os.OpenFile(filepath.Join(dir, h.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = io.CopyN(part, archive, h.Size)
		closeErr := part.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if len(seen) != len(checkpointTables) {
		return errors.New("incomplete checkpoint")
	}
	return nil
}

func (s *checkpointStore) save(ctx context.Context, e *Engine) (err error) {
	stage := "read_metadata"
	defer func() { err = checkpointStageError(stage, err) }()
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key)})
	var etag string
	var previous int64 = -1
	if err == nil {
		etag = aws.ToString(head.ETag)
		previous, _ = strconv.ParseInt(head.Metadata["objects"], 10, 64)
		if etag == "" {
			return errors.New("checkpoint missing ETag")
		}
	} else if code := storageErrorCode(err); code != "NoSuchKey" && code != "NotFound" {
		return err
	}
	var current int64
	if err = e.DB.QueryRowContext(ctx, "SELECT count(*) FROM imported_objects").Scan(&current); err != nil {
		return err
	}
	if current <= previous {
		return nil
	}
	dir, err := os.MkdirTemp(e.cfg.DataDir, "checkpoint-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	stage = "export"
	n, err := e.checkpoint(ctx, dir)
	if err != nil {
		return err
	}
	stage = "pack"
	path, digest, err := packCheckpointContext(ctx, dir)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() > maxCheckpointBytes {
		return errors.New("checkpoint too large")
	}
	req := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key), Body: f, ContentType: aws.String("application/gzip"), Metadata: map[string]string{"schema": "1", "source": s.source, "objects": strconv.FormatInt(n, 10), "created-ms": strconv.FormatInt(time.Now().UnixMilli(), 10), "sha256": digest}}
	if etag == "" {
		req.IfNoneMatch = aws.String("*")
	} else {
		req.IfMatch = aws.String(etag)
	}
	stage = "upload"
	_, err = s.client.PutObject(ctx, req)
	if code := storageErrorCode(err); code == "PreconditionFailed" || code == "ConditionalRequestConflict" {
		return nil
	}
	return err
}

func (s *checkpointStore) restore(ctx context.Context, e *Engine) (restored bool, err error) {
	stage := "inspect_cache"
	defer func() { err = checkpointStageError(stage, err) }()
	var current int64
	if err := e.DB.QueryRowContext(ctx, "SELECT count(*) FROM imported_objects").Scan(&current); err != nil {
		return false, err
	}
	if current > 0 {
		return false, nil
	}
	stage = "download"
	expectedSource := s.source
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key)})
	if err != nil && (storageErrorCode(err) == "NoSuchKey" || storageErrorCode(err) == "NotFound") && s.legacyKey != "" {
		out, err = s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.legacyKey)})
		expectedSource = s.legacySource
	}
	if err != nil {
		if code := storageErrorCode(err); code == "NoSuchKey" || code == "NotFound" {
			return false, nil
		}
		return false, err
	}
	defer out.Body.Close()
	stage = "metadata"
	if out.Metadata["schema"] != "1" || out.Metadata["source"] != expectedSource || len(out.Metadata["sha256"]) != 64 || aws.ToInt64(out.ContentLength) > maxCheckpointBytes {
		return false, errors.New("incompatible checkpoint")
	}
	expected, err := strconv.ParseInt(out.Metadata["objects"], 10, 64)
	if err != nil || expected < 0 {
		return false, errors.New("invalid checkpoint count")
	}
	stage = "local_storage"
	dir, err := os.MkdirTemp(e.cfg.DataDir, "restore-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "snapshot.gz")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false, err
	}
	hash := sha256.New()
	stage = "download"
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(contextReader{ctx, out.Body}, maxCheckpointBytes+1))
	closeErr := f.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		stage = "local_storage"
		return false, closeErr
	}
	if out.ContentLength != nil && n < *out.ContentLength {
		return false, io.ErrUnexpectedEOF
	}
	stage = "integrity"
	if n > maxCheckpointBytes || hex.EncodeToString(hash.Sum(nil)) != out.Metadata["sha256"] {
		return false, errors.New("checkpoint integrity mismatch")
	}
	stage = "unpack"
	if err = unpackCheckpointContext(ctx, path, dir); err != nil {
		return false, err
	}
	stage = "apply"
	e.mu.Lock()
	defer e.mu.Unlock()
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	for _, table := range checkpointTables {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO "+table+" BY NAME SELECT * FROM read_parquet("+sqlPath(filepath.Join(dir, table+".parquet"))+")"); err != nil {
			return false, err
		}
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM imported_objects").Scan(&current); err != nil {
		return false, err
	}
	if current != expected {
		return false, fmt.Errorf("checkpoint count mismatch")
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	e.Revision.Add(1)
	return true, nil
}

func (s *Service) maybeCheckpoint(ctx context.Context) {
	if s.checkpoints == nil || time.Now().Unix()-s.lastCheckpoint.Load() < 300 || !s.checkpointActive.CompareAndSwap(false, true) {
		return
	}
	s.lastCheckpoint.Store(time.Now().Unix())
	s.checkpointWorkers.Add(1)
	go func() {
		defer s.checkpointWorkers.Done()
		defer s.checkpointActive.Store(false)
		started := time.Now()
		callCtx, cancel := context.WithTimeout(ctx, defaultCheckpointRestorePolicy.timeout)
		defer cancel()
		if err := s.checkpoints.save(callCtx, s.engine); err != nil {
			detail := checkpointFailure(err)
			s.checkpointError.Store(detail.Stage + ":" + detail.Class)
			logCheckpoint("save", 1, 1, started, detail, false)
		} else {
			s.checkpointError.Store("")
		}
	}()
}

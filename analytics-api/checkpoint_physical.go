package main

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const physicalSchema = "1"
const checkpointBufferSize = 256 << 10

type physicalManifest struct {
	Rows    map[string]int64 `json:"rows"`
	Schema  string           `json:"schema"`
	Version string           `json:"version"`
	Bytes   int64            `json:"bytes"`
	SHA256  string           `json:"sha256"`
}

func historySchema(ctx context.Context, db *sql.DB, catalog string) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT table_name,column_name,data_type,is_nullable,coalesce(column_default,'') FROM information_schema.columns WHERE table_catalog=? AND table_schema='main' ORDER BY table_name,ordinal_position`, catalog)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	enc := json.NewEncoder(hash)
	for rows.Next() {
		var fields [5]string
		if err = rows.Scan(&fields[0], &fields[1], &fields[2], &fields[3], &fields[4]); err != nil {
			rows.Close()
			return "", err
		}
		if err = enc.Encode(fields); err != nil {
			rows.Close()
			return "", err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	rows, err = db.QueryContext(ctx, `SELECT table_name,constraint_type,CAST(constraint_column_names AS VARCHAR) FROM duckdb_constraints() WHERE database_name=? ORDER BY table_name,constraint_type,constraint_column_names`, catalog)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var fields [3]string
		if err = rows.Scan(&fields[0], &fields[1], &fields[2]); err != nil {
			return "", err
		}
		if err = enc.Encode(fields); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), rows.Err()
}

func (e *Engine) physicalCheckpoint(ctx context.Context, path string) (physicalManifest, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m := physicalManifest{Rows: map[string]int64{}}
	var err error
	for _, table := range checkpointTables {
		var n int64
		if err = e.DB.QueryRowContext(ctx, "SELECT count(*) FROM history."+table).Scan(&n); err != nil {
			return m, err
		}
		m.Rows[table] = n
	}
	if m.Schema, err = historySchema(ctx, e.DB, "history"); err != nil {
		return m, err
	}
	if err = e.DB.QueryRowContext(ctx, "SELECT version()").Scan(&m.Version); err != nil {
		return m, err
	}
	// The write mutex and CHECKPOINT make this a self-contained immutable copy.
	// Never copy an actively written database or omit an outstanding WAL.
	if _, err = e.DB.ExecContext(ctx, "CHECKPOINT history"); err != nil {
		return m, err
	}
	src, err := os.Open(e.historyPath)
	if err != nil {
		return m, err
	}
	defer src.Close()
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return m, err
	}
	hash := sha256.New()
	m.Bytes, err = io.CopyBuffer(io.MultiWriter(dst, hash), contextReader{ctx, src}, make([]byte, checkpointBufferSize))
	if err == nil {
		err = dst.Sync()
	}
	closeErr := dst.Close()
	if err == nil {
		err = closeErr
	}
	m.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if m.Bytes > 4*maxCheckpointBytes {
		return m, errors.New("checkpoint expands beyond limit")
	}
	return m, err
}

func (s *checkpointStore) save(ctx context.Context, e *Engine) (err error) {
	stage := "read_metadata"
	defer func() { err = checkpointStageError(stage, err) }()
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.physicalKey)})
	etag := ""
	previous := int64(-1)
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
	if current <= previous && head != nil {
		var old physicalManifest
		schema, e1 := historySchema(ctx, e.DB, "history")
		var version string
		e2 := e.DB.QueryRowContext(ctx, "SELECT version()").Scan(&version)
		if e1 != nil {
			return e1
		}
		if e2 != nil {
			return e2
		}
		if json.Unmarshal([]byte(head.Metadata["manifest"]), &old) == nil && head.Metadata["schema"] == physicalSchema && old.Schema == schema && old.Version == version {
			return nil
		}
	}
	dir, err := os.MkdirTemp(e.cfg.DataDir, "physical-checkpoint-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	stage = "export"
	started := time.Now()
	path := filepath.Join(dir, "history.duckdb")
	manifest, err := e.physicalCheckpoint(ctx, path)
	if err != nil {
		return err
	}
	log.Printf("analytics checkpoint format=duckdb stage=copy duration_ms=%d bytes=%d objects=%d", time.Since(started).Milliseconds(), manifest.Bytes, manifest.Rows["imported_objects"])
	stage = "pack"
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	compressed, err := os.OpenFile(filepath.Join(dir, "history.gz"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer compressed.Close()
	hash := sha256.New()
	gz, _ := gzip.NewWriterLevel(io.MultiWriter(compressed, hash), gzip.BestSpeed)
	_, err = io.CopyBuffer(gz, contextReader{ctx, src}, make([]byte, checkpointBufferSize))
	closeErr := gz.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	size, err := compressed.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if size > maxCheckpointBytes {
		return errors.New("checkpoint too large")
	}
	if _, err = compressed.Seek(0, io.SeekStart); err != nil {
		return err
	}
	raw, _ := json.Marshal(manifest)
	req := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.physicalKey), Body: compressed, ContentType: aws.String("application/gzip"), Metadata: map[string]string{"schema": physicalSchema, "source": s.physicalSource, "objects": strconv.FormatInt(manifest.Rows["imported_objects"], 10), "sha256": hex.EncodeToString(hash.Sum(nil)), "manifest": string(raw)}}
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

func (s *checkpointStore) restore(ctx context.Context, e *Engine) (bool, error) {
	restored, found, err := s.restorePhysical(ctx, e)
	if err == nil && found {
		return restored, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		d := checkpointFailure(err)
		if d.Retryable || d.Class == "access_denied" {
			return false, err
		}
		log.Printf("analytics checkpoint format=duckdb stage=%s class=%s fallback=parquet", d.Stage, d.Class)
	}
	restored, legacyErr := s.restoreParquet(ctx, e)
	if legacyErr != nil {
		if err != nil {
			return false, err
		}
		return false, legacyErr
	}
	if !restored && err != nil {
		return false, err
	}
	return restored, nil
}

func (s *checkpointStore) restorePhysical(ctx context.Context, e *Engine) (restored, found bool, err error) {
	stage := "inspect_cache"
	defer func() { err = checkpointStageError(stage, err) }()
	var current int64
	if err = e.DB.QueryRowContext(ctx, "SELECT count(*) FROM imported_objects").Scan(&current); err != nil {
		return
	}
	if current > 0 {
		return false, true, nil
	}
	stage = "download"
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.physicalKey)})
	if err != nil {
		if code := storageErrorCode(err); code == "NoSuchKey" || code == "NotFound" {
			return false, false, nil
		}
		return false, false, err
	}
	defer out.Body.Close()
	found = true
	stage = "metadata"
	var manifest physicalManifest
	if out.Metadata["source"] != s.physicalSource || out.Metadata["schema"] != physicalSchema || len(out.Metadata["sha256"]) != 64 || json.Unmarshal([]byte(out.Metadata["manifest"]), &manifest) != nil || manifest.Bytes < 0 || manifest.Bytes > 4*maxCheckpointBytes || len(manifest.SHA256) != 64 || len(manifest.Rows) != len(checkpointTables) {
		return false, true, errors.New("incompatible physical checkpoint")
	}
	expected, parseErr := strconv.ParseInt(out.Metadata["objects"], 10, 64)
	if parseErr != nil || expected < 0 || expected != manifest.Rows["imported_objects"] {
		return false, true, errors.New("invalid checkpoint count")
	}
	var version string
	if err = e.DB.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		return false, true, err
	}
	if version != manifest.Version {
		return false, true, errors.New("incompatible database version")
	}
	schema, schemaErr := historySchema(ctx, e.DB, "history")
	if schemaErr != nil {
		return false, true, schemaErr
	}
	if schema != manifest.Schema {
		return false, true, errors.New("incompatible history schema")
	}
	stage = "local_storage"
	dir, err := os.MkdirTemp(filepath.Dir(e.historyPath), "restore-physical-")
	if err != nil {
		return false, true, err
	}
	defer os.RemoveAll(dir)
	compressed, err := os.OpenFile(filepath.Join(dir, "history.gz"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return false, true, err
	}
	defer compressed.Close()
	stage = "download"
	hash := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(compressed, hash), io.LimitReader(contextReader{ctx, out.Body}, maxCheckpointBytes+1), make([]byte, checkpointBufferSize))
	if err != nil {
		return false, true, err
	}
	if out.ContentLength != nil && n < *out.ContentLength {
		return false, true, io.ErrUnexpectedEOF
	}
	stage = "integrity"
	if n > maxCheckpointBytes || hex.EncodeToString(hash.Sum(nil)) != out.Metadata["sha256"] {
		return false, true, errors.New("checkpoint integrity mismatch")
	}
	if _, err = compressed.Seek(0, io.SeekStart); err != nil {
		return false, true, err
	}
	stage = "unpack"
	gz, err := gzip.NewReader(contextReader{ctx, compressed})
	if err != nil {
		return false, true, err
	}
	defer gz.Close()
	path := filepath.Join(dir, "history.duckdb")
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false, true, err
	}
	hash = sha256.New()
	n, err = io.CopyBuffer(io.MultiWriter(dst, hash), io.LimitReader(gz, manifest.Bytes+1), make([]byte, checkpointBufferSize))
	if err == nil {
		err = dst.Sync()
	}
	closeErr := dst.Close()
	if err != nil {
		return false, true, err
	}
	if closeErr != nil {
		return false, true, closeErr
	}
	if n != manifest.Bytes || hex.EncodeToString(hash.Sum(nil)) != manifest.SHA256 {
		return false, true, errors.New("database file integrity mismatch")
	}
	stage = "validate"
	if err = validatePhysical(ctx, path, manifest); err != nil {
		return false, true, err
	}
	stage = "apply"
	if err = e.installPhysical(ctx, path); err != nil {
		return false, true, err
	}
	log.Printf("analytics checkpoint format=duckdb stage=installed bytes=%d objects=%d", manifest.Bytes, expected)
	return true, true, nil
}

func validatePhysical(ctx context.Context, path string, m physicalManifest) error {
	u := url.URL{Path: path, RawQuery: "access_mode=read_only&enable_external_access=false&memory_limit=32MiB&threads=1"}
	db, err := sql.Open("duckdb", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var catalog string
	if err = db.QueryRowContext(ctx, "SELECT current_database()").Scan(&catalog); err != nil {
		return err
	}
	schema, err := historySchema(ctx, db, catalog)
	if err != nil {
		return err
	}
	if schema != m.Schema {
		return errors.New("physical schema mismatch")
	}
	var tables, views int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FILTER (WHERE table_type='BASE TABLE'),count(*) FILTER (WHERE table_type<>'BASE TABLE') FROM information_schema.tables WHERE table_catalog=?", catalog).Scan(&tables, &views); err != nil {
		return err
	}
	if tables != len(checkpointTables) || views != 0 {
		return errors.New("physical checkpoint contains unexpected tables")
	}
	for _, table := range checkpointTables {
		var n int64
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			return err
		}
		expected, ok := m.Rows[table]
		if !ok || n != expected {
			return errors.New("physical row count mismatch")
		}
	}
	return nil
}

// The startup worker installs before analytics readiness. The mutex protects
// writers and Prices() across catalog replacement; settings never detaches.
func (e *Engine) installPhysical(ctx context.Context, path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var n int64
	if err := e.DB.QueryRowContext(ctx, "SELECT count(*) FROM imported_objects").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return errors.New("history changed while restoring")
	}
	if _, err := e.DB.ExecContext(ctx, "DETACH history"); err != nil {
		return err
	}
	recovery, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	backup := e.historyPath + ".previous"
	reattach := func() error {
		_, err := e.DB.ExecContext(recovery, "ATTACH "+sqlPath(e.historyPath)+" AS history")
		return err
	}
	// A previous successful atomic rename may have been interrupted before
	// cleanup. The currently attached/validated file is authoritative here.
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		_ = reattach()
		return err
	}
	if err := os.Link(e.historyPath, backup); err != nil {
		_ = reattach()
		return err
	}
	if err := os.Rename(path, e.historyPath); err != nil {
		os.Remove(backup)
		_ = reattach()
		return err
	}
	if err := reattach(); err != nil {
		_ = os.Rename(backup, e.historyPath)
		_ = reattach()
		return err
	}
	if dir, err := os.Open(filepath.Dir(e.historyPath)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	_ = os.Remove(backup)
	e.Revision.Add(1)
	return nil
}

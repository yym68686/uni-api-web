package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const maxObjectBytes = 32 << 20

func (s *Service) importSingleS3(ctx context.Context) error {
	if s.cfg.S3Bucket == "" || s.cfg.S3Endpoint == "" {
		return errors.New("storage_not_configured")
	}
	client := s.factClient
	var err error
	if client == nil {
		client, err = newS3Client(ctx, s.cfg)
		if err != nil {
			return err
		}
	}
	known, err := s.engine.ImportedObjects(ctx)
	if err != nil {
		return err
	}
	pager := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(s.cfg.S3Bucket), Prefix: aws.String(strings.Trim(s.cfg.S3Prefix, "/") + "/")})
	var pending []types.Object
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if !strings.HasSuffix(key, ".jsonl") {
				continue
			}
			if etag, ok := known[sourceObjectKey(s.cfg.SourceID, key)]; ok {
				if etag != aws.ToString(obj.ETag) {
					return errors.New("immutable object changed")
				}
				continue
			}
			pending = append(pending, obj)
		}
	}
	// Catch up with live traffic before replaying older objects. Checkpoints still
	// cover the complete listing; no high-water mark can hide a late arrival.
	sort.Slice(pending, func(i, j int) bool {
		return aws.ToTime(pending[i].LastModified).After(aws.ToTime(pending[j].LastModified))
	})
	s.remaining.Store(int64(len(pending)))
	if len(pending) > 0 {
		fmt.Printf("analytics import pending_objects=%d\n", len(pending))
	}
	lastProgress := time.Now()
	var failures []error
	for start := 0; start < len(pending); {
		end := start
		var groupBytes int64
		for end < len(pending) && end-start < 256 {
			size := aws.ToInt64(pending[end].Size)
			if end > start && groupBytes+size > 16<<20 {
				break
			}
			groupBytes += size
			end++
		}
		group := pending[start:end]
		objects := make([]FactObject, len(group))
		errs := make([]error, len(group))
		var wg sync.WaitGroup
		slots := make(chan struct{}, 16)
		for i, obj := range group {
			wg.Add(1)
			go func(i int, obj types.Object) {
				defer wg.Done()
				select {
				case slots <- struct{}{}:
				case <-ctx.Done():
					errs[i] = ctx.Err()
					return
				}
				defer func() { <-slots }()
				if aws.ToInt64(obj.Size) > maxObjectBytes {
					errs[i] = errors.New("object_too_large")
					return
				}
				response, e := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.cfg.S3Bucket), Key: obj.Key})
				if e != nil {
					errs[i] = e
					return
				}
				defer response.Body.Close()
				facts, e := decodeFacts(response.Body)
				errs[i] = e
				sourceID := s.cfg.SourceID
				if sourceID == "" {
					sourceID = "primary"
				}
				for j := range facts {
					facts[j].SourceID = sourceID
					if sourceID != "primary" {
						facts[j].EventID = sourceID + "::" + facts[j].EventID
					}
				}
				objects[i] = FactObject{Key: sourceObjectKey(sourceID, aws.ToString(obj.Key)), ETag: aws.ToString(obj.ETag), Facts: facts}
			}(i, obj)
		}
		wg.Wait()
		good := make([]FactObject, 0, len(objects))
		for i, obj := range objects {
			if errs[i] != nil {
				failures = append(failures, errs[i])
			} else {
				good = append(good, obj)
			}
		}
		if len(good) > 0 {
			if err = s.engine.ImportBatch(ctx, good); err != nil {
				return err
			}
			s.remaining.Add(-int64(len(good)))
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(lastProgress) >= 10*time.Second {
			fmt.Printf("analytics import remaining_objects=%d\n", s.remaining.Load())
			lastProgress = time.Now()
		}
		start = end
	}
	if len(failures) > 0 {
		return failures[0]
	}
	return nil
}
func decodeFacts(body io.Reader) ([]Fact, error) {
	// Read one extra byte, rejecting rather than checkpointing a truncated object.
	raw, err := io.ReadAll(io.LimitReader(body, maxObjectBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxObjectBytes {
		return nil, errors.New("object_too_large")
	}
	scan := bufio.NewScanner(strings.NewReader(string(raw)))
	scan.Buffer(make([]byte, 64<<10), 1<<20)
	facts := []Fact{}
	for scan.Scan() {
		if len(strings.TrimSpace(scan.Text())) == 0 {
			continue
		}
		var f Fact
		if err = json.Unmarshal(scan.Bytes(), &f); err != nil {
			return nil, errors.New("invalid_fact_json")
		}
		if err = validFact(f); err != nil {
			return nil, errors.New("invalid_fact_schema")
		}
		facts = append(facts, f)
		if len(facts) > 10000 {
			return nil, errors.New("too_many_events")
		}
	}
	if scan.Err() != nil {
		return nil, errors.New("fact_line_too_large")
	}
	return facts, nil
}
func (s *Service) importObject(ctx context.Context, key, etag string, body io.Reader) error {
	facts, err := decodeFacts(body)
	if err != nil {
		return err
	}
	return s.engine.Import(ctx, key, etag, facts)
}
func (s *Service) maybeImport(ctx context.Context) {
	s.active.Store(1)
	defer s.active.Store(0)
	if err := s.importS3(ctx); err != nil {
		s.importFailures.Add(1)
		s.importError.Store(importErrorClass(err))
		fmt.Printf("analytics import failed class=%s\n", importErrorClass(err))
	} else {
		s.importError.Store("")
		s.lastCollect.Store(time.Now().UnixMilli())
		s.maybeCheckpoint(ctx)
	}
}
func importErrorClass(err error) string {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return "access_denied"
		case "NoSuchBucket":
			return "bucket_missing"
		default:
			return "storage_api_error"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	for _, kind := range []string{"invalid_fact_json", "invalid_fact_schema", "object_too_large", "too_many_events", "fact_line_too_large", "storage_not_configured", "immutable object changed"} {
		if err.Error() == kind {
			return kind
		}
	}
	return "unavailable"
}
func (s *Service) startImportLoop(ctx context.Context) {
	if s.lastCollect.Load() == 0 {
		s.maybeImport(ctx)
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.maybeImport(ctx)
		}
	}
}

func sourceObjectKey(source, key string) string {
	if source == "" || source == "primary" {
		return key
	}
	return source + "::" + key
}
func (s *Service) importS3(ctx context.Context) error {
	var failures []error
	var sources []sourceView
	primaryEnabled := s.control == nil
	if s.control != nil {
		var err error
		sources, err = s.control.listSources(ctx)
		if err != nil {
			return err
		}
		for _, v := range sources {
			if v.ID == s.cfg.SourceID {
				primaryEnabled = true
			}
		}
	}
	var remaining int64
	if primaryEnabled && s.cfg.S3Bucket != "" {
		if e := s.importSingleS3(ctx); e != nil {
			failures = append(failures, e)
		}
		remaining = s.remaining.Load()
	}
	for _, v := range sources {
		if v.ID == s.cfg.SourceID || !v.HasStorage {
			continue
		}
		src, e := s.control.source(ctx, v.ID)
		if e != nil {
			failures = append(failures, e)
			continue
		}
		cfg := s.cfg
		cfg.SourceID, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Prefix = src.ID, src.Storage.Endpoint, src.Storage.Bucket, src.Storage.Prefix
		client, e := newObjectClient(ctx, src.Storage.Endpoint, src.Storage.AccessKey, src.Storage.SecretKey, 20*time.Second)
		child := &Service{engine: s.engine, cfg: cfg, factClient: client}
		if e == nil {
			e = child.importSingleS3(ctx)
		}
		remaining += child.remaining.Load()
		if e != nil {
			failures = append(failures, e)
		}
	}
	s.remaining.Store(remaining)
	return errors.Join(failures...)
}

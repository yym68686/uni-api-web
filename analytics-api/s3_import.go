package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const maxObjectBytes = 32 << 20

func (s *Service) importS3(ctx context.Context) error {
	if s.cfg.S3Bucket == "" || s.cfg.S3Endpoint == "" {
		return errors.New("storage_not_configured")
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("auto"), config.WithHTTPClient(&http.Client{Timeout: 20 * time.Second}))
	if err != nil {
		return err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(s.cfg.S3Endpoint); o.UsePathStyle = true })
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
			if etag, ok := known[key]; ok {
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
	var failures []error
	for start := 0; start < len(pending); start += 32 {
		end := min(start+32, len(pending))
		group := pending[start:end]
		objects := make([]FactObject, len(group))
		errs := make([]error, len(group))
		var wg sync.WaitGroup
		slots := make(chan struct{}, 6)
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
				objects[i] = FactObject{Key: aws.ToString(obj.Key), ETag: aws.ToString(obj.ETag), Facts: facts}
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
	s.maybeImport(ctx)
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

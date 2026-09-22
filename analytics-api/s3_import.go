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
const factDownloadConcurrency = 4
const factBatchObjects = 64
const factBatchBytes = 4 << 20

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
	pager := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(s.cfg.S3Bucket), Prefix: aws.String(strings.Trim(s.cfg.S3Prefix, "/") + "/")})
	var failures []error
	var pages, listed int
	listStarted := time.Now()
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		pages++
		listed += len(page.Contents)
		s.listPages.Store(int64(pages))
		s.listedObjects.Store(int64(listed))
		keys := make([]string, 0, len(page.Contents))
		for _, obj := range page.Contents {
			if strings.HasSuffix(aws.ToString(obj.Key), ".jsonl") {
				keys = append(keys, sourceObjectKey(s.cfg.SourceID, aws.ToString(obj.Key)))
			}
		}
		known, err := s.engine.ImportedObjectPage(ctx, keys)
		if err != nil {
			return err
		}
		var pending []types.Object
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
		// Hash-addressed objects have no chronological cursor. Import discoveries
		// before asking for the next page, and still scan every page for late data.
		if err = s.importListedObjects(ctx, client, pending); err != nil {
			failures = append(failures, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	fmt.Printf("analytics source_scan source=%s pages=%d objects=%d duration_ms=%d failures=%d\n", s.cfg.SourceID, pages, listed, time.Since(listStarted).Milliseconds(), len(failures))
	return errors.Join(failures...)
}

func (s *Service) importListedObjects(ctx context.Context, client *s3.Client, pending []types.Object) error {
	var err error
	// Catch up with live traffic before replaying older objects. Checkpoints still
	// cover the complete listing; no high-water mark can hide a late arrival.
	sort.Slice(pending, func(i, j int) bool {
		return aws.ToTime(pending[i].LastModified).After(aws.ToTime(pending[j].LastModified))
	})
	s.remaining.Add(int64(len(pending)))
	if len(pending) > 0 {
		fmt.Printf("analytics import source=%s pending_objects=%d\n", s.cfg.SourceID, len(pending))
	}
	lastProgress := time.Now()
	var failures []error
	for start := 0; start < len(pending); {
		end := start
		var groupBytes int64
		for end < len(pending) && end-start < factBatchObjects {
			size := aws.ToInt64(pending[end].Size)
			if end > start && groupBytes+size > factBatchBytes {
				break
			}
			groupBytes += size
			end++
		}
		group := pending[start:end]
		objects := make([]FactObject, len(group))
		errs := make([]error, len(group))
		var wg sync.WaitGroup
		slots := s.factDownloadSlots
		if slots == nil {
			slots = make(chan struct{}, factDownloadConcurrency)
		}
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
				if start == 0 {
					fmt.Printf("analytics import_batch source=%s class=%s objects=%d\n", s.cfg.SourceID, importErrorClass(err), len(good))
				}
				return err
			}
			s.remaining.Add(-int64(len(good)))
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(lastProgress) >= 10*time.Second {
			fmt.Printf("analytics import source=%s remaining_objects=%d\n", s.cfg.SourceID, s.remaining.Load())
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
	started := time.Now()
	s.active.Store(1)
	defer s.active.Store(0)
	if err := s.importS3(ctx); err != nil {
		s.importFailures.Add(1)
		s.importError.Store(importErrorClass(err))
		fmt.Printf("analytics import failed stage=import_facts class=%s duration_ms=%d remaining_objects=%d\n", importErrorClass(err), time.Since(started).Milliseconds(), s.remaining.Load())
	} else {
		s.importError.Store("")
		s.lastCollect.Store(time.Now().UnixMilli())
		s.maybeCheckpoint(ctx)
	}
}
func importErrorClass(err error) string {
	if class := databaseErrorClass(err); class != "" {
		return class
	}
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
	if errors.Is(err, context.Canceled) {
		return "canceled"
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

func sourceObjectKey(source, key string) string {
	if source == "" || source == "primary" {
		return key
	}
	return source + "::" + key
}

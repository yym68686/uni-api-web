package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func (s *Service) importS3(ctx context.Context) error {
	if s.cfg.S3Bucket == "" || s.cfg.S3Endpoint == "" {
		return nil
	}
	opts := []func(*config.LoadOptions) error{config.WithRegion("auto")}
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return err
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(s.cfg.S3Endpoint); o.UsePathStyle = true })
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(s.cfg.S3Bucket), Prefix: aws.String(strings.Trim(s.cfg.S3Prefix, "/"))})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if !strings.HasSuffix(key, ".jsonl") && !strings.HasSuffix(key, ".json") {
				continue
			}
			known, err := s.engine.Imported(ctx, key, aws.ToString(obj.ETag))
			if err != nil {
				return err
			}
			if known {
				continue
			}
			resp, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.cfg.S3Bucket), Key: aws.String(key)})
			if err != nil {
				return err
			}
			if err = s.importObject(ctx, key, aws.ToString(obj.ETag), resp.Body); err != nil {
				resp.Body.Close()
				return err
			}
			_ = resp.Body.Close()
		}
	}
	return nil
}
func (s *Service) importObject(ctx context.Context, key, etag string, body io.Reader) error {
	scan := bufio.NewScanner(io.LimitReader(body, 32<<20))
	scan.Buffer(make([]byte, 64<<10), 1<<20)
	facts := []Fact{}
	for scan.Scan() {
		var f Fact
		if err := json.Unmarshal(scan.Bytes(), &f); err != nil {
			return fmt.Errorf("decode fact object %s: %w", path.Base(key), err)
		}
		facts = append(facts, f)
		if len(facts) > 10000 {
			return fmt.Errorf("fact batch exceeds 10000 events")
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	return s.engine.Import(ctx, key, etag, facts)
}

func (s *Service) maybeImport(ctx context.Context) {
	if err := s.importS3(ctx); err != nil {
		// Keep credentials, bucket names and object keys out of logs while still
		// making retryable import failures observable.
		fmt.Printf("analytics S3 import failed: %s\n", importErrorClass(err))
	} else {
		s.lastCollect.Store(time.Now().UnixMilli())
	}
}

func importErrorClass(err error) string {
	if err == nil {
		return "unknown"
	}
	switch {
	case strings.Contains(err.Error(), "AccessDenied"), strings.Contains(err.Error(), "Forbidden"):
		return "access_denied"
	case strings.Contains(err.Error(), "timeout"), strings.Contains(err.Error(), "deadline"):
		return "timeout"
	default:
		return "unavailable"
	}
}
func (s *Service) startImportLoop(ctx context.Context) {
	s.maybeImport(ctx)
	ticker := time.NewTicker(15 * time.Second)
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

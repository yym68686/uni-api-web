package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type factScanTiming struct {
	list, lookup, journal, ingest     time.Duration
	pages, objects, batches, imported int
}

// Keep at most two batches of object METADATA ahead of ingestion. Downloads
// retain the existing global four-slot bound and 64-object/4MiB batch limits.
// Flush even if the next List page is slow, so new facts remain visible.
func (s *Service) scanFactPipeline(parent context.Context, client *s3.Client) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	started := time.Now()
	var timing factScanTiming
	if s.control != nil {
		if err := s.control.ensureJournal(ctx, s.journalKey()); err != nil {
			return err
		}
	}
	pending := make(chan types.Object, 2*factBatchObjects)
	done := make(chan error, 1)
	go func() {
		defer close(pending)
		pager := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(s.cfg.S3Bucket), Prefix: aws.String(strings.Trim(s.cfg.S3Prefix, "/") + "/")})
		for pager.HasMorePages() {
			at := time.Now()
			page, err := pager.NextPage(ctx)
			timing.list += time.Since(at)
			if err != nil {
				done <- err
				return
			}
			timing.pages++
			timing.objects += len(page.Contents)
			s.listPages.Store(int64(timing.pages))
			s.listedObjects.Store(int64(timing.objects))
			var keys []string
			for _, o := range page.Contents {
				if strings.HasSuffix(aws.ToString(o.Key), ".jsonl") {
					keys = append(keys, sourceObjectKey(s.cfg.SourceID, aws.ToString(o.Key)))
				}
			}
			at = time.Now()
			known, err := s.engine.ImportedObjectPage(ctx, keys)
			timing.lookup += time.Since(at)
			if err != nil {
				done <- err
				return
			}
			for _, o := range page.Contents {
				key := aws.ToString(o.Key)
				if !strings.HasSuffix(key, ".jsonl") {
					continue
				}
				if etag, ok := known[sourceObjectKey(s.cfg.SourceID, key)]; ok {
					if etag != aws.ToString(o.ETag) {
						done <- errors.New("immutable object changed")
						return
					}
					continue
				}
				select {
				case pending <- o:
				case <-ctx.Done():
					done <- ctx.Err()
					return
				}
			}
		}
		done <- nil
	}()
	var batch []types.Object
	var size int64
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		at := time.Now()
		if s.control != nil {
			if err := s.control.appendJournal(ctx, s.journalKey(), batch); err != nil {
				return err
			}
		}
		timing.journal += time.Since(at)
		at = time.Now()
		err := s.importListedObjects(ctx, client, batch)
		timing.ingest += time.Since(at)
		timing.batches++
		timing.imported += len(batch)
		batch = nil
		size = 0
		timer.Stop()
		return err
	}
	var err error
loop:
	for {
		select {
		case o, ok := <-pending:
			if !ok {
				err = flush()
				break loop
			}
			if len(batch) > 0 && (len(batch) >= factBatchObjects || size+aws.ToInt64(o.Size) > factBatchBytes) {
				if err = flush(); err != nil {
					break loop
				}
			}
			if len(batch) == 0 {
				timer.Reset(250 * time.Millisecond)
			}
			batch = append(batch, o)
			size += aws.ToInt64(o.Size)
			if len(batch) >= factBatchObjects || size >= factBatchBytes {
				if err = flush(); err != nil {
					break loop
				}
			}
		case <-timer.C:
			if err = flush(); err != nil {
				break loop
			}
		case <-ctx.Done():
			err = ctx.Err()
			break loop
		}
	}
	cancel()
	listErr := <-done // Join producer before reading timings or returning/closing DB.
	if err == nil {
		err = listErr
	}
	if err != nil && s.control != nil && err.Error() == "immutable object changed" {
		// Invalidate fast replay until the actual source inconsistency is repaired.
		_, _ = s.control.db.ExecContext(parent, `UPDATE console_fact_journals SET error_class='immutable_object_changed' WHERE source_key=$1`, s.journalKey())
	}
	failures := 0
	if err != nil {
		failures = 1
	}
	log.Printf("analytics source_scan source=%s pages=%d objects=%d duration_ms=%d failures=%d list_ms=%d lookup_ms=%d journal_ms=%d ingest_ms=%d batches=%d discovered=%d", s.cfg.SourceID, timing.pages, timing.objects, time.Since(started).Milliseconds(), failures, timing.list.Milliseconds(), timing.lookup.Milliseconds(), timing.journal.Milliseconds(), timing.ingest.Milliseconds(), timing.batches, timing.imported)
	return err
}

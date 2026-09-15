package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type fakeStateObjects struct {
	mu        sync.Mutex
	body      []byte
	etag      string
	revision  int
	failWrite bool
	metadata  map[string]string
}

func (f *fakeStateObjects) GetObject(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.body == nil {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey"}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(append([]byte(nil), f.body...))), ETag: aws.String(f.etag), Metadata: f.metadata, ContentLength: aws.Int64(int64(len(f.body)))}, nil
}
func (f *fakeStateObjects) HeadObject(_ context.Context, _ *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.body == nil {
		return nil, &smithy.GenericAPIError{Code: "NotFound"}
	}
	return &s3.HeadObjectOutput{ETag: aws.String(f.etag), Metadata: f.metadata, ContentLength: aws.Int64(int64(len(f.body)))}, nil
}
func (f *fakeStateObjects) PutObject(_ context.Context, req *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite {
		return nil, errors.New("storage offline")
	}
	if (req.IfNoneMatch != nil && f.body != nil) || (req.IfMatch != nil && aws.ToString(req.IfMatch) != f.etag) {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
	}
	if req.IfMatch == nil && req.IfNoneMatch == nil {
		return nil, errors.New("unconditional write rejected")
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	f.body = raw
	f.metadata = req.Metadata
	f.revision++
	f.etag = fmt.Sprintf("\"state-%d\"", f.revision)
	return &s3.PutObjectOutput{ETag: aws.String(f.etag)}, nil
}

func stateTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := OpenEngine(filepath.Join(t.TempDir(), "cache.duckdb"), Config{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func TestPriceStateSurvivesCacheReplacementAndPreservesOtherModels(t *testing.T) {
	ctx := context.Background()
	objects := &fakeStateObjects{}
	a, b := stateTestEngine(t), stateTestEngine(t)
	first := newStateStore(objects, Config{StateBucket: "state", StatePrefix: "console/v1"})
	second := newStateStore(objects, Config{StateBucket: "state", StatePrefix: "console/v1"})
	if err := first.sync(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := first.save(ctx, a, Price{Model: "custom-one", Input: 3, Output: 5, Verified: true}); err != nil {
		t.Fatal(err)
	}
	if err := second.sync(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := second.save(ctx, b, Price{Model: "custom-two", Input: 7, Output: 9, Verified: true}); err != nil {
		t.Fatal(err)
	}
	if err := first.sync(ctx, a); err != nil {
		t.Fatal(err)
	}
	for _, e := range []*Engine{a, b} {
		var count int
		if err := e.DB.QueryRow("SELECT count(*) FROM prices WHERE model IN ('custom-one','custom-two')").Scan(&count); err != nil || count != 2 {
			t.Fatalf("lost setting: count=%d err=%v", count, err)
		}
	}
	// A new local database obtains operator settings from S3, not seed defaults.
	third := newStateStore(objects, Config{StateBucket: "state", StatePrefix: "console/v1"})
	c := stateTestEngine(t)
	if err := third.sync(ctx, c); err != nil {
		t.Fatal(err)
	}
	var input float64
	if err := c.DB.QueryRow("SELECT input FROM prices WHERE model='custom-one'").Scan(&input); err != nil || input != 3 {
		t.Fatal(input, err)
	}
}

func TestStalePriceWriteAndStorageFailureDoNotOverwriteCommittedState(t *testing.T) {
	ctx := context.Background()
	objects := &fakeStateObjects{}
	e := stateTestEngine(t)
	s := newStateStore(objects, Config{StateBucket: "state", StatePrefix: "console/v1"})
	if err := s.sync(ctx, e); err != nil {
		t.Fatal(err)
	}
	old, etag, _, err := s.read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.save(ctx, e, Price{Model: "operator", Input: 42, Verified: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.put(ctx, old, etag); !errors.Is(err, errPriceConflict) {
		t.Fatalf("stale update accepted: %v", err)
	}
	objects.failWrite = true
	if err = s.save(ctx, e, Price{Model: "operator", Input: 1, Verified: true}); err == nil {
		t.Fatal("failed storage write reported success")
	}
	var input float64
	if err = e.DB.QueryRow("SELECT input FROM prices WHERE model='operator'").Scan(&input); err != nil || input != 42 {
		t.Fatal(input, err)
	}
	var doc priceState
	if err = json.Unmarshal(objects.body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, p := range doc.Prices {
		if p.Model == "operator" && p.Input != 42 {
			t.Fatal("S3 setting changed after rejected write")
		}
	}
}

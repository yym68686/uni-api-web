package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

const maxPriceStateBytes = 4 << 20

var errPriceConflict = errors.New("prices changed concurrently; refresh and retry")

type stateObjectClient interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

type priceState struct {
	Schema int     `json:"schema"`
	Prices []Price `json:"prices"`
}

// S3 is authoritative for operator settings. Each replica only caches them in
// its own DuckDB file; conditional writes prevent concurrent editors from
// silently replacing a newer document during a rolling deployment.
type stateStore struct {
	client            stateObjectClient
	bucket, key, etag string
	mu                sync.Mutex
}

func newS3Client(ctx context.Context, cfg Config) (*s3.Client, error) {
	return newObjectClient(ctx, cfg.S3Endpoint, "", "", 20*time.Second)
}

func newObjectClient(ctx context.Context, endpoint, accessKey, secretKey string, timeout time.Duration) (*s3.Client, error) {
	options := []func(*config.LoadOptions) error{config.WithRegion("auto"), config.WithHTTPClient(&http.Client{Timeout: timeout})}
	if accessKey != "" || secretKey != "" {
		if accessKey == "" || secretKey == "" {
			return nil, errors.New("incomplete state credentials")
		}
		options = append(options, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")))
	}
	base, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(base, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	}), nil
}

func newStateStore(client stateObjectClient, cfg Config) *stateStore {
	return &stateStore{client: client, bucket: cfg.StateBucket, key: strings.Trim(cfg.StatePrefix, "/") + "/prices.json"}
}

func storageErrorCode(err error) string {
	var api smithy.APIError
	if errors.As(err, &api) {
		return api.ErrorCode()
	}
	return ""
}

func (s *stateStore) read(ctx context.Context) (priceState, string, bool, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key)})
	if err != nil {
		if code := storageErrorCode(err); code == "NoSuchKey" || code == "NotFound" {
			return priceState{Schema: 1}, "", false, nil
		}
		return priceState{}, "", false, err
	}
	defer out.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(out.Body, maxPriceStateBytes+1))
	if err != nil {
		return priceState{}, "", false, err
	}
	if len(raw) > maxPriceStateBytes {
		return priceState{}, "", false, errors.New("price state too large")
	}
	var doc priceState
	if err = json.Unmarshal(raw, &doc); err != nil {
		return doc, "", false, errors.New("invalid price state")
	}
	if doc.Schema != 1 || len(doc.Prices) > 4096 {
		return doc, "", false, errors.New("invalid price state schema")
	}
	seen := map[string]bool{}
	for _, p := range doc.Prices {
		if err = validatePrice(p); err != nil {
			return doc, "", false, err
		}
		if seen[p.Model] {
			return doc, "", false, errors.New("duplicate model price")
		}
		seen[p.Model] = true
	}
	etag := aws.ToString(out.ETag)
	if etag == "" {
		return doc, "", false, errors.New("price state missing ETag")
	}
	return doc, etag, true, nil
}

func (s *stateStore) put(ctx context.Context, doc priceState, etag string) (string, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	if len(raw) > maxPriceStateBytes || len(doc.Prices) > 4096 {
		return "", errors.New("price state too large")
	}
	req := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key), Body: bytes.NewReader(raw), ContentType: aws.String("application/json")}
	if etag == "" {
		req.IfNoneMatch = aws.String("*")
	} else {
		req.IfMatch = aws.String(etag)
	}
	out, err := s.client.PutObject(ctx, req)
	if err != nil {
		if code := storageErrorCode(err); code == "PreconditionFailed" || code == "ConditionalRequestConflict" {
			return "", errPriceConflict
		}
		return "", err
	}
	if aws.ToString(out.ETag) == "" {
		return "", errors.New("price write missing ETag; reload required")
	}
	return aws.ToString(out.ETag), nil
}

func (s *stateStore) sync(ctx context.Context, e *Engine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, etag, found, err := s.read(ctx)
	if err != nil {
		return err
	}
	if !found {
		doc.Prices, err = e.Prices(ctx)
		if err != nil {
			return err
		}
		etag, err = s.put(ctx, doc, "")
		if errors.Is(err, errPriceConflict) {
			doc, etag, _, err = s.read(ctx)
		}
		if err != nil {
			return err
		}
	}
	if etag == s.etag {
		return nil
	}
	if err = e.applyPrices(ctx, doc.Prices); err != nil {
		return err
	}
	s.etag = etag
	return nil
}

func (s *stateStore) save(ctx context.Context, e *Engine, p Price) error {
	if err := validatePrice(p); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, etag, found, err := s.read(ctx)
	if err != nil {
		return err
	}
	if !found {
		doc.Prices, err = e.Prices(ctx)
		if err != nil {
			return err
		}
	}
	p.EffectiveAt = time.Now().UTC()
	replaced := false
	for i := range doc.Prices {
		if doc.Prices[i].Model == p.Model {
			doc.Prices[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		doc.Prices = append(doc.Prices, p)
	}
	newETag, err := s.put(ctx, doc, etag)
	if err != nil {
		return err
	}
	if err = e.applyPrices(ctx, doc.Prices); err != nil {
		return err
	}
	s.etag = newETag
	return nil
}

func (e *Engine) applyPrices(ctx context.Context, prices []Price) error {
	for _, p := range prices {
		if err := validatePrice(p); err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM prices"); err != nil {
		return err
	}
	for _, p := range prices {
		if _, err = tx.ExecContext(ctx, "INSERT INTO prices(model,input,output,cache_read,cache_write,cache_write_1h,source,verified,effective_at,charge_cache_write) VALUES(?,?,?,?,?,?,?,?,?,?)", p.Model, p.Input, p.Output, p.CacheRead, p.CacheWrite, p.CacheWrite1h, p.Source, p.Verified, p.EffectiveAt, p.ChargeCacheWrite); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	e.Revision.Add(1)
	return nil
}

func (s *Service) syncStateLoop(ctx context.Context) {
	if s.state == nil {
		return
	}
	sync := func() {
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := s.state.sync(callCtx, s.engine); err != nil {
			s.stateError.Store("state_unavailable")
		} else {
			s.stateError.Store("")
			s.stateReady.Store(true)
		}
	}
	sync()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sync()
		}
	}
}

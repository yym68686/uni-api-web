package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// The HTTP client and each complete download/apply attempt share this bound.
// Retries also cover interrupted response bodies, which SDK request retries do
// not cover. Permanent failures go straight to rebuilding from immutable facts.
var defaultCheckpointRestorePolicy = checkpointRestorePolicy{attempts: 3, timeout: 5 * time.Minute, backoff: 2 * time.Second}

type checkpointRestorePolicy struct {
	attempts int
	timeout  time.Duration
	backoff  time.Duration
}

type checkpointError struct {
	stage string
	err   error
}

func (e *checkpointError) Error() string { return e.stage + ": " + e.err.Error() }
func (e *checkpointError) Unwrap() error { return e.err }
func checkpointStageError(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &checkpointError{stage: stage, err: err}
}

type checkpointErrorDetail struct {
	Stage     string `json:"stage"`
	Class     string `json:"class"`
	Retryable bool   `json:"retryable"`
}

// Never log SDK/database error strings: they can contain signed storage URLs,
// credentials, object contents or local paths. Only controlled classifications
// are exposed, together with the stage that actually failed.
func checkpointFailure(err error) checkpointErrorDetail {
	d := checkpointErrorDetail{Stage: "unknown", Class: "unavailable"}
	var staged *checkpointError
	if errors.As(err, &staged) {
		d.Stage = staged.stage
	}
	var transport net.Error
	var response interface{ HTTPStatusCode() int }
	switch {
	case errors.Is(err, context.Canceled):
		d.Class = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		d.Class, d.Retryable = "timeout", true
	case errors.As(err, &transport):
		d.Class, d.Retryable = "transport", true
		if transport.Timeout() {
			d.Class = "timeout"
		}
	case d.Stage == "download" && (errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)):
		d.Class, d.Retryable = "truncated_download", true
	default:
		switch storageErrorCode(err) {
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "ExpiredToken":
			d.Class = "access_denied"
		case "NoSuchBucket":
			d.Class = "bucket_missing"
		case "SlowDown", "Throttling", "ThrottlingException", "RequestTimeout", "InternalError", "ServiceUnavailable":
			d.Class, d.Retryable = "storage_transient", true
		default:
			if errors.As(err, &response) {
				status := response.HTTPStatusCode()
				d.Class, d.Retryable = "storage_http", status == 408 || status == 429 || status >= 500
			} else {
				switch d.Stage {
				case "metadata", "integrity", "unpack":
					d.Class = "invalid_checkpoint"
				case "inspect_cache", "apply", "export":
					d.Class = "database"
				case "local_storage", "pack":
					d.Class = "local_storage"
				}
			}
		}
	}
	return d
}

func logCheckpoint(operation string, attempt, limit int, started time.Time, detail checkpointErrorDetail, retry bool) {
	log.Printf("analytics checkpoint operation=%s stage=%s class=%s attempt=%d/%d duration_ms=%d retry=%t", operation, detail.Stage, detail.Class, attempt, limit, time.Since(started).Milliseconds(), retry)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

type analyticsStartup struct {
	Phase          string                 `json:"phase"`
	StartedMS      int64                  `json:"started_ms"`
	Attempt        int                    `json:"attempt,omitempty"`
	MaxAttempts    int                    `json:"max_attempts,omitempty"`
	LastError      *checkpointErrorDetail `json:"last_error,omitempty"`
	RestoreOutcome string                 `json:"restore_outcome,omitempty"`
}

func (s *Service) restoreCheckpoint(ctx context.Context, policy checkpointRestorePolicy) string {
	if s.checkpoints == nil {
		return "not_configured"
	}
	startup := analyticsStartup{Phase: "restoring_checkpoint", StartedMS: time.Now().UnixMilli(), MaxAttempts: policy.attempts}
	for attempt := 1; attempt <= policy.attempts && ctx.Err() == nil; attempt++ {
		startup.Attempt = attempt
		snapshot := startup
		s.startup.Store(&snapshot)
		started := time.Now()
		log.Printf("analytics checkpoint operation=restore stage=start attempt=%d/%d timeout_ms=%d", attempt, policy.attempts, policy.timeout.Milliseconds())
		callCtx, cancel := context.WithTimeout(ctx, policy.timeout)
		restored, err := s.checkpoints.restore(callCtx, s.engine)
		cancel()
		if err == nil {
			outcome := "not_needed"
			if restored {
				outcome = "restored"
			}
			log.Printf("analytics checkpoint operation=restore outcome=%s attempt=%d/%d duration_ms=%d", outcome, attempt, policy.attempts, time.Since(started).Milliseconds())
			s.checkpointError.Store("")
			return outcome
		}
		detail := checkpointFailure(err)
		retry := detail.Retryable && attempt < policy.attempts && ctx.Err() == nil
		logCheckpoint("restore", attempt, policy.attempts, started, detail, retry)
		s.checkpointError.Store(detail.Stage + ":" + detail.Class)
		startup.LastError = &detail
		snapshot = startup
		s.startup.Store(&snapshot)
		if !retry {
			break
		}
		if !waitStartup(ctx, policy.backoff*time.Duration(1<<(attempt-1))) {
			return "canceled"
		}
	}
	if ctx.Err() != nil {
		return "canceled"
	}
	log.Print("analytics checkpoint operation=restore outcome=rebuild_from_facts")
	return "rebuild_from_facts"
}

func waitStartup(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

// Start control recovery and detection independently of analytics. History is
// gated at its own endpoints until one complete import and price sync succeed.
// Only this history goroutine may restore/import, preventing a late restore
// from overwriting freshly imported rows. Shutdown drains all writers before
// either database is closed.
func (s *Service) startBackground(ctx context.Context) <-chan struct{} {
	s.historyLoading.Store(true)
	s.startup.Store(&analyticsStartup{Phase: "starting", StartedMS: time.Now().UnixMilli()})
	done := make(chan struct{})
	var workers sync.WaitGroup
	start := func(fn func(context.Context)) {
		workers.Add(1)
		go func() { defer workers.Done(); fn(ctx) }()
	}
	if s.control != nil {
		start(s.controlRecoveryLoop)
		start(s.subWorkerLoop)
	}
	start(s.syncStateLoop)
	start(s.initializeAnalytics)
	go func() {
		workers.Wait()
		// All callers of maybeCheckpoint have stopped before this Wait.
		s.checkpointWorkers.Wait()
		close(done)
	}()
	return done
}

func (s *Service) initializeAnalytics(ctx context.Context) {
	started := time.Now()
	outcome := s.restoreCheckpoint(ctx, defaultCheckpointRestorePolicy)
	startup := analyticsStartup{Phase: "importing_facts", StartedMS: time.Now().UnixMilli(), RestoreOutcome: outcome}
	if previous := s.startup.Load(); previous != nil {
		startup.LastError = previous.LastError
	}
	s.startup.Store(&startup)
	for ctx.Err() == nil {
		s.maybeImport(ctx)
		if s.lastCollect.Load() > 0 {
			break
		}
		if !waitStartup(ctx, 5*time.Second) {
			return
		}
	}
	// Settings synchronize independently and can recover while a slow history
	// download is in progress. Do not publish statistics with default prices.
	if ctx.Err() != nil {
		return
	}
	s.startup.Store(&analyticsStartup{Phase: "waiting_for_settings", StartedMS: time.Now().UnixMilli(), RestoreOutcome: outcome, LastError: startup.LastError})
	for s.state != nil && !s.stateReady.Load() {
		if !waitStartup(ctx, time.Second) {
			return
		}
	}
	s.historyLoading.Store(false)
	s.startup.Store(&analyticsStartup{Phase: "ready", StartedMS: time.Now().UnixMilli(), RestoreOutcome: outcome, LastError: startup.LastError})
	log.Printf("analytics history ready duration_ms=%d restore_outcome=%s", time.Since(started).Milliseconds(), outcome)
	// Warming is optional and no longer part of deployment or worker readiness.
	warmDone := make(chan struct{})
	go func() { defer close(warmDone); s.warmAnalytics(ctx) }()
	s.startImportLoop(ctx)
	<-warmDone
}

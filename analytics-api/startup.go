package main

import (
	"context"
	"sync"
	"time"
)

type analyticsStartup struct {
	Phase     string `json:"phase"`
	StartedMS int64  `json:"started_ms"`
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
func (s *Service) startBackground(ctx context.Context) <-chan struct{} {
	s.startup.Store(&analyticsStartup{Phase: "ready", StartedMS: time.Now().UnixMilli()})
	done := make(chan struct{})
	var workers sync.WaitGroup
	start := func(fn func(context.Context)) { workers.Add(1); go func() { defer workers.Done(); fn(ctx) }() }
	if s.control != nil {
		start(s.subModelSummaryBackfillLoop)
		start(s.automationLoop)
		start(s.controlRecoveryLoop)
		start(s.subWorkerLoop)
		start(s.configuredCheckLoop)
		start(s.fingerprintLoop)
		start(s.subUsageLoop)
		start(s.subSpendLoop)
		start(s.subBindingLoop)
	}
	start(s.retentionLoop)
	go func() { workers.Wait(); close(done) }()
	return done
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errAnalyticsPriority = errors.New("billing deferred for interactive analytics")

func (e *Engine) acquireAnalytical(ctx context.Context, name string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A few helper-only Engine values used by tests and migration code do not
	// open a database. Keep those callers usable without turning a nil channel
	// send into an unbounded block; normal engines always initialize the gate in
	// OpenEngine.
	if e.analyticalSlots == nil {
		return func() {}, nil
	}
	// Per-request billing reconciliation is optional and can scan a large
	// receipt set. A critical analytics read must be able to reclaim the single
	// analytical slot from that work instead of waiting until its 20s deadline.
	leavePriority := func() {}
	if name == "analytics_query" {
		e.spendMu.Lock()
		e.criticalReads++
		cancels := make([]context.CancelCauseFunc, 0, len(e.spendCancels))
		for _, cancel := range e.spendCancels {
			cancels = append(cancels, cancel)
		}
		e.spendMu.Unlock()
		for _, cancel := range cancels {
			cancel(errAnalyticsPriority)
		}
		if len(cancels) > 0 {
			log.Printf("analytics billing_deferred reason=interactive_analytics requests=%d", len(cancels))
		}
		leavePriority = func() {
			e.spendMu.Lock()
			e.criticalReads--
			e.spendMu.Unlock()
		}
	}
	e.analyticalWaiting.Add(1)
	defer e.analyticalWaiting.Add(-1)
	began := time.Now()
	select {
	case e.analyticalSlots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-e.analyticalSlots
			leavePriority()
			return nil, err
		}
		if elapsed := time.Since(began); elapsed >= 100*time.Millisecond {
			log.Printf("analytics db_wait operation=%s duration_ms=%d request_id=%s", name, elapsed.Milliseconds(), consoleRequestID(ctx))
		}
		var once sync.Once
		return func() {
			once.Do(func() {
				<-e.analyticalSlots
				leavePriority()
			})
		}, nil
	case <-ctx.Done():
		leavePriority()
		log.Printf("analytics db_wait operation=%s duration_ms=%d request_id=%s class=%s", name, time.Since(began).Milliseconds(), consoleRequestID(ctx), databaseErrorClass(ctx.Err()))
		return nil, ctx.Err()
	}
}

// Registration shares the priority lock: new billing must also yield while
// an interactive reader is queued or running. Billing never cancels its peers.
func (e *Engine) registerSpend(ctx context.Context) (context.Context, func()) {
	workCtx, cancel := context.WithCancelCause(ctx)
	e.spendMu.Lock()
	e.nextSpendID++
	id := e.nextSpendID
	if e.spendCancels == nil {
		e.spendCancels = make(map[uint64]context.CancelCauseFunc)
	}
	e.spendCancels[id] = cancel
	deferred := e.criticalReads > 0
	e.spendMu.Unlock()
	if deferred {
		cancel(errAnalyticsPriority)
	}
	return workCtx, func() {
		cancel(nil)
		e.spendMu.Lock()
		delete(e.spendCancels, id)
		e.spendMu.Unlock()
	}
}

type databaseOperation struct {
	engine *Engine
	name   string
	phase  string
	began  time.Time
	last   time.Time
	stages []string
	ctx    context.Context
}

func (e *Engine) observeOperation(name string) *databaseOperation {
	return e.observeRequestOperation(context.Background(), name)
}

func (e *Engine) observeRequestOperation(ctx context.Context, name string) *databaseOperation {
	now := time.Now()
	o := &databaseOperation{engine: e, name: name, phase: "start", began: now, last: now, ctx: ctx}
	o.stage("start")
	return o
}

func (o *databaseOperation) stage(phase string) {
	o.engine.operationsMu.Lock()
	defer o.engine.operationsMu.Unlock()
	if o.engine.operations == nil {
		o.engine.operations = make(map[*databaseOperation]string)
	}
	now := time.Now()
	if o.phase != phase {
		o.stages = append(o.stages, fmt.Sprintf("%s_ms=%d", o.phase, now.Sub(o.last).Milliseconds()))
	}
	o.phase, o.last = phase, now
	o.engine.operations[o] = o.name + "/" + phase
}

func (o *databaseOperation) finish(err error) {
	o.engine.operationsMu.Lock()
	delete(o.engine.operations, o)
	o.engine.operationsMu.Unlock()
	class := databaseErrorClass(err)
	if err != nil && o.ctx.Err() != nil {
		class = databaseErrorClass(o.ctx.Err())
	}
	if err != nil && class == "" {
		class = "operation_failed"
	}
	if err != nil || time.Since(o.began) >= time.Second {
		// Never log SQL, source credentials, object names or driver error text.
		stages := append(o.stages, fmt.Sprintf("%s_ms=%d", o.phase, time.Since(o.last).Milliseconds()))
		log.Printf("analytics db_operation operation=%s stage=%s duration_ms=%d class=%s request_id=%s %s", o.name, o.phase, time.Since(o.began).Milliseconds(), class, consoleRequestID(o.ctx), strings.Join(stages, " "))
	}
	if class == "database_out_of_memory" {
		o.engine.logMemory("failure_after_cleanup")
	}
}

type databaseMemorySample struct {
	Event          string           `json:"event"`
	Reason         string           `json:"reason"`
	Active         map[string]int   `json:"active"`
	Pool           sql.DBStats      `json:"pool"`
	QueriesWaiting int64            `json:"queries_waiting"`
	GoHeap         uint64           `json:"go_heap_bytes"`
	Process        map[string]int64 `json:"process_bytes,omitempty"`
	Cgroup         map[string]int64 `json:"cgroup_bytes,omitempty"`
	SampleError    string           `json:"sample_error,omitempty"`
}

// Linux proc/cgroup counters are read independently; they are observations, not
// an atomic peak breakdown. RSS describes the API process; database memory belongs to PostgreSQL.
func processMemory() map[string]int64 {
	out := map[string]int64{}
	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && (fields[0] == "VmRSS:" || fields[0] == "VmHWM:") {
				n, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					out[strings.TrimSuffix(fields[0], ":")] = n * 1024
				}
			}
		}
	}
	return out
}

func cgroupMemory() map[string]int64 {
	out := map[string]int64{}
	for _, name := range []string{"memory.current", "memory.peak"} {
		if data, err := os.ReadFile("/sys/fs/cgroup/" + name); err == nil {
			if n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
				out[name] = n
			}
		}
	}
	if data, err := os.ReadFile("/sys/fs/cgroup/memory.stat"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && (fields[0] == "anon" || fields[0] == "file" || fields[0] == "kernel" || fields[0] == "file_dirty") {
				if n, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					out[fields[0]] = n
				}
			}
		}
	}
	return out
}

func (e *Engine) logMemory(reason string) {
	sample := databaseMemorySample{Event: "analytics_memory", Reason: reason, Pool: e.DB.Stats(), Active: map[string]int{}, QueriesWaiting: e.analyticalWaiting.Load(), Process: processMemory(), Cgroup: cgroupMemory()}
	e.operationsMu.Lock()
	for _, phase := range e.operations {
		sample.Active[phase]++
	}
	e.operationsMu.Unlock()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	sample.GoHeap = m.HeapAlloc
	if data, err := json.Marshal(sample); err == nil {
		log.Print(string(data))
	}
}

func (e *Engine) startMemoryDiagnostics(parent context.Context, interval time.Duration) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.logMemory("periodic")
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(cancel); <-done }
}

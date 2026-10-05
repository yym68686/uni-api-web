package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

func configureDatabaseResources(db *sql.DB, path string, cfg Config) error {
	threads := cfg.DatabaseThreads
	if threads == 0 {
		threads = 2
	}
	if threads < 1 || cfg.DatabaseTempLimitMB < 0 {
		return fmt.Errorf("invalid database resource limits")
	}
	dir := cfg.DatabaseTempDir
	if dir == "" {
		dir = path + ".tmp"
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// These settings are database-wide, not a budget multiplied by each connection.
	if _, err = db.Exec(fmt.Sprintf("SET threads=%d; SET temp_directory=%s", threads, sqlPath(dir))); err != nil {
		return err
	}
	// Zero preserves DuckDB's disk-dependent default until a measured limit is set.
	if cfg.DatabaseTempLimitMB > 0 {
		_, err = db.Exec(fmt.Sprintf("SET max_temp_directory_size='%dMiB'", cfg.DatabaseTempLimitMB))
	}
	return err
}

func (e *Engine) acquireAnalytical(ctx context.Context, name string) (func(), error) {
	// A few helper-only Engine values used by tests and migration code do not
	// open a database. Keep those callers usable without turning a nil channel
	// send into an unbounded block; normal engines always initialize the gate in
	// OpenEngine.
	if e.analyticalSlots == nil {
		return func() {}, nil
	}
	e.analyticalWaiting.Add(1)
	defer e.analyticalWaiting.Add(-1)
	began := time.Now()
	select {
	case e.analyticalSlots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-e.analyticalSlots
			return nil, err
		}
		if elapsed := time.Since(began); elapsed >= 100*time.Millisecond {
			log.Printf("analytics db_wait operation=%s duration_ms=%d", name, elapsed.Milliseconds())
		}
		return func() { <-e.analyticalSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type databaseOperation struct {
	engine *Engine
	name   string
	phase  string
	began  time.Time
}

func (e *Engine) observeOperation(name string) *databaseOperation {
	o := &databaseOperation{engine: e, name: name, phase: "start", began: time.Now()}
	o.stage("start")
	return o
}

func (o *databaseOperation) stage(phase string) {
	o.engine.operationsMu.Lock()
	defer o.engine.operationsMu.Unlock()
	if o.engine.operations == nil {
		o.engine.operations = make(map[*databaseOperation]string)
	}
	o.phase = phase
	o.engine.operations[o] = o.name + "/" + phase
}

func (o *databaseOperation) finish(err error) {
	o.engine.operationsMu.Lock()
	delete(o.engine.operations, o)
	o.engine.operationsMu.Unlock()
	class := databaseErrorClass(err)
	if err != nil && class == "" {
		class = "operation_failed"
	}
	if err != nil || time.Since(o.began) >= time.Second {
		// Never log SQL, source credentials, object names or driver error text.
		log.Printf("analytics db_operation operation=%s stage=%s duration_ms=%d class=%s", o.name, o.phase, time.Since(o.began).Milliseconds(), class)
	}
	if class == "database_out_of_memory" {
		o.engine.logMemory("failure_after_cleanup")
	}
}

type databaseMemorySample struct {
	Event          string              `json:"event"`
	Reason         string              `json:"reason"`
	Active         map[string]int      `json:"active"`
	Pool           sql.DBStats         `json:"pool"`
	QueriesWaiting int64               `json:"queries_waiting"`
	GoHeap         uint64              `json:"go_heap_bytes"`
	Process        map[string]int64    `json:"process_bytes,omitempty"`
	Cgroup         map[string]int64    `json:"cgroup_bytes,omitempty"`
	DuckDB         map[string][2]int64 `json:"duckdb_bytes,omitempty"`
	SampleError    string              `json:"sample_error,omitempty"`
}

// Linux proc/cgroup counters are read independently; they are observations, not
// an atomic peak breakdown. RSS is distinct from DuckDB buffers and file cache.
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
	// Sampling must never queue behind work for more than 200 ms, or hold a
	// transaction/lock on the writer. Failure itself is useful evidence.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rows, err := e.DB.QueryContext(ctx, "SELECT tag,memory_usage_bytes,temporary_storage_bytes FROM duckdb_memory() WHERE memory_usage_bytes>0 OR temporary_storage_bytes>0")
	if err == nil {
		sample.DuckDB = map[string][2]int64{}
		for rows.Next() {
			var tag string
			var memory, temporary int64
			if err = rows.Scan(&tag, &memory, &temporary); err != nil {
				break
			}
			sample.DuckDB[tag] = [2]int64{memory, temporary}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
	}
	if err != nil {
		sample.SampleError = "database_sample_unavailable"
	}
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

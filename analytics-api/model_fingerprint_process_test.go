package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestSharedFingerprintProcessFiftyCollectorsCancelIsolationAndCrash(t *testing.T) {
	p := &fingerprintProcess{}
	defer p.close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	arrived, release := make(chan string, 50), make(chan struct{})
	var mu sync.Mutex
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		key := r.Header.Get("Authorization")
		mu.Lock()
		calls[key]++
		n := calls[key]
		mu.Unlock()
		if n == 1 {
			arrived <- key
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		writeJSON(w, 200, map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "1 2 3"}}}}})
	}))
	defer server.Close()
	type completion struct {
		id  int
		err error
	}
	results := make(chan completion, 50)
	var cancelOne context.CancelFunc
	for i := range 50 {
		jobCtx := ctx
		if i == 0 {
			jobCtx, cancelOne = context.WithCancel(ctx)
		}
		go func() {
			_, err := p.detect(jobCtx, map[string]any{"base_url": server.URL, "key": fmt.Sprintf("job-%d", i), "model": checkModel})
			results <- completion{i, err}
		}()
	}
	for range 50 {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal("50 collectors did not overlap")
		}
	}
	w, err := p.get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pid := w.cmd.Process.Pid
	cancelOne()
	select {
	case result := <-results:
		if result.id != 0 || result.err == nil {
			t.Fatal("cancel did not stop only selected job", result)
		}
	case <-ctx.Done():
		t.Fatal("cancel did not return")
	}
	close(release)
	for range 49 {
		select {
		case result := <-results:
			if result.id == 0 || result.err != nil {
				t.Fatal("cancel affected another collector", result)
			}
		case <-ctx.Done():
			t.Fatal("remaining collectors stalled")
		}
	}
	w, err = p.get(ctx)
	if err != nil || w.cmd.Process.Pid != pid {
		t.Fatal("collectors did not share one process", err)
	}
	mu.Lock()
	for i := range 50 {
		want := 3
		if i == 0 {
			want = 1
		}
		if calls[fmt.Sprintf("Bearer job-%d", i)] != want {
			t.Errorf("job %d sent %d calls, want %d", i, calls[fmt.Sprintf("Bearer job-%d", i)], want)
		}
	}
	mu.Unlock()
	// Crash during a blocked probe must fail exactly that run without replay.
	crashArrived := make(chan struct{}, 1)
	crashServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		crashArrived <- struct{}{}
		<-r.Context().Done()
	}))
	defer crashServer.Close()
	go func() {
		_, err := p.detect(ctx, map[string]any{"base_url": crashServer.URL, "key": "crash", "model": checkModel})
		results <- completion{51, err}
	}()
	select {
	case <-crashArrived:
	case <-ctx.Done():
		t.Fatal("crash job never started")
	}
	if err = w.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-results:
		if result.err == nil {
			t.Fatal("crashed worker reported success")
		}
	case <-ctx.Done():
		t.Fatal("crash did not finish job")
	}
	<-w.done
	newWorker, err := p.get(ctx)
	if err != nil || newWorker.cmd.Process.Pid == pid {
		t.Fatal("future job cannot start fresh worker", err)
	}
	raw, err := p.detect(ctx, map[string]any{"base_url": server.URL, "key": "after-crash", "model": checkModel})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Status != "insufficient" {
		t.Fatalf("unexpected new result %s", raw)
	}
	select {
	case <-crashArrived:
		t.Fatal("crashed request replayed")
	default:
	}
}

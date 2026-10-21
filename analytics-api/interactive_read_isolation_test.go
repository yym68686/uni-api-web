package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// A physical checkpoint holds the history-writer mutex through the entire
// file copy. Neither interactive statistics nor independent price settings
// may wait for that copy. Keep the lock held until the assertions finish.
func TestInteractiveReadsAndSettingsDoNotWaitForHistoryCopy(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	fact := Fact{Schema: 1, EventID: "lock-isolation", Kind: "request", AtMS: time.Now().Add(-time.Second).UnixMilli(), Provider: "p", Model: "custom", UpstreamModel: "custom", Outcome: "success"}
	if err := e.Import(ctx, "isolation", "v1", []Fact{fact}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	locked := true
	defer func() {
		if locked {
			e.mu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() {
		readCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		result, err := e.Query(readCtx, QueryFilter{Range: "1h"})
		if err != nil {
			done <- err
			return
		}
		if result.Total["requests"] != float64(1) {
			done <- fmt.Errorf("unexpected request count: %#v", result.Total["requests"])
			return
		}
		if err := e.SavePrice(readCtx, Price{Model: "custom", Input: 7, Verified: true}); err != nil {
			done <- err
			return
		}
		prices, err := e.Prices(readCtx)
		if err != nil {
			done <- err
			return
		}
		found := false
		for _, p := range prices {
			if p.Model == "custom" && p.Input == 7 {
				found = true
			}
		}
		if !found {
			done <- fmt.Errorf("saved price missing: %#v", prices)
			return
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(1500 * time.Millisecond):
		e.mu.Unlock()
		locked = false
		<-done
		t.Fatal("interactive read/settings blocked on history copy mutex")
	}
}

func TestSettingsWaitForCatalogReplacementOnly(t *testing.T) {
	e := stateTestEngine(t)
	ctx := context.Background()
	e.historyCatalogMu.Lock()
	locked := true
	defer func() {
		if locked {
			e.historyCatalogMu.Unlock()
		}
	}()
	if _, err := e.DB.ExecContext(ctx, "DETACH history"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 3)
	go func() { done <- e.SavePrice(ctx, Price{Model: "replacement", Input: 7}) }()
	go func() { done <- e.applyPrices(ctx, []Price{{Model: "replacement", Input: 7}}) }()
	go func() { _, err := e.readPrices(ctx, false); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("settings entered detached catalog: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := e.DB.ExecContext(ctx, "ATTACH "+sqlPath(e.historyPath)+" AS history"); err != nil {
		t.Fatal(err)
	}
	e.historyCatalogMu.Unlock()
	locked = false
	for i := 0; i < 3; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

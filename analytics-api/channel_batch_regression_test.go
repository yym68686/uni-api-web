package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Production shape: the first caller already retained a failed Luna route;
// the other callers only add verified Sol. Retention is local to an exact pair.
func TestBatchSolUsesSiteGroupEvidenceWithoutSpreadingFailedSavedLuna(t *testing.T) {
	s, account, src := bindingFixture(t, "https://upstream.test")
	ctx := context.Background()
	p := configuredProvider{Provider: "stable", Base: "https://upstream.test/v1/responses", API: "fixture-key"}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("eligibility must not modify a route")
		}
		switch r.URL.Path {
		case "/v1/channel-settings/providers":
			writeJSON(w, 200, map[string]any{"providers": []configuredProvider{p}})
		case "/v1/model-channels":
			writeJSON(w, 200, map[string]any{"data": []batchCatalogRow{{Provider: "stable", Model: "gpt-5.4", Upstream: "gpt-5.5"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	src.Base = gateway.URL
	_, err := s.control.db.Exec(`INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES($1,3,'stable','openai')`, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.control.db.Exec(`INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id) VALUES($1,$2,42,3)`, account.ID, tokenHash("fixture-key"))
	if err != nil {
		t.Fatal(err)
	}
	for model, status := range map[string]string{"gpt-6-sol": "success", "gpt-6-luna": "error"} {
		_, err = s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state,result) VALUES($1,3,$2,'done',$3)`, account.ID, model, mustJSON(subResult{Model: model, CheckedAt: 10, Availability: subProbe{Status: status}}))
		if err != nil {
			t.Fatal(err)
		}
	}
	current := map[string]string{"gpt-5.4": "gpt-5.5"}
	wanted := map[string]string{"gpt-5.4": "gpt-5.5", "gpt-6-sol": "gpt-6-sol", "gpt-6-luna": "gpt-6-luna"}
	err = s.validateConfiguredModelChanges(ctx, src, p.Provider, account.Owner, current, wanted)
	if err == nil || !strings.Contains(err.Error(), "未通过：gpt-6-luna") || strings.Contains(err.Error(), "gpt-6-sol") {
		t.Fatal("must identify only the actual rejected model", err)
	}
	delete(wanted, "gpt-6-luna")
	if err = s.validateConfiguredModelChanges(ctx, src, p.Provider, account.Owner, current, wanted); err != nil {
		t.Fatal("verified Sol rejected", err)
	}
	current["gpt-6-luna"] = "gpt-6-luna"
	wanted["gpt-6-luna"] = "gpt-6-luna"
	if err = s.validateConfiguredModelChanges(ctx, src, p.Provider, account.Owner, current, wanted); err != nil {
		t.Fatal("existing failed pair must remain retainable", err)
	}
	if err = s.validateSiteModelChanges(ctx, account.ID, 3, nil, map[string]string{"gpt-6-sol": "gpt-6-sol"}); err != nil {
		t.Fatal(err)
	}
	if err = s.validateSiteModelChanges(ctx, account.ID, 3, nil, map[string]string{"gpt-6-luna": "gpt-6-luna", "gpt-6-sol": "gpt-6-sol"}); err == nil || !strings.Contains(err.Error(), "未通过：gpt-6-luna") {
		t.Fatal(err)
	}
}

func TestBatchWaitsForControlLockWithoutStarvingOwnerAndHonorsCancellation(t *testing.T) {
	s, _, src := bindingFixture(t, "https://upstream.test")
	// The existing control pool has eight slots. A lock owner still needs a
	// spare slot to reconcile/save while more than eight batches are waiting.
	unlock, err := s.control.lockControls(context.Background(), src.ID)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan error, 10)
	for range 10 {
		go func() {
			release, e := s.control.waitControls(ctx, src.ID)
			if e == nil {
				release()
			}
			results <- e
		}()
	}
	probe, stopProbe := context.WithTimeout(ctx, time.Second)
	if err = s.control.db.PingContext(probe); err != nil {
		t.Fatal("waiting batches starved owner", err)
	}
	stopProbe()
	select {
	case e := <-results:
		t.Fatal("waiter acquired locked source", e)
	case <-time.After(150 * time.Millisecond):
	}
	unlock()
	released = true
	for range 10 {
		if err = <-results; err != nil {
			t.Fatal(err)
		}
	}
	unlock, err = s.control.lockControls(ctx, src.ID)
	if err != nil {
		t.Fatal("leaked advisory lock", err)
	}
	released = false
	short, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if release, e := s.control.waitControls(short, src.ID); !errors.Is(e, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatal("wait did not honor timeout", e)
	}
	unlock()
	released = true
}

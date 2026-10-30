package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fingerprintAccumulationFixture(t *testing.T, probe http.HandlerFunc) (*Service, subBindingAccount, fingerprintTarget) {
	t.Helper()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/observability/runtime":
			writeJSON(w, 200, map[string]any{"capabilities": map[string]bool{"targeted_responses": true}})
		case "/v1/channel-settings/providers":
			writeJSON(w, 200, map[string]any{"providers": []configuredProvider{{Provider: "native", Base: "https://upstream.example/v1/responses", API: "upstream-secret"}}})
		case "/v1/responses":
			_, _ = io.Copy(io.Discard, r.Body)
			if r.Header.Get("X-Uni-API-Provider") != "native" {
				t.Error("resume lost channel targeting")
			}
			probe(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(gateway.Close)
	s, account, source := bindingFixture(t, "https://site.example")
	source.Base = gateway.URL
	if _, err := s.control.saveSource(context.Background(), source, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.control.db.Exec(`DELETE FROM console_fingerprint_site_leases`)
		s.control.db.Exec(`UPDATE console_fingerprint_worker SET deadline=NULL WHERE id=true`)
	})
	return s, account, fingerprintTarget{Source: source.ID, Provider: "native", Model: checkModel}
}

func fingerprintFixtureTexts(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("fingerprint/reference-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string][]struct{ Text string }
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, sample := range fixture["gpt-6-astra"] {
		out = append(out, sample.Text)
	}
	return out
}

func writeFingerprintSample(w http.ResponseWriter, text string) {
	writeJSON(w, 200, map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}}})
}

func fingerprintSavedCount(t *testing.T, s *Service, target fingerprintTarget) (int, int) {
	t.Helper()
	var count, retained int
	if err := s.control.db.QueryRow(`SELECT COALESCE((result->>'used_outputs')::integer,0),COALESCE(jsonb_array_length(sample_state->'entries'),0) FROM console_model_fingerprints WHERE scope=$1 AND model=$2`, target.scope(), target.Model).Scan(&count, &retained); err != nil {
		t.Fatal(err)
	}
	return count, retained
}

func TestModelFingerprintAccumulatesAcrossRunsAndResetsOnlyCompletedRound(t *testing.T) {
	texts := fingerprintFixtureTexts(t)
	var round, calls atomic.Int32
	s, account, target := fingerprintAccumulationFixture(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 && round.Load() < 3 {
			writeFingerprintSample(w, texts[round.Load()])
		} else {
			http.Error(w, "private-upstream-error", http.StatusTooManyRequests)
		}
	})
	for n, wantCalls := range []int32{3, 2, 1, 3} {
		round.Store(int32(n))
		calls.Store(0)
		for range 2 { // duplicate submissions must not reset a queued round
			if w := fingerprintRequest(t, s, account.Owner, "POST", []fingerprintTarget{target}); w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
		}
		before, retained := fingerprintSavedCount(t, s, target)
		if n > 0 && n < 3 && (before != n || retained != n) || n == 3 && (before != 0 || retained != 0) {
			t.Fatal("enqueue discarded an incomplete round or retained a complete one", n, before, retained)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		ok := s.fingerprintOne(ctx)
		cancel()
		if !ok || calls.Load() != wantCalls {
			t.Fatal("wrong number of missing sample requests", n, calls.Load(), wantCalls)
		}
		count, stored := fingerprintSavedCount(t, s, target)
		want := n + 1
		if n == 3 {
			want = 0
		}
		if count != want || stored != want {
			t.Fatal("valid samples did not accumulate durably", n, count, stored)
		}
		w := fingerprintRequest(t, s, account.Owner, "GET", nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, private := range []string{"_resume", `"numbers"`, "sample_state", "upstream-secret", "private-upstream-error"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("public fingerprint read leaked private evidence", private)
			}
		}
		if n == 0 {
			sharedFingerprintWorker.close() // a new runtime must load DB evidence
		}
	}
}

func TestModelFingerprintCancellationAndDuplicateClickKeepCheckpoint(t *testing.T) {
	texts := fingerprintFixtureTexts(t)
	second := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	s, account, target := fingerprintAccumulationFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeFingerprintSample(w, texts[0])
			return
		}
		second <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	if err := enqueueFingerprint(context.Background(), s.control.db, target); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan bool, 1)
	go func() { done <- s.fingerprintOne(ctx) }()
	select {
	case <-second:
	case <-ctx.Done():
		t.Fatal("second request not reached")
	}
	for _, method := range []string{"POST", "DELETE"} {
		if w := fingerprintRequest(t, s, account.Owner, method, []fingerprintTarget{target}); w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		if count, stored := fingerprintSavedCount(t, s, target); count != 1 || stored != 1 {
			t.Fatal("duplicate or cancellation discarded checkpoint", method, count, stored)
		}
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("cancellation did not finish")
	}
	if calls.Load() != 2 {
		t.Fatal("cancelled job issued additional paid request")
	}
	if count, stored := fingerprintSavedCount(t, s, target); count != 1 || stored != 1 {
		t.Fatal("late finish erased checkpoint", count, stored)
	}
}

func TestModelFingerprintResumeRequiresSameChannelProtocolAndReference(t *testing.T) {
	texts := fingerprintFixtureTexts(t)
	s, _, target := fingerprintAccumulationFixture(t, func(w http.ResponseWriter, r *http.Request) { writeFingerprintSample(w, texts[0]) })
	ctx := context.Background()
	if err := enqueueFingerprint(ctx, s.control.db, target); err != nil {
		t.Fatal(err)
	}
	_, run, err := s.claimFingerprint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.prepareFingerprint(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.bindFingerprintSite(ctx, target, run, plan.site); err != nil {
		t.Fatal(err)
	}
	if _, err = s.startFingerprintSamples(ctx, target, run, plan); err != nil {
		t.Fatal(err)
	}
	entries := fmt.Sprintf(`[{"numbers":[%s],"expected_count":300,"sample":{"request_id":"fingerprint-saved","state":"done","duration_ms":1}}]`, strings.TrimSuffix(strings.Repeat("42,", 200), ","))
	raw := json.RawMessage(fmt.Sprintf(`{"version":1,"model":%q,"format":"responses","reference_key":%q,"entries":%s}`, target.Model, fingerprintReferenceKey, entries))
	if count, valid := validateFingerprintSamples(raw, plan.config); count != 1 || !valid {
		t.Fatal("valid fixture rejected")
	}
	for _, variant := range []string{"same", "channel", "protocol", "reference", "legacy"} {
		body := raw
		candidate := plan
		candidate.config = map[string]any{"model": target.Model}
		switch variant {
		case "channel":
			candidate.revision = "changed-channel"
		case "protocol":
			candidate.config["format"] = "gemini"
		case "reference":
			body = json.RawMessage(strings.Replace(string(raw), fingerprintReferenceKey, "old-bank", 1))
		case "legacy":
			body = nil
		}
		var stored any
		if body != nil {
			stored = string(body)
		}
		if _, err = s.control.db.Exec(`UPDATE console_model_fingerprints SET sample_state=$3,result='{"used_outputs":1}',revision=$4 WHERE scope=$1 AND model=$2`, target.scope(), target.Model, stored, plan.revision); err != nil {
			t.Fatal(err)
		}
		resume, err := s.startFingerprintSamples(ctx, target, run, candidate)
		if err != nil || (len(resume) > 0) != (variant == "same") {
			t.Fatal("resume crossed incompatible identity", variant, err)
		}
	}
	// A preparation failure or a busy upstream can finish before sample
	// validation. Neither may relabel old evidence with a new channel revision.
	for _, state := range []string{"queued", "error"} {
		if _, err = s.control.db.Exec(`UPDATE console_model_fingerprints SET state='running',run_id=$3,deadline=now()+interval '30 seconds',sample_state=$4,result='{"used_outputs":1}',revision=$5 WHERE scope=$1 AND model=$2`, target.scope(), target.Model, run, string(raw), plan.revision); err != nil {
			t.Fatal(err)
		}
		if err = s.finishFingerprint(ctx, target, run, state, "", "new-channel-revision", nil); err != nil {
			t.Fatal(err)
		}
		var revision string
		if err = s.control.db.QueryRow(`SELECT revision FROM console_model_fingerprints WHERE scope=$1 AND model=$2`, target.scope(), target.Model).Scan(&revision); err != nil || revision != plan.revision {
			t.Fatal("preparation relabeled old samples", state, revision, err)
		}
	}
}

func TestModelFingerprintPrivateCheckpointValidation(t *testing.T) {
	config := map[string]any{"model": checkModel}
	entry := map[string]any{"numbers": make([]int, 200), "expected_count": 300, "sample": map[string]any{"request_id": "fingerprint-fixture", "state": "done", "duration_ms": 1}}
	for i := range entry["numbers"].([]int) {
		entry["numbers"].([]int)[i] = 42
	}
	for _, variant := range []string{"valid", "reference", "model", "protocol", "small", "large", "range", "duplicate", "count", "request_id", "long_request_id", "duration", "version", "missing", "oversize"} {
		t.Run(variant, func(t *testing.T) {
			var sample map[string]any
			json.Unmarshal([]byte(mustJSON(entry)), &sample)
			state := map[string]any{"version": 1, "model": checkModel, "format": "responses", "reference_key": fingerprintReferenceKey, "entries": []any{sample}}
			result := map[string]any{"model": checkModel, "status": "insufficient", "used_outputs": 1, "_resume": state}
			switch variant {
			case "reference":
				state["reference_key"] = "other"
			case "model":
				state["model"] = "gpt-6.1-sol"
			case "protocol":
				state["format"] = "gemini"
			case "small":
				sample["numbers"] = []int{1, 2, 3}
			case "large":
				sample["numbers"] = make([]int, 8193)
			case "range":
				sample["numbers"].([]any)[0] = 356
			case "duplicate":
				state["entries"] = []any{sample, sample}
				result["used_outputs"] = 2
			case "count":
				result["used_outputs"] = 2
			case "request_id":
				sample["sample"].(map[string]any)["request_id"] = "private-upstream-text"
			case "long_request_id":
				sample["sample"].(map[string]any)["request_id"] = "fingerprint-" + strings.Repeat("a", 65)
			case "duration":
				sample["sample"].(map[string]any)["duration_ms"] = -1
			case "version":
				state["version"] = 2
			case "missing":
				delete(result, "_resume")
			}
			raw := []byte(mustJSON(result))
			if variant == "oversize" {
				raw = append(raw, []byte(strings.Repeat(" ", fingerprintSampleBytes))...)
			}
			public, private, err := decodeFingerprintCheckpoint(raw, config)
			if variant == "valid" {
				if err != nil || len(private) == 0 || strings.Contains(string(public), "_resume") || strings.Contains(string(public), `"numbers"`) {
					t.Fatal("valid checkpoint was rejected or exposed", err)
				}
			} else if err == nil {
				t.Fatal("invalid private evidence admitted", variant)
			}
		})
	}
}

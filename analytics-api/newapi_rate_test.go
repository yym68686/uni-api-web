package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestNewAPIReadRetryRespectsDeadlineAndNeverReplaysWrites(t *testing.T) {
	s, account, _ := newAPITestService(t)
	for _, tc := range []struct {
		method, path string
		delay, want  int
	}{
		{"GET", "/api/user/self/groups", 1, 2},
		{"POST", "/api/token/42/key", 1, 2},
		{"POST", "/api/token/", 1, 1},
		{"DELETE", "/api/token/42", 1, 1},
		{"GET", "/api/user/self", 1200, 1},
		{"GET", "/api/log/self?type=2", 1, 1},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			calls := 0
			withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.Header().Set("Retry-After", time.Now().Add(time.Duration(tc.delay)*time.Second).UTC().Format(http.TimeFormat))
					w.WriteHeader(429)
					return
				}
				newAPIOK(w, map[string]any{})
			}))
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			err := s.newAPICall(ctx, account, "https://usage.example", tc.method, tc.path, nil, nil)
			if calls != tc.want || (tc.want == 2 && err != nil) {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
			if tc.want == 1 {
				var remote *subRemoteError
				if !errors.As(err, &remote) || remote.Status != 429 || remote.Operation == "" {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestNewAPILogPacingSharedAcrossServicesAndAccounts(t *testing.T) {
	s, _, _ := newAPITestService(t)
	gap := 100 * time.Millisecond
	s.newAPILogGap = &gap
	other := &Service{control: s.control, newAPILogGap: &gap}
	base := "https://" + randomID() + ".example"
	t.Cleanup(func() { s.control.db.Exec(`DELETE FROM console_site_api_schedule WHERE site=$1`, base) })
	ctx := context.Background()
	if err := s.newAPIWaitLog(ctx, base); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := other.newAPIWaitLog(ctx, base+"/other-account-path"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 70*time.Millisecond {
		t.Fatal("another replica/account bypassed host pacing")
	}
	short, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	if err := s.newAPIWaitLog(short, base); err == nil {
		t.Fatal("queued call ignored deadline")
	}
	// Management reads don't wait behind queued background log reservations.
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { newAPIOK(w, nil) }))
	var before, after time.Time
	s.control.db.QueryRow(`SELECT next_log_at FROM console_site_api_schedule WHERE site=$1`, base).Scan(&before)
	if err := other.newAPIReadCall(ctx, "fixture", base, "GET", "/api/user/self/groups", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	s.control.db.QueryRow(`SELECT next_log_at FROM console_site_api_schedule WHERE site=$1`, base).Scan(&after)
	if !before.Equal(after) {
		t.Fatal("manual management queued behind ledger pages")
	}
}

func TestNewAPIRetryAfter(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for value, want := range map[string]time.Duration{"120": 120 * time.Second, "": 0, "-1": 0, "bad": 0, now.Add(3 * time.Second).Format(http.TimeFormat): 3 * time.Second} {
		if got := newAPIRetryAfter(value, now); got != want {
			t.Fatalf("%q: %v != %v", value, got, want)
		}
	}
}

func TestNewAPILedgerRateLimitKeepsCursorAndUsesDurableBackoff(t *testing.T) {
	s, account, _ := newAPITestService(t)
	to := time.Now().Truncate(time.Second)
	if err := s.queueAccountSpendWindow(context.Background(), account, to.Add(-time.Hour).UnixMilli(), to.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	withSpendUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "360")
		w.WriteHeader(429)
	}))
	runSpendBatch(t, s, account)
	var page int
	var remaining float64
	var requested bool
	if err := s.control.db.QueryRow(`SELECT page,extract(epoch FROM next_attempt-now()),requested FROM console_sub_account_spend_cache WHERE account_id=$1`, account).Scan(&page, &remaining, &requested); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || page != 1 || remaining < 350 || !requested {
		t.Fatalf("calls=%d page=%d remaining=%f requested=%t", calls, page, remaining, requested)
	}
}

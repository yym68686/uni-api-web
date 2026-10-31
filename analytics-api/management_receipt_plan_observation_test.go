package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestManagementReceiptPlanDeltaRequiresOneActualExecution(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after map[string]managementPreparedPlanCounts
		want          string
	}{
		{"first_custom", nil, map[string]managementPreparedPlanCounts{"new": {custom: 1}}, "custom"},
		{"first_generic", nil, map[string]managementPreparedPlanCounts{"new": {generic: 1}}, "generic"},
		{"existing_custom", map[string]managementPreparedPlanCounts{"p": {3, 4}}, map[string]managementPreparedPlanCounts{"p": {3, 5}}, "custom"},
		{"existing_generic", map[string]managementPreparedPlanCounts{"p": {3, 4}}, map[string]managementPreparedPlanCounts{"p": {4, 4}}, "generic"},
		{"unchanged", map[string]managementPreparedPlanCounts{"p": {3, 4}}, map[string]managementPreparedPlanCounts{"p": {3, 4}}, "unavailable"},
		{"two_executions", nil, map[string]managementPreparedPlanCounts{"p": {1, 1}}, "unavailable"},
		{"counter_reset", map[string]managementPreparedPlanCounts{"p": {3, 4}}, map[string]managementPreparedPlanCounts{"p": {1, 4}}, "unavailable"},
		{"evicted", map[string]managementPreparedPlanCounts{"p": {3, 4}}, nil, "unavailable"},
		{"unprepared", nil, nil, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := managementReceiptPlanDelta(tc.before, tc.after)
			if got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	var limiter managementReceiptPlanLimiter
	now := time.Unix(10, 0)
	if !limiter.admit(now) || limiter.admit(now.Add(59*time.Second)) || !limiter.admit(now.Add(time.Minute)) || limiter.admit(now) {
		t.Fatal("diagnostic admission was not bounded to once per minute")
	}
}

func TestManagementReceiptPlanObservationMatchesActualConnectionMode(t *testing.T) {
	s := managementHydrationFixture(t)
	db := s.control.db
	ids := []string{"a-1-gpt-5.5", "q-1-gpt-5.5", "absent", "a-1-gpt-5.5"}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for _, mode := range []string{"generic", "custom"} {
		t.Run(mode, func(t *testing.T) {
			tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			// Test isolation only; the production diagnostic never changes this.
			if _, err := tx.Exec(`SET LOCAL plan_cache_mode=force_` + mode + `_plan`); err != nil {
				t.Fatal(err)
			}
			want, wantErr := readManagementStreamReceiptsUncached(context.Background(), tx, "owner", ids, true)
			output.Reset()
			managementReceiptPlanAdmission.at.Store(0)
			ctx := context.WithValue(context.Background(), managementReceiptPlanKey{}, &managementReceiptPlanRequest{})
			ctx = context.WithValue(ctx, consoleRequestIDKey{}, "plan-fixture-123")
			got, gotErr := readManagementStreamReceiptsUncached(ctx, tx, "owner", ids, true)
			if wantErr != nil || gotErr != nil || !reflect.DeepEqual(got, want) {
				t.Fatal("observed receipt or permission changed", gotErr, wantErr)
			}
			text := output.String()
			if !strings.Contains(text, "cache_mode="+mode+" ") || !strings.Contains(text, "ids=4 ") || !strings.Contains(text, "request_id=plan-fixture-123") {
				t.Fatal("actual same-connection execution was not identified", text)
			}
			for _, private := range []string{"owner", "a-1-gpt", "q-1-gpt", "absent", "stmtcache", "SELECT", "WHERE"} {
				if strings.Contains(text, private) {
					t.Fatal("private query data logged", private)
				}
			}
			output.Reset()
			if _, err := readManagementStreamReceiptsUncached(ctx, tx, "owner", ids, true); err != nil || output.Len() != 0 {
				t.Fatal("same request observed twice", err, output.String())
			}
			fresh := context.WithValue(context.Background(), managementReceiptPlanKey{}, &managementReceiptPlanRequest{})
			if _, err := readManagementStreamReceiptsUncached(fresh, tx, "owner", ids, true); err != nil || output.Len() != 0 {
				t.Fatal("rate-limited diagnostic ran", err, output.String())
			}
			managementReceiptPlanAdmission.at.Store(0)
			if _, err := readManagementStreamReceiptsUncached(context.Background(), tx, "owner", ids, true); err != nil || output.Len() != 0 || managementReceiptPlanAdmission.at.Load() != 0 {
				t.Fatal("ordinary read ran diagnostic SQL", err)
			}
		})
	}
	managementReceiptPlanAdmission.at.Store(0)
}

func TestManagementReceiptPlanSavepointRestoresOptionalCatalogFailure(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SAVEPOINT management_receipt_plan_observation`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1/0`); err == nil {
		t.Fatal("fixture did not fail")
	}
	finishManagementReceiptPlanSavepoint(ctx, tx, false)
	if _, err := readManagementStreamReceiptsUncached(ctx, tx, "owner", []string{"a-1-gpt-5.5"}, true); err != nil {
		t.Fatal("optional failure poisoned original read", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("diagnostic left an aborted transaction", err)
	}
}

func TestManagementReceiptPlanKeepsStrictFallbackOwnershipAndCancellation(t *testing.T) {
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`UPDATE console_management_receipt_signal SET valid=false WHERE id='a-1-gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"strict-invalid", "non-strict-invalid", "foreign", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			tx, err := s.control.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			owner, strict := "owner", mode != "non-strict-invalid"
			if mode == "foreign" {
				owner = "foreign"
			}
			ctx := context.Background()
			if mode == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			want, wantErr := readManagementStreamReceiptsUncached(ctx, tx, owner, []string{"a-1-gpt-5.5"}, strict)
			managementReceiptPlanAdmission.at.Store(0)
			observed := context.WithValue(ctx, managementReceiptPlanKey{}, &managementReceiptPlanRequest{})
			got, gotErr := readManagementStreamReceiptsUncached(observed, tx, owner, []string{"a-1-gpt-5.5"}, strict)
			if !errors.Is(gotErr, wantErr) || !reflect.DeepEqual(got, want) {
				t.Fatal("diagnostic changed error, fallback, permission or receipt", gotErr, wantErr)
			}
			if mode == "strict-invalid" && !errors.Is(gotErr, errManagementPredicateFallback) || mode == "canceled" && !errors.Is(gotErr, context.Canceled) {
				t.Fatal("fixture did not exercise expected error", gotErr)
			}
			if mode != "canceled" {
				var one int
				if err := tx.QueryRow(`SELECT 1`).Scan(&one); err != nil || one != 1 {
					t.Fatal("read-only diagnostic prevented original fallback", err)
				}
			}
		})
	}
	managementReceiptPlanAdmission.at.Store(0)
}

func TestManagementReceiptPlanOptInKeepsValidationAuthorizationAndFilters(t *testing.T) {
	base := url.Values{"filters": {`{"model":"gpt-5.5"}`}}
	want, err := parseManagementQuery(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"1", "hydrate"} {
		base.Set("observe_receipt_plan", mode)
		got, err := parseManagementQuery(base)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("diagnostic changed filters", mode, err)
		}
	}
	for _, value := range []string{"", "0", "true", "1&sql=SELECT"} {
		base.Set("observe_receipt_plan", value)
		if _, err := parseManagementQuery(base); !errors.Is(err, errManagementFilters) {
			t.Fatal("invalid diagnostic accepted", value)
		}
	}
	base["observe_receipt_plan"] = []string{"1", "1"}
	if _, err := parseManagementQuery(base); !errors.Is(err, errManagementFilters) {
		t.Fatal("duplicate flag accepted")
	}
	for _, mode := range []string{"1", "hydrate"} {
		t.Run(mode, func(t *testing.T) {
			s := managementHydrationFixture(t)
			managementReceiptPlanAdmission.at.Store(0)
			r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query?observe_receipt_plan="+mode, nil)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized || managementReceiptPlanAdmission.at.Load() != 0 {
				t.Fatal("diagnostic bypassed authentication", w.Code)
			}
			token, err := s.control.newSession(context.Background(), "owner")
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(previous)
			var before managementPageResponse
			for _, suffix := range []string{"&observe_receipt_plan=" + mode, ""} {
				r := httptest.NewRequest(http.MethodGet, "/v1/channel-management/query?filters="+url.QueryEscape(`{"model":"gpt-5.5"}`)+suffix, nil)
				r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				var page managementPageResponse
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
					t.Fatal("diagnostic changed successful HTTP read", w.Code)
				}
				page.ReadAtMS = 0
				if suffix != "" {
					before = page
					if mode == "1" && !strings.Contains(output.String(), "management_receipt_plan cache_mode=") {
						t.Fatal("authenticated diagnostic did not reach pinned reader", output.String())
					}
					// This fixture may resolve all page receipts from the predicate
					// read. Hydration mode must not force an extra SELECT to observe.
					if mode == "hydrate" && strings.Contains(output.String(), "management_receipt_plan cache_mode=") && !strings.Contains(output.String(), "phase=hydrate ") {
						t.Fatal("hydration diagnostic observed an earlier reader")
					}
					output.Reset()
				} else if !reflect.DeepEqual(page, before) || strings.Contains(output.String(), "management_receipt_plan ") {
					t.Fatal("diagnostic changed public snapshot or enabled ordinary observation")
				}
			}
			managementReceiptPlanAdmission.at.Store(0)
		})
	}
}

func TestManagementReceiptHydrationObservationWaitsForActualPageRead(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	if managementReceiptHydrationObservationContext(ctx) != ctx {
		t.Fatal("ordinary hydration installed diagnostic state")
	}
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	request := &managementReceiptPlanRequest{hydrationOnly: true}
	ctx = context.WithValue(ctx, managementReceiptPlanKey{}, request)
	managementReceiptPlanAdmission.at.Store(0)
	defer managementReceiptPlanAdmission.at.Store(0)
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	if _, err := readManagementStreamReceiptsUncached(ctx, tx, "owner", []string{"a-1-gpt-5.5"}, true); err != nil {
		t.Fatal(err)
	}
	if request.used.Load() || managementReceiptPlanAdmission.at.Load() != 0 || output.Len() != 0 {
		t.Fatal("earlier predicate consumed hydration diagnostic", output.String())
	}
	result := &subResult{Availability: subProbe{ID: "a-1-gpt-5.5"}}
	accounts := []subAccount{{Targets: []subTarget{{Models: []subModelResult{{Result: result}}}}}}
	if err := s.attachManagementPageSummaryUsageFrom(ctx, tx, "owner", accounts, nil); err != nil {
		t.Fatal(err)
	}
	if !request.used.Load() || !strings.Contains(output.String(), "phase=hydrate ") || !strings.Contains(output.String(), "management_receipt_access available=true ") || result.Availability.Usage == nil {
		t.Fatal("actual page hydration was not observed or populated", output.String())
	}
	output.Reset()
	if _, err := readManagementStreamReceiptsUncached(managementReceiptHydrationObservationContext(ctx), tx, "owner", []string{"q-1-gpt-5.5"}, true); err != nil || output.Len() != 0 {
		t.Fatal("hydration diagnostic exceeded one query per request", err, output.String())
	}
}

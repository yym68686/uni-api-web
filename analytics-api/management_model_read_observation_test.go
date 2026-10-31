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
)

func modelObservationFixture(t *testing.T) *Service {
	t.Helper()
	s := managementHydrationFixture(t)
	if _, err := s.control.db.Exec(`INSERT INTO console_sub_models(account_id,group_id,model,state)
 SELECT 'a',g,'off-scope-'||m,'idle' FROM generate_series(1,61) g CROSS JOIN generate_series(1,32) m`); err != nil {
		t.Fatal(err)
	}
	return s
}

func modelObservationDirectory(ctx context.Context, s *Service, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, managementDirectoryScopeKey{}, &managementDirectoryScope{
		store: &managementModelDirectories{}, database: s.control.db, tx: tx, owner: "owner", ready: true,
		accountRevision: "snapshot", modelRevision: 1,
	})
}

func TestManagementModelReadObservationKeepsRowsAndActualPlanMode(t *testing.T) {
	s := modelObservationFixture(t)
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	defer managementModelReadAdmission.at.Store(0)
	for _, mode := range []string{"generic", "custom"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			// Test fixture only; production diagnostics never set execution modes.
			if _, err := tx.Exec(`SET LOCAL plan_cache_mode=force_` + mode + `_plan`); err != nil {
				t.Fatal(err)
			}
			ctx = modelObservationDirectory(ctx, s, tx)
			scope := managementStreamScope{CompactRevision: true, Model: "gpt-5.5", Names: []string{"gpt-5.5", "gpt-6-astra"}}
			want, wantIDs, err := readManagementStreamModels(ctx, tx, "owner", true, scope)
			if err != nil {
				t.Fatal(err)
			}
			request := &managementModelReadRequest{}
			observed := context.WithValue(ctx, managementModelReadKey{}, request)
			observed = context.WithValue(observed, consoleRequestIDKey{}, "model-observation-123")
			managementModelReadAdmission.at.Store(0)
			output.Reset()
			got, ids, err := readManagementStreamModels(observed, tx, "owner", true, scope)
			if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(ids, wantIDs) {
				t.Fatal("observation changed predicates or receipt membership", err)
			}
			text := output.String()
			if !request.used.Load() || !strings.Contains(text, "cache_mode="+mode+" ") ||
				!strings.Contains(text, "candidates=122 rows=122 ") || !strings.Contains(text, "management_model_access available=true ") ||
				!strings.Contains(text, "request_id=model-observation-123") {
				t.Fatal("actual pinned model read not identified", text)
			}
			for _, private := range []string{"owner", "off-scope", "gpt-5.5", "stmtcache", "SELECT", "WHERE"} {
				if strings.Contains(text, private) {
					t.Fatal("private query data logged", private)
				}
			}
			output.Reset()
			if _, _, err := readManagementStreamModels(observed, tx, "owner", true, scope); err != nil || output.Len() != 0 {
				t.Fatal("request observed twice", err, output.String())
			}
			fresh := context.WithValue(ctx, managementModelReadKey{}, &managementModelReadRequest{})
			if _, _, err := readManagementStreamModels(fresh, tx, "owner", true, scope); err != nil || output.Len() != 0 {
				t.Fatal("process minute limit bypassed", err, output.String())
			}
			managementModelReadAdmission.at.Store(0)
			if _, _, err := readManagementStreamModels(ctx, tx, "owner", true, scope); err != nil || output.Len() != 0 || managementModelReadAdmission.at.Load() != 0 {
				t.Fatal("ordinary read ran diagnostic", err)
			}
		})
	}
}

func TestManagementModelObservationAdmissionAndMetadataFailure(t *testing.T) {
	s := modelObservationFixture(t)
	ctx := context.WithValue(context.Background(), managementModelReadKey{}, &managementModelReadRequest{})
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	managementModelReadAdmission.at.Store(0)
	defer managementModelReadAdmission.at.Store(0)
	for _, tc := range []struct {
		db    controlQueryDB
		query string
		ids   int
	}{
		{s.control.db, managementModelSeekSQL, 1}, {tx, managementModelStreamSQL, 1},
		{tx, managementModelSeekSQL, 0}, {tx, managementModelSeekSQL, 4097},
	} {
		if beginManagementModelReadObservation(ctx, tc.db, tc.query, tc.ids) != nil || managementModelReadAdmission.at.Load() != 0 {
			t.Fatal("unbounded, unpinned or scan read admitted")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if beginManagementModelReadObservation(canceled, tx, managementModelSeekSQL, 1) != nil || managementModelReadAdmission.at.Load() != 0 {
		t.Fatal("canceled read consumed admission")
	}
	if _, err := managementSignalAccessSnapshot(ctx, tx, "console_sub_usage"); err == nil {
		t.Fatal("arbitrary relation admitted")
	}
	if _, err := tx.Exec(`SAVEPOINT management_model_read_observation`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SELECT 1/0`); err == nil {
		t.Fatal("fixture did not fail")
	}
	finishManagementModelReadSavepoint(ctx, tx, false)
	if _, err := tx.Exec(`SELECT 1`); err != nil {
		t.Fatal("optional metadata failure poisoned business transaction", err)
	}
}

func TestManagementModelAccessIsTransactionAndRelationLocal(t *testing.T) {
	s := modelObservationFixture(t)
	ctx := context.Background()
	tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before, err := managementSignalAccessSnapshot(ctx, tx, "console_management_model_signal")
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := managementReceiptAccessSnapshot(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := readManagementStreamModels(modelObservationDirectory(ctx, s, tx), tx, "owner", true,
		managementStreamScope{CompactRevision: true, Model: "gpt-5.5", Names: []string{"gpt-5.5"}}); err != nil {
		t.Fatal(err)
	}
	after, err := managementSignalAccessSnapshot(ctx, tx, "console_management_model_signal")
	if err != nil || after.heapFetch <= before.heapFetch || after.indexFetch <= before.indexFetch {
		t.Fatal("model read did not enter model relation counters", before, after, err)
	}
	unchanged, err := managementReceiptAccessSnapshot(ctx, tx)
	if err != nil || unchanged != receipts {
		t.Fatal("model access contaminated receipt counters", err)
	}
	if _, err := s.control.db.Exec(`SELECT count(*) FROM console_management_model_signal`); err != nil {
		t.Fatal(err)
	}
	unchanged, err = managementSignalAccessSnapshot(ctx, tx, "console_management_model_signal")
	if err != nil || unchanged != after {
		t.Fatal("metadata or another backend contaminated model counters", err)
	}
}

func TestManagementModelObservationKeepsStrictErrorsAndOwnership(t *testing.T) {
	s := modelObservationFixture(t)
	if _, err := s.control.db.Exec(`UPDATE console_management_model_signal SET valid=false WHERE account_id='a' AND group_id=1 AND model='gpt-5.5'`); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	defer managementModelReadAdmission.at.Store(0)
	for _, mode := range []string{"strict", "non-strict", "foreign", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			tx, err := s.control.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			ctx = modelObservationDirectory(ctx, s, tx)
			owner := "owner"
			if mode == "foreign" {
				owner = "foreign"
			}
			if mode == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			scope := managementStreamScope{CompactRevision: true, Model: "gpt-5.5", Names: []string{"gpt-5.5"}}
			want, wantIDs, wantErr := readManagementStreamModels(ctx, tx, owner, mode != "non-strict", scope)
			observed := context.WithValue(ctx, managementModelReadKey{}, &managementModelReadRequest{})
			managementModelReadAdmission.at.Store(0)
			output.Reset()
			got, ids, gotErr := readManagementStreamModels(observed, tx, owner, mode != "non-strict", scope)
			if !errors.Is(gotErr, wantErr) || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(ids, wantIDs) {
				t.Fatal("observation changed failure, ownership or non-strict evidence", gotErr, wantErr)
			}
			if mode == "strict" && !errors.Is(gotErr, errManagementPredicateFallback) || mode == "canceled" && !errors.Is(gotErr, context.Canceled) {
				t.Fatal("fixture missed expected error", gotErr)
			}
			if mode != "non-strict" && strings.Contains(output.String(), "management_model_access available=true ") {
				t.Fatal("failed or foreign read reported successful model access")
			}
			if mode != "canceled" {
				if _, err := tx.Exec(`SELECT 1`); err != nil {
					t.Fatal("original fallback transaction poisoned", err)
				}
			}
		})
	}
}

func TestManagementModelReadOptInKeepsHTTPAuthorizationAndCompletePage(t *testing.T) {
	values := url.Values{"filters": {`{"model":"gpt-5.5"}`}}
	wantFilters, err := parseManagementQuery(values)
	if err != nil {
		t.Fatal(err)
	}
	values.Set("observe_model_read", "1")
	gotFilters, err := parseManagementQuery(values)
	if err != nil || !reflect.DeepEqual(gotFilters, wantFilters) {
		t.Fatal("diagnostic changed filters", err)
	}
	for _, bad := range []string{"", "0", "true", "1&sql=SELECT"} {
		values.Set("observe_model_read", bad)
		if _, err := parseManagementQuery(values); !errors.Is(err, errManagementFilters) {
			t.Fatal("invalid diagnostic accepted", bad)
		}
	}
	values["observe_model_read"] = []string{"1", "1"}
	if _, err := parseManagementQuery(values); !errors.Is(err, errManagementFilters) {
		t.Fatal("duplicate diagnostic accepted")
	}
	s := modelObservationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	managementModelReadAdmission.at.Store(0)
	defer managementModelReadAdmission.at.Store(0)
	endpoint := "/v1/channel-management/query?filters=" + url.QueryEscape(`{"model":"gpt-5.5"}`)
	r := httptest.NewRequest(http.MethodGet, endpoint+"&observe_model_read=1", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || managementModelReadAdmission.at.Load() != 0 {
		t.Fatal("diagnostic bypassed authorization", w.Code)
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
	for _, suffix := range []string{"&observe_model_read=1", ""} {
		r := httptest.NewRequest(http.MethodGet, endpoint+suffix, nil)
		r.AddCookie(&http.Cookie{Name: "uni_console_session", Value: token})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		var page managementPageResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatal("diagnostic changed successful HTTP read", w.Code)
		}
		page.ReadAtMS = 0
		if suffix != "" {
			before = page
			if !strings.Contains(output.String(), "management_model_access available=true ") {
				t.Fatal("authenticated request did not reach model diagnostic", output.String())
			}
			output.Reset()
		} else if !reflect.DeepEqual(page, before) || strings.Contains(output.String(), "management_model_access ") {
			t.Fatal("diagnostic changed complete page or enabled ordinary SQL")
		}
	}
}

package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagementPagingUsesTwoProofsAndReleasesSingleConnection(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	f := managementQueryFilters{GroupStatus: "all", Model: "gpt-5.5"}
	first, err := s.readManagementQuerySnapshot(ctx, "owner", f, "", false)
	if err != nil {
		t.Fatal(err)
	}
	s.rememberManagementPagingPlan("owner", f, first)
	want, err := s.readManagementQuerySnapshot(ctx, "owner", f, first.Page.NextCursor, false)
	if err != nil {
		t.Fatal(err)
	}
	s.control.db.SetMaxOpenConns(1)
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	traced := context.WithValue(managementTraceContext(ctx), consoleRequestIDKey{}, "paging-two-proofs")
	got, ok, err := s.currentManagementPagingPage(traced, "owner", f, first.Page.NextCursor, false)
	if err != nil || !ok {
		t.Fatal("single-connection paging failed", err)
	}
	got.ReadAtMS = want.Page.ReadAtMS
	if mustJSON(got.managementQueryPage) != mustJSON(want.Page) {
		t.Fatal("same-snapshot paging changed public fields, ordering or revision")
	}
	if count := strings.Count(output.String(), "management_database_session phase=proof "); count != 2 {
		t.Fatal("paging did not eliminate the redundant third proof", count, output.String())
	}
	for _, stage := range []string{"proof_compact", "proof_epochs", "proof_overrides", "proof_headers"} {
		if count := strings.Count(output.String(), "stage="+stage+" "); count != 2 {
			t.Fatal("individual proof stages not measured once per snapshot", stage, count)
		}
	}
	if s.control.db.Stats().InUse != 0 {
		t.Fatal("successful hydration retained a pool connection")
	}
	if _, ok, err := s.currentManagementPagingPage(ctx, "owner", f, "forged", false); !errors.Is(err, errManagementCursor) || ok {
		t.Fatal("invalid cursor accepted", err)
	}
	if s.control.db.Stats().InUse != 0 {
		t.Fatal("invalid cursor leaked the verified snapshot")
	}
	key := managementPagingKey("owner", f)
	s.managementComputed.mu.Lock()
	entry := s.managementComputed.pages[key]
	entry.Plan = []byte("invalid-json")
	s.managementComputed.pages[key] = entry
	s.managementComputed.mu.Unlock()
	if _, ok, err := s.currentManagementPagingPage(ctx, "owner", f, first.Page.NextCursor, false); err == nil || ok {
		t.Fatal("invalid directory accepted")
	}
	if s.control.db.Stats().InUse != 0 {
		t.Fatal("decode failure leaked the verified snapshot")
	}
}

func TestManagementRetainedProofKeepsOneReadOnlySnapshot(t *testing.T) {
	s := managementHydrationFixture(t)
	prepareManagementRevisionsForTest(t, s.control.db)
	ctx := context.Background()
	expected, _, err := s.managementReadProof(ctx, s.control.db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot managementCurrentSnapshot
	proof, at, ok, err := s.currentManagementProofRetaining(ctx, "owner", expected, &snapshot)
	if err != nil || !ok || snapshot.tx == nil || snapshot.directory == nil || !snapshot.directory.ready {
		t.Fatal("verified snapshot unavailable", err)
	}
	defer snapshot.tx.Rollback()
	var isolation, readOnly string
	var txAt time.Time
	if err := snapshot.tx.QueryRow(`SELECT current_setting('transaction_isolation'),current_setting('transaction_read_only'),transaction_timestamp()`).Scan(&isolation, &readOnly, &txAt); err != nil || isolation != "repeatable read" || readOnly != "on" || !txAt.Equal(at) {
		t.Fatal("proof did not describe its retained read-only RR transaction", err, isolation, readOnly)
	}
	var before string
	if err := snapshot.tx.QueryRow(`SELECT name FROM console_sub_targets WHERE account_id='a' AND group_id=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET name='committed after final proof' WHERE account_id='a' AND group_id=1`); err != nil {
		t.Fatal(err)
	}
	current := context.WithValue(ctx, managementDirectoryScopeKey{}, snapshot.directory)
	stable, stableAt, err := s.managementReadProof(current, snapshot.tx, "owner")
	if err != nil || stable.Stamp != proof.Stamp || !stableAt.Equal(at) {
		t.Fatal("proof generation mixed inside retained transaction", err)
	}
	var name string
	if err := snapshot.tx.QueryRow(`SELECT name FROM console_sub_targets WHERE account_id='a' AND group_id=1`).Scan(&name); err != nil || name != before {
		t.Fatal("hydration mixed a later generation", err)
	}
	fresh, _, err := s.managementReadProof(ctx, s.control.db, "owner")
	if err != nil || fresh.Stamp == proof.Stamp {
		t.Fatal("next request failed to observe the committed generation", err)
	}
	if err := snapshot.tx.Rollback(); err != nil || s.control.db.Stats().InUse != 0 {
		t.Fatal("snapshot ownership/release failed", err)
	}
	if err := snapshot.tx.QueryRow(`SELECT 1`).Scan(new(int)); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal("released snapshot remained usable", err)
	}
}

func TestManagementRetainedProofRejectsChangesAndNeverLeaksTransactions(t *testing.T) {
	for _, mode := range []string{"evidence", "headers", "headers-aba", "source", "denied", "gateway-revision", "cancel", "deadline", "invalid-projection"} {
		t.Run(mode, func(t *testing.T) {
			s := managementHydrationFixture(t)
			prepareManagementRevisionsForTest(t, s.control.db)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var once sync.Once
			var writeErr error
			gatewayDone := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				once.Do(func() {
					defer close(gatewayDone)
					query := ""
					switch mode {
					case "evidence":
						query = `UPDATE console_sub_models SET message='changed during gateway' WHERE account_id='a' AND group_id=1`
					case "headers":
						query = `UPDATE console_sub_accounts SET balance='{"amount":42,"status":"ok"}' WHERE id='a'`
					case "headers-aba":
						query = `UPDATE console_sub_accounts SET balance='{"amount":42,"status":"ok"}' WHERE id='a'; UPDATE console_sub_accounts SET balance=NULL WHERE id='a'`
					case "source":
						query = `UPDATE console_sources SET enabled=false WHERE id='live'`
					case "invalid-projection":
						query = `UPDATE console_management_revision_state SET complete=false WHERE relation_name='console_sub_models'`
					case "cancel":
						cancel()
					case "deadline":
						time.Sleep(1100 * time.Millisecond)
					}
					if query != "" {
						_, writeErr = s.control.db.Exec(query)
					}
				})
				if mode == "denied" {
					http.Error(w, "denied", http.StatusForbidden)
					return
				}
				rev := "r1"
				if mode == "gateway-revision" {
					rev = "r2"
				}
				writeJSON(w, http.StatusOK, map[string]any{"format": "management-revision-v1", "revision": rev, "config_revision": "c1"})
			}))
			defer server.Close()
			source := controlSource{sourceView: sourceView{ID: "live", Name: "Live", Base: server.URL}, Key: "observer", ConfigKey: "admin"}
			if _, err := s.control.saveSource(ctx, source, false); err != nil {
				t.Fatal(err)
			}
			expected, _, err := s.managementReadProof(ctx, s.control.db, "owner")
			if err != nil {
				t.Fatal(err)
			}
			expected.Facts = []managementGatewayFacts{{Source: source, AtomicSnapshot: true, Revision: "r1", ConfigRevision: "c1"}}
			if mode == "deadline" {
				expected.Until = time.Now().Add(time.Second)
			}
			var snapshot managementCurrentSnapshot
			_, _, ok, err := s.currentManagementProofRetaining(ctx, "owner", expected, &snapshot)
			select {
			case <-gatewayDone:
			case <-time.After(2 * time.Second):
				t.Fatal("fixture did not exercise the live gateway fence")
			}
			if writeErr != nil {
				t.Fatal(writeErr)
			}
			if ok || snapshot.tx != nil || snapshot.directory != nil {
				t.Fatal("invalid proof published or leaked a transaction", err)
			}
			wantError := mode == "source" || mode == "denied" || mode == "cancel"
			if (err != nil) != wantError {
				t.Fatal("existing error/miss behavior changed", err)
			}
			if s.control.db.Stats().InUse != 0 {
				t.Fatal("failed proof retained a pool connection")
			}
		})
	}
}

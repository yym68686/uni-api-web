package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
)

func TestManagementTargetRowsTracePreservesDetailsAndDoesNotExposeValues(t *testing.T) {
	s := managementHydrationFixture(t)
	ctx := context.Background()
	if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET result='{"model":"gpt-5.5","availability":{"status":"success","text":"private-body","id":"private-id"}}',billing='{"rate":0.5}',compaction='{"status":"supported"}',tool_use='{"status":"supported"}' WHERE group_id=1`); err != nil {
		t.Fatal(err)
	}
	var targetBytes int
	if err := s.control.db.QueryRow(`SELECT sum(coalesce(octet_length(t.result::text),0)+coalesce(octet_length(t.billing::text),0)+coalesce(octet_length(cs.compaction::text),0)+coalesce(octet_length(cs.tool_use::text),0)) FROM console_sub_targets t JOIN console_sub_capability_summaries cs USING(account_id,group_id)`).Scan(&targetBytes); err != nil || targetBytes <= 0 {
		t.Fatal("target trace fixture bytes missing", err)
	}
	read := func(traced bool) []subAccount {
		t.Helper()
		accounts, err := readSubAccountHeaders(ctx, s.control.db, "owner", "")
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		previous := log.Writer()
		if traced {
			log.SetOutput(&output)
		}
		readCtx := ctx
		if traced {
			readCtx = context.WithValue(managementTraceContext(ctx), consoleRequestIDKey{}, "target-trace-123")
		}
		readModels := func(accounts []subAccount) error {
			if traced && !strings.Contains(output.String(), "stage=target_rows ") {
				t.Error("target stream timing included later readers")
			}
			_, _, err := s.subReadModelsFrom(readCtx, s.control.db, "owner", "", 0, "", true, accounts)
			return err
		}
		err = s.readSubAccountDetailsWithReadersFrom(readCtx, s.control.db, "owner", "", 0, "", true, true, accounts, nil, sharedQuality{}, readModels, nil, true, nil)
		if traced {
			log.SetOutput(previous)
		}
		if err != nil {
			t.Fatal(err)
		}
		if traced {
			line := output.String()
			if strings.Count(line, "stage=target_rows ") != 1 {
				t.Fatal("target stream timing duplicated")
			}
			for _, field := range []string{"stage=target_rows ", "query_ready_ms=", "decode_ms=", "elapsed_ms=", "rows=61 ", fmt.Sprintf("bytes=%d ", targetBytes), "advance_ms=", "request_id=target-trace-123"} {
				if !strings.Contains(line, field) {
					t.Fatal("target row timing split missing", field, line)
				}
			}
			for _, private := range []string{"owner", "fixture", "a.test", "model message", "current diagnostic", "private-body", "private-id", "SELECT", "console_sub_targets"} {
				if strings.Contains(line, private) {
					t.Fatal("target row timing exposed query or business data", private)
				}
			}
		}
		return accounts
	}
	want, got := read(false), read(true)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("target row trace changed read results")
	}
}

func TestManagementTargetRowsTracePreservesErrorEmptyAndCancellation(t *testing.T) {
	s := managementHydrationFixture(t)
	for _, tc := range []struct {
		name, column, raw, owner string
		cancel                   bool
	}{
		{name: "empty", owner: "other"},
		{name: "null", column: "result", raw: `null`},
		{name: "result_error", column: "result", raw: `{"availability":{"ttft_ms":"private-value"}}`},
		{name: "billing_error", column: "billing", raw: `{"rate":"private-value"}`},
		{name: "compaction_error", column: "compaction", raw: `{"attempts":"private-value"}`},
		{name: "tool_use_error", column: "tool_use", raw: `{"checked_at":"private-value"}`},
		{name: "canceled", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET result=NULL,billing=NULL,compaction=NULL,tool_use=NULL`); err != nil {
				t.Fatal(err)
			}
			if tc.column != "" {
				// The column comes only from the fixed cases, never external input.
				if _, err := s.control.db.Exec(`UPDATE console_sub_targets SET `+tc.column+`=$1 WHERE group_id=1`, tc.raw); err != nil {
					t.Fatal(err)
				}
			}
			owner := tc.owner
			if owner == "" {
				owner = "owner"
			}
			read := func(traced bool) ([]subAccount, error, string) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				accounts, err := readSubAccountHeaders(ctx, s.control.db, owner, "")
				if err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				previous := log.Writer()
				log.SetOutput(&output)
				defer log.SetOutput(previous)
				if traced {
					ctx = context.WithValue(managementTraceContext(ctx), consoleRequestIDKey{}, "target-error-123")
				}
				if tc.cancel {
					cancel()
				}
				err = s.readSubAccountDetailsWithReadersFrom(ctx, s.control.db, owner, "", 0, "", true, true, accounts, nil, sharedQuality{}, nil, nil, true, nil)
				return accounts, err, output.String()
			}
			want, wantErr, _ := read(false)
			got, gotErr, trace := read(true)
			if !reflect.DeepEqual(got, want) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || fmt.Sprint(errors.Unwrap(gotErr)) != fmt.Sprint(errors.Unwrap(wantErr)) {
				t.Fatal("tracing changed partial rows or original typed error", wantErr, gotErr)
			}
			if tc.column != "" && tc.name != "null" && gotErr == nil {
				t.Fatal("invalid target JSON did not exercise decoder failure")
			}
			if strings.Count(trace, "stage=target_rows ") != 1 || !strings.Contains(trace, "request_id=target-error-123") || strings.Contains(trace, "private-value") || strings.Contains(trace, "SELECT") {
				t.Fatal("error trace missing, duplicated, or exposed values", trace)
			}
		})
	}
}

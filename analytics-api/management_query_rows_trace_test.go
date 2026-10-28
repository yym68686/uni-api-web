package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
)

func TestManagementRowsAdvanceTracePreservesValuesAndErrors(t *testing.T) {
	db := migrationTestDB(t)
	for _, tc := range []struct {
		name, query        string
		cancel, closeEarly bool
	}{
		{name: "rows", query: `SELECT i FROM generate_series(1,5) i ORDER BY i`},
		{name: "empty", query: `SELECT 1 WHERE false`},
		{name: "server_error", query: `SELECT 10/(3-i) FROM generate_series(1,5) i`},
		{name: "scan_error", query: `SELECT 'private-row-value'`},
		{name: "closed_rows", query: `SELECT i FROM generate_series(1,5) i ORDER BY i`, closeEarly: true},
		{name: "canceled", query: `SELECT 1`, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := func(traced bool) ([]int, string, string) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if traced {
					ctx = context.WithValue(managementTraceContext(ctx), consoleRequestIDKey{}, "row-trace-123")
				}
				if tc.cancel {
					cancel()
				}
				var output bytes.Buffer
				previous := log.Writer()
				log.SetOutput(&output)
				defer log.SetOutput(previous)
				trace := newManagementRowsTrace(ctx, "page_receipts")
				result := []int{}
				err := func() error {
					defer trace.finish()
					rows, err := db.QueryContext(ctx, tc.query)
					if err != nil {
						return err
					}
					defer rows.Close()
					trace.queryReady()
					for trace.next(rows) {
						var value int
						if err := rows.Scan(&value); err != nil {
							return err
						}
						start := trace.decodeStart()
						result = append(result, value)
						trace.decoded(start, 0)
						if tc.closeEarly {
							rows.Close()
						}
					}
					return rows.Err()
				}()
				if trace != nil && trace.rows != len(result) {
					t.Fatal("trace changed the completed row count")
				}
				return result, fmt.Sprint(err), output.String()
			}
			want, wantErr, plainLog := read(false)
			got, gotErr, tracedLog := read(true)
			if !reflect.DeepEqual(got, want) || gotErr != wantErr || plainLog != "" {
				t.Fatal("trace changed rows, errors, or untraced logging")
			}
			if !strings.Contains(tracedLog, "advance_ms=") || !strings.Contains(tracedLog, "request_id=row-trace-123") {
				t.Fatal("missing safe row-advance timing")
			}
			for _, value := range []string{tc.query, "private-row-value", "generate_series", "SELECT", "FROM"} {
				if strings.Contains(tracedLog, value) {
					t.Fatal("diagnostic exposed SQL or values")
				}
			}
		})
	}
}

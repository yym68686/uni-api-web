package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestDatabaseDiagnosticsClassifiesBoundedMaintenanceWithoutErrorText(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{fmt.Errorf("wrapped: %w", errBillingBackfillHeadroom), "history_capacity_unavailable"},
		{errHistoryCapacity, "history_capacity_unavailable"},
		{errBillingBackfillBusy, "maintenance_deferred"},
		{errSubSummaryBackfillBusy, "maintenance_deferred"},
		{errBillingProjectionMismatch, "projection_mismatch"},
		{context.DeadlineExceeded, "deadline_exceeded"},
		{&pgconn.PgError{Code: "53400", Message: "private SQL and parameters"}, "database_resource_limit"},
		{errors.New("private connection configuration"), "unclassified_error"},
	} {
		if got := databaseErrorClass(test.err); got != test.want {
			t.Fatalf("got %q, want %q", got, test.want)
		}
	}
}

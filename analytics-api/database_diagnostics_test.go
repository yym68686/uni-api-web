package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
		{errReceiptRetentionIndexRequired, "maintenance_deferred"},
		{errBillingProjectionMismatch, "projection_mismatch"},
		{errClaimProjectionMismatch, "projection_mismatch"},
		{fmt.Errorf("private query: %w", errManagementProjectionHeadroom), "history_capacity_unavailable"},
		{fmt.Errorf("private account: %w", errManagementPreparationBusy), "maintenance_deferred"},
		{fmt.Errorf("private model: %w", errManagementProjectionMismatch), "projection_mismatch"},
		{context.DeadlineExceeded, "deadline_exceeded"},
		{fmt.Errorf("private query: %w", io.ErrUnexpectedEOF), "database_connection_lost"},
		{io.EOF, "database_connection_lost"},
		{&pgconn.PgError{Code: "57P02", Message: "database crash details"}, "database_connection_lost"},
		{&pgconn.PgError{Code: "53400", Message: "private SQL and parameters"}, "database_resource_limit"},
		{errors.New("private connection configuration"), "unclassified_error"},
	} {
		if got := databaseErrorClass(test.err); got != test.want {
			t.Fatalf("got %q, want %q", got, test.want)
		}
	}
}

package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
)

func databaseErrorClass(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errHistoryCapacity) || errors.Is(err, errBillingBackfillHeadroom) {
		return "history_capacity_unavailable"
	}
	if errors.Is(err, errBillingBackfillBusy) || errors.Is(err, errSubSummaryBackfillBusy) || errors.Is(err, errReceiptRetentionIndexRequired) {
		return "maintenance_deferred"
	}
	if errors.Is(err, errBillingProjectionMismatch) {
		return "projection_mismatch"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return "unclassified_error"
	}
	switch pg.Code {
	case "53200":
		return "database_out_of_memory"
	case "53100":
		return "database_disk_full"
	case "53400":
		return "database_resource_limit"
	case "57014":
		return "database_query_canceled"
	case "55P03":
		return "database_lock_timeout"
	case "40001", "40P01":
		return "database_transaction"
	case "23505", "23503", "23514":
		return "database_constraint"
	default:
		return "database_error"
	}
}

package main

import (
	"errors"
	"fmt"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

// Driver messages can contain SQL, paths and fact contents. Log only the
// driver's typed category; do not copy its message into telemetry.
func databaseErrorClass(err error) string {
	var dbErr *duckdb.Error
	if !errors.As(err, &dbErr) {
		return ""
	}
	switch dbErr.Type {
	case duckdb.ErrorTypeOutOfMemory:
		return "database_out_of_memory"
	case duckdb.ErrorTypeConstraint:
		return "database_constraint"
	case duckdb.ErrorTypeTransaction:
		return "database_transaction"
	case duckdb.ErrorTypeIO:
		return "database_io"
	case duckdb.ErrorTypeFatal:
		return "database_fatal"
	case duckdb.ErrorTypeInternal:
		return "database_internal"
	case duckdb.ErrorTypeBinder:
		return "database_binder"
	default:
		return fmt.Sprintf("database_type_%d", dbErr.Type)
	}
}

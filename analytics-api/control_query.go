package main

import (
	"context"
	"database/sql"
)

// Both *sql.DB and *sql.Tx implement this contract. Interactive management
// queries pass a transaction explicitly so enrichment cannot escape its
// snapshot through a pooled connection.
type controlQueryDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

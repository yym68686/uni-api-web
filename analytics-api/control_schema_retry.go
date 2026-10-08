package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Each schema batch is one implicit PostgreSQL transaction. A deadlock aborts
// that entire batch, so replay only this idempotent batch after rollback. Never
// retry arbitrary statements or keep locks while waiting between attempts.
func execControlSchema(ctx context.Context, db *sql.DB, schema string) error {
	return retryControlSchema(ctx, func() error { _, err := db.ExecContext(ctx, schema); return err })
}
func retryControlSchema(ctx context.Context, execute func() error) error {
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := execute()
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "40P01" || attempt >= 5 {
			return err
		}
		log.Printf("event=control_schema_retry attempt=%d sqlstate=%s", attempt, pg.Code)
		timer := time.NewTimer(time.Duration(attempt) * 200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

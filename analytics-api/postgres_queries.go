package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"log"
	"strconv"
	"strings"
	"sync"
)

// Analytics predicates use positional ? parameters. Bind them without touching
// quoted SQL literals or identifiers. Control queries already use native $n.
func postgresQuery(query string) string {
	var out strings.Builder
	parameter := 0
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c == '\'' || c == '"' {
			quote := c
			out.WriteByte(c)
			for i++; i < len(query); i++ {
				out.WriteByte(query[i])
				if query[i] == quote {
					if i+1 < len(query) && query[i+1] == quote {
						i++
						out.WriteByte(query[i])
					} else {
						break
					}
				}
			}
		} else if c == '?' {
			parameter++
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(parameter))
		} else {
			out.WriteByte(c)
		}
	}
	return out.String()
}

type analyticsDB struct {
	*sql.DB
	snapshot *sql.Tx
}
type analyticsTx struct{ *sql.Tx }
type analyticsConn struct{ *sql.Conn }

func (d *analyticsDB) Exec(q string, args ...any) (sql.Result, error) {
	return d.ExecContext(context.Background(), q, args...)
}
func (d *analyticsDB) ExecContext(c context.Context, q string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(c, postgresQuery(q), args...)
}
func (d *analyticsDB) Query(q string, args ...any) (*sql.Rows, error) {
	return d.QueryContext(context.Background(), q, args...)
}
func (d *analyticsDB) QueryContext(c context.Context, q string, args ...any) (*sql.Rows, error) {
	if d.snapshot != nil {
		return d.snapshot.QueryContext(c, postgresQuery(q), args...)
	}
	return d.DB.QueryContext(c, postgresQuery(q), args...)
}
func (d *analyticsDB) QueryRow(q string, args ...any) *sql.Row {
	return d.QueryRowContext(context.Background(), q, args...)
}
func (d *analyticsDB) QueryRowContext(c context.Context, q string, args ...any) *sql.Row {
	if d.snapshot != nil {
		return d.snapshot.QueryRowContext(c, postgresQuery(q), args...)
	}
	return d.DB.QueryRowContext(c, postgresQuery(q), args...)
}
func (d *analyticsDB) Begin() (*analyticsTx, error) { return d.BeginTx(context.Background(), nil) }
func (d *analyticsDB) BeginTx(c context.Context, o *sql.TxOptions) (*analyticsTx, error) {
	t, e := d.DB.BeginTx(c, o)
	if e != nil {
		return nil, e
	}
	return &analyticsTx{t}, nil
}

// A finished analytical transaction can leave a large private allocation in its
// PostgreSQL backend even when the session is idle. Lease the connection until
// rollback, then retire that backend instead of returning it to the shared pool.
// Ingestion and control connections retain their ordinary pool lifetimes.
func (d *analyticsDB) analyticalSnapshot(ctx context.Context, options *sql.TxOptions) (*analyticsTx, func(), error) {
	conn, err := d.DB.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	retire := func() {
		// database/sql discards the connection on ErrBadConn. This happens only
		// after the read transaction is finished, never during a statement, and
		// the sentinel is not returned to callers or used to replay any query.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
	}
	var backendPID int
	if err = conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&backendPID); err != nil {
		retire()
		return nil, nil, err
	}
	tx, err := conn.BeginTx(ctx, options)
	if err != nil {
		retire()
		return nil, nil, err
	}
	log.Printf("event=analytics_session_acquired backend_pid=%d request_id=%s", backendPID, consoleRequestID(ctx))
	var once sync.Once
	return &analyticsTx{tx}, func() {
		once.Do(func() {
			_ = tx.Rollback()
			retire()
			log.Printf("event=analytics_session_retired backend_pid=%d request_id=%s", backendPID, consoleRequestID(ctx))
		})
	}, nil
}
func (d *analyticsDB) Conn(c context.Context) (*analyticsConn, error) {
	v, e := d.DB.Conn(c)
	if e != nil {
		return nil, e
	}
	return &analyticsConn{v}, nil
}
func (t *analyticsTx) Exec(q string, args ...any) (sql.Result, error) {
	return t.ExecContext(context.Background(), q, args...)
}
func (t *analyticsTx) ExecContext(c context.Context, q string, args ...any) (sql.Result, error) {
	return t.Tx.ExecContext(c, postgresQuery(q), args...)
}
func (t *analyticsTx) Query(q string, args ...any) (*sql.Rows, error) {
	return t.QueryContext(context.Background(), q, args...)
}
func (t *analyticsTx) QueryContext(c context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.Tx.QueryContext(c, postgresQuery(q), args...)
}
func (t *analyticsTx) QueryRow(q string, args ...any) *sql.Row {
	return t.QueryRowContext(context.Background(), q, args...)
}
func (t *analyticsTx) QueryRowContext(c context.Context, q string, args ...any) *sql.Row {
	return t.Tx.QueryRowContext(c, postgresQuery(q), args...)
}
func (c *analyticsConn) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return c.Conn.ExecContext(ctx, postgresQuery(q), args...)
}
func (c *analyticsConn) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return c.Conn.QueryContext(ctx, postgresQuery(q), args...)
}
func (c *analyticsConn) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return c.Conn.QueryRowContext(ctx, postgresQuery(q), args...)
}
func (c *analyticsConn) BeginTx(ctx context.Context, o *sql.TxOptions) (*analyticsTx, error) {
	t, e := c.Conn.BeginTx(ctx, o)
	if e != nil {
		return nil, e
	}
	return &analyticsTx{t}, nil
}

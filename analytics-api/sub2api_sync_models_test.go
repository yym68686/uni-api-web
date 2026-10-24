package main

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
	"time"
)

func TestSyncModelInitializationRevisionLockOrder(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		name := "batched"
		if legacy {
			name = "legacy_deadlock_reproduction"
		}
		t.Run(name, func(t *testing.T) {
			db := migrationTestDB(t)
			db.SetMaxOpenConns(8)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if err := migrateControlSchema(ctx, db, controlMigrations()); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"sync", "writer"} {
				if _, err := db.ExecContext(ctx, `INSERT INTO console_sub_accounts(id,owner,name,base,email,encrypted_auth) VALUES($1,$1,$1,'https://fixture.test',$1,'')`, id); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `INSERT INTO console_sub_targets(account_id,group_id,name,platform) VALUES($1,1,'g','openai')`, id); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `INSERT INTO console_sub_models(account_id,group_id,model,state) VALUES($1,1,'existing','running')`, id); err != nil {
					t.Fatal(err)
				}
			}
			a, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Rollback()
			b, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Rollback()
			var pid int
			if err = a.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			// Result writes acquire the row-summary epoch before source counters.
			if _, err = b.ExecContext(ctx, `SELECT revision FROM console_management_view_epoch WHERE relation_name='console_sub_model_summaries' FOR UPDATE`); err != nil {
				t.Fatal(err)
			}
			if legacy {
				if _, err = a.ExecContext(ctx, `INSERT INTO console_sub_models(account_id,group_id,model,state) VALUES('sync',1,'existing','queued') ON CONFLICT(account_id,group_id,model) DO UPDATE SET state='queued',message=''`); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() {
				var e error
				if legacy {
					_, e = a.ExecContext(ctx, `INSERT INTO console_sub_models(account_id,group_id,model,state) VALUES('sync',1,'new','queued')`)
				} else {
					e = queueSubSyncModels(withSubSyncModels(ctx, []string{"existing", "new"}), a, "sync", []subRemoteGroup{{ID: 1}}, nil)
				}
				if e == nil {
					e = a.Commit()
				} else {
					_ = a.Rollback()
				}
				done <- e
			}()
			// Wait for the exact lock edge, not an assumed scheduling delay.
			for {
				var waiting bool
				if err = db.QueryRowContext(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err = <-done:
					t.Fatalf("writer finished before lock edge: %v", err)
				default:
				}
				time.Sleep(5 * time.Millisecond)
			}
			_, errB := b.ExecContext(ctx, `UPDATE console_sub_models SET state='done',result='{"model":"existing","checked_at":2,"availability":{"status":"success"}}' WHERE account_id='writer'`)
			if errB == nil {
				errB = b.Commit()
			} else {
				_ = b.Rollback()
			}
			errA := <-done
			if legacy {
				deadlock := func(e error) bool { var pg *pgconn.PgError; return errors.As(e, &pg) && pg.Code == "40P01" }
				if !deadlock(errA) && !deadlock(errB) {
					t.Fatalf("expected exact old cycle, got %v / %v", errA, errB)
				}
				t.Log("old multi-statement initialization reproduced SQLSTATE 40P01")
			} else {
				if errA != nil || errB != nil {
					t.Fatalf("batched writers failed: %v / %v", errA, errB)
				}
				var queued int
				if err = db.QueryRowContext(ctx, `SELECT count(*) FROM console_sub_models WHERE account_id='sync' AND state='queued'`).Scan(&queued); err != nil || queued != 2 {
					t.Fatal(queued, err)
				}
				var unused sql.NullString
				if err = db.QueryRowContext(ctx, `SELECT result::text FROM console_sub_models WHERE account_id='sync' AND model='existing'`).Scan(&unused); err != nil || unused.Valid {
					t.Fatal("result changed", err)
				}
			}
		})
	}
}

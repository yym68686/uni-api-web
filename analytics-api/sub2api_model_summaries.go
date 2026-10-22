package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

type subModelSummaryCursor struct {
	account string
	group   int64
	model   string
}

var errSubSummaryBackfillBusy = errors.New("model summary backfill busy")

// Each transaction visits at most 128 historical identities. A single database-wide
// worker is active per batch, including during rolling releases. The cursor
// scans the metadata once, rather than re-scanning an increasing filled prefix.
func (s *controlStore) backfillSubModelSummaries(ctx context.Context, after subModelSummaryCursor) (next subModelSummaryCursor, inserted int, done bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return after, 0, false, err
	}
	defer tx.Rollback()
	var locked bool
	if err = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(8395014)`).Scan(&locked); err != nil {
		return after, 0, false, err
	}
	if !locked {
		return after, 0, false, errSubSummaryBackfillBusy
	}
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='2s'; SET LOCAL lock_timeout='100ms'`); err != nil {
		return after, 0, false, err
	}
	// DO NOTHING is intentional: a concurrent old/new writer may have already
	// published a newer result via the trigger. Never overwrite that projection
	// with the older SELECT snapshot. Deletes remain protected by the FK.
	err = tx.QueryRowContext(ctx, `WITH batch AS MATERIALIZED (
 SELECT m.account_id,m.group_id,m.model FROM console_sub_models m
 WHERE (m.account_id,m.group_id,m.model)>($1,$2,$3)
 ORDER BY m.account_id,m.group_id,m.model LIMIT 128
), filled AS (
 INSERT INTO console_sub_model_summaries(account_id,group_id,model,result)
 SELECT m.account_id,m.group_id,m.model,console_sub_model_summary_v1(m.result)
 FROM batch b JOIN console_sub_models m USING(account_id,group_id,model)
 WHERE NOT EXISTS (SELECT 1 FROM console_sub_model_summaries ms WHERE ms.account_id=b.account_id AND ms.group_id=b.group_id AND ms.model=b.model)
 ON CONFLICT(account_id,group_id,model) DO NOTHING RETURNING 1
)
SELECT account_id,group_id,model,(SELECT count(*) FROM filled) FROM batch
ORDER BY account_id DESC,group_id DESC,model DESC LIMIT 1`, after.account, after.group, after.model).Scan(&next.account, &next.group, &next.model, &inserted)
	if errors.Is(err, sql.ErrNoRows) {
		return after, 0, true, tx.Commit()
	}
	if err != nil {
		return after, 0, false, err
	}
	return next, inserted, false, tx.Commit()
}

func (s *Service) subModelSummaryBackfillLoop(ctx context.Context) {
	var cursor subModelSummaryCursor
	started, lastLog := time.Now(), time.Now()
	rows := 0
	for ctx.Err() == nil {
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		next, count, done, err := s.control.backfillSubModelSummaries(callCtx, cursor)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if time.Since(lastLog) >= 10*time.Second {
				log.Printf("sub_model_summary_backfill status=retry rows=%d elapsed_ms=%d class=%s", rows, time.Since(started).Milliseconds(), databaseErrorClass(err))
				lastLog = time.Now()
			}
			if !waitStartup(ctx, time.Second) {
				return
			}
			continue
		}
		cursor, rows = next, rows+count
		if done || time.Since(lastLog) >= 10*time.Second {
			log.Printf("sub_model_summary_backfill complete=%t rows=%d elapsed_ms=%d", done, rows, time.Since(started).Milliseconds())
			lastLog = time.Now()
		}
		if done || !waitStartup(ctx, 100*time.Millisecond) {
			return
		}
	}
}

package main

import (
	"context"
	"database/sql"
	"log"
	"sync/atomic"
	"time"
)

// Authenticated opt-in only; ordinary reads execute no diagnostic SQL. Observe
// only the existing bounded primary-key SELECT, once per request/process minute.
// No query replay, EXPLAIN, execution-mode change, or relation data read is added.
type managementModelReadKey struct{}
type managementModelReadRequest struct{ used atomic.Bool }

var managementModelReadAdmission managementReceiptPlanLimiter

type managementModelReadObservation struct {
	tx       *sql.Tx
	before   map[string]managementPreparedPlanCounts
	access   managementReceiptAccess
	metadata time.Duration
	ids      int
}

func beginManagementModelReadObservation(ctx context.Context, db controlQueryDB, query string, ids int) *managementModelReadObservation {
	request, _ := ctx.Value(managementModelReadKey{}).(*managementModelReadRequest)
	tx, pinned := db.(*sql.Tx)
	if request == nil || !pinned || tx == nil || query != managementModelSeekSQL || ids < 1 || ids > 4096 || ctx.Err() != nil ||
		!request.used.CompareAndSwap(false, true) || !managementModelReadAdmission.admit(time.Now()) {
		return nil
	}
	start := time.Now()
	if _, err := tx.ExecContext(ctx, `SAVEPOINT management_model_read_observation`); err != nil {
		return nil
	}
	before, err := managementReceiptPreparedPlans(ctx, tx, query)
	var access managementReceiptAccess
	if err == nil {
		access, err = managementSignalAccessSnapshot(ctx, tx, "console_management_model_signal")
	}
	if err != nil {
		finishManagementModelReadSavepoint(ctx, tx, false)
		log.Printf("management_model_access available=false phase=before request_id=%s", consoleRequestID(ctx))
		return nil
	}
	return &managementModelReadObservation{tx: tx, before: before, access: access, ids: ids, metadata: time.Since(start)}
}

func finishManagementModelReadSavepoint(ctx context.Context, tx *sql.Tx, successful bool) {
	if ctx.Err() != nil {
		return
	}
	if !successful {
		_, _ = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT management_model_read_observation`)
	}
	_, _ = tx.ExecContext(ctx, `RELEASE SAVEPOINT management_model_read_observation`)
}

// Called only after the unchanged SELECT and decoder succeed. Errors and
// cancellation retain their original path and are never retried or recovered.
func (o *managementModelReadObservation) finish(ctx context.Context, rows int) {
	if o == nil || ctx.Err() != nil {
		return
	}
	start := time.Now()
	access, err := managementSignalAccessSnapshot(ctx, o.tx, "console_management_model_signal")
	delta := "available=false"
	var after map[string]managementPreparedPlanCounts
	if err == nil {
		delta = managementReceiptAccessDelta(o.access, access)
		after, err = managementReceiptPreparedPlans(ctx, o.tx, managementModelSeekSQL)
	}
	mode, generic, custom := "unavailable", int64(0), int64(0)
	if err == nil {
		mode, generic, custom = managementReceiptPlanDelta(o.before, after)
	}
	finishManagementModelReadSavepoint(ctx, o.tx, err == nil)
	log.Printf("management_model_plan cache_mode=%s generic_delta=%d custom_delta=%d candidates=%d rows=%d metadata_ms=%d scope=same_connection_execution_delta request_id=%s",
		mode, generic, custom, o.ids, rows, (o.metadata + time.Since(start)).Milliseconds(), consoleRequestID(ctx))
	log.Printf("management_model_access %s scope=current_transaction_model_relation_delta request_id=%s", delta, consoleRequestID(ctx))
}

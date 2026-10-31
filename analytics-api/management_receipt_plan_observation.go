package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync/atomic"
	"time"
)

// Explicit authenticated diagnostic reads only. No query is replayed, no plan
// mode is changed, and ordinary management reads execute no additional SQL.
// pg_prepared_statements is local to the SAME pinned connection, so an exact
// one-execution delta identifies this read's actual plan mode, unlike EXPLAIN
// of an unrelated connection or a cumulative server-wide statistic.
type managementReceiptPlanKey struct{}
type managementReceiptHydrationKey struct{}
type managementReceiptPlanRequest struct {
	used          atomic.Bool
	hydrationOnly bool
}
type managementReceiptPlanLimiter struct{ at atomic.Int64 }

var managementReceiptPlanAdmission managementReceiptPlanLimiter

func (l *managementReceiptPlanLimiter) admit(now time.Time) bool {
	previous := l.at.Load()
	return (previous == 0 || now.UnixNano()-previous >= int64(time.Minute)) && l.at.CompareAndSwap(previous, now.UnixNano())
}

type managementPreparedPlanCounts struct{ generic, custom int64 }

func managementReceiptPreparedPlans(ctx context.Context, tx *sql.Tx, query string) (map[string]managementPreparedPlanCounts, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name,generic_plans,custom_plans FROM pg_catalog.pg_prepared_statements WHERE statement=$1 LIMIT 9`, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]managementPreparedPlanCounts{}
	for rows.Next() {
		var name string
		var counts managementPreparedPlanCounts
		if err := rows.Scan(&name, &counts.generic, &counts.custom); err != nil {
			return nil, err
		}
		out[name] = counts
		if len(out) > 8 {
			return nil, errors.New("prepared statement observation exceeds bound")
		}
	}
	return out, rows.Err()
}

func managementReceiptPlanDelta(before, after map[string]managementPreparedPlanCounts) (string, int64, int64) {
	var generic, custom int64
	for name, counts := range after {
		old := before[name]
		if counts.generic < old.generic || counts.custom < old.custom {
			return "unavailable", 0, 0
		}
		generic += counts.generic - old.generic
		custom += counts.custom - old.custom
	}
	for name := range before {
		if _, found := after[name]; !found {
			return "unavailable", 0, 0
		}
	}
	if generic == 1 && custom == 0 {
		return "generic", generic, custom
	}
	if custom == 1 && generic == 0 {
		return "custom", generic, custom
	}
	return "unavailable", generic, custom
}

type managementReceiptPlanObservation struct {
	tx        *sql.Tx
	query     string
	before    map[string]managementPreparedPlanCounts
	access    managementReceiptAccess
	ids       int
	args      []any
	metadata  time.Duration
	rows      int
	succeeded bool
	phase     string
}

func managementReceiptHydrationObservationContext(ctx context.Context) context.Context {
	if request, _ := ctx.Value(managementReceiptPlanKey{}).(*managementReceiptPlanRequest); request != nil {
		return context.WithValue(ctx, managementReceiptHydrationKey{}, true)
	}
	return ctx
}

func beginManagementReceiptPlanObservation(ctx context.Context, db controlQueryDB, query string, args []any, ids int) *managementReceiptPlanObservation {
	request, _ := ctx.Value(managementReceiptPlanKey{}).(*managementReceiptPlanRequest)
	hydrating, _ := ctx.Value(managementReceiptHydrationKey{}).(bool)
	tx, pinned := db.(*sql.Tx)
	if request == nil || request.hydrationOnly && !hydrating || !pinned || tx == nil || ids < 1 || ids > 4096 || query != managementReceiptSelectedStreamSQL || ctx.Err() != nil ||
		!request.used.CompareAndSwap(false, true) || !managementReceiptPlanAdmission.admit(time.Now()) {
		return nil
	}
	start := time.Now()
	// Optional catalog failures must not poison the business transaction. The
	// savepoint surrounds only this read-only diagnostic and the observed SELECT.
	if _, err := tx.ExecContext(ctx, `SAVEPOINT management_receipt_plan_observation`); err != nil {
		return nil
	}
	before, err := managementReceiptPreparedPlans(ctx, tx, query)
	if err != nil {
		finishManagementReceiptPlanSavepoint(ctx, tx, false)
		log.Printf("management_receipt_plan cache_mode=unavailable phase=before request_id=%s", consoleRequestID(ctx))
		return nil
	}
	access, err := managementReceiptAccessSnapshot(ctx, tx)
	if err != nil {
		finishManagementReceiptPlanSavepoint(ctx, tx, false)
		log.Printf("management_receipt_access available=false phase=before request_id=%s", consoleRequestID(ctx))
		return nil
	}
	phase := "unspecified"
	if hydrating {
		phase = "hydrate"
	}
	return &managementReceiptPlanObservation{tx: tx, query: query, args: args, before: before, access: access, ids: ids, metadata: time.Since(start), phase: phase}
}

func finishManagementReceiptPlanSavepoint(ctx context.Context, tx *sql.Tx, successful bool) {
	if ctx.Err() != nil {
		return // The caller retains its original cancellation and rollback path.
	}
	if !successful {
		_, _ = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT management_receipt_plan_observation`)
	}
	_, _ = tx.ExecContext(ctx, `RELEASE SAVEPOINT management_receipt_plan_observation`)
}

func (o *managementReceiptPlanObservation) finish(ctx context.Context) {
	if o == nil || !o.succeeded || ctx.Err() != nil {
		return // Never recover, retry, or mask a failed business query.
	}
	start := time.Now()
	access, err := managementReceiptAccessSnapshot(ctx, o.tx)
	accessDelta := "available=false"
	var after map[string]managementPreparedPlanCounts
	if err == nil {
		accessDelta = managementReceiptAccessDelta(o.access, access)
		after, err = managementReceiptPreparedPlans(ctx, o.tx, o.query)
	}
	mode, generic, custom := "unavailable", int64(0), int64(0)
	plan := "unavailable"
	if err == nil {
		mode, generic, custom = managementReceiptPlanDelta(o.before, after)
		if mode != "unavailable" {
			plan, err = managementReceiptEstimatedPlan(ctx, o.tx, o.args)
		}
	}
	finishManagementReceiptPlanSavepoint(ctx, o.tx, err == nil)
	log.Printf("management_receipt_plan cache_mode=%s generic_delta=%d custom_delta=%d ids=%d rows=%d metadata_ms=%d phase=%s scope=same_connection_execution_delta request_id=%s",
		mode, generic, custom, o.ids, o.rows, (o.metadata + time.Since(start)).Milliseconds(), o.phase, consoleRequestID(ctx))
	log.Printf("management_receipt_plan_shape %s scope=same_connection_parameterized_estimate_not_execution_tree request_id=%s", plan, consoleRequestID(ctx))
	log.Printf("management_receipt_access %s phase=%s scope=current_transaction_receipt_relation_delta request_id=%s", accessDelta, o.phase, consoleRequestID(ctx))
}

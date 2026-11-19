package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"slices"
)

// The existing transactionally maintained signals contain the whole-model
// validity flag plus the scalars required for the operations table. Correlate
// both sides to the group so PostgreSQL can use both primary-key prefixes
// instead of doing a separate random signal lookup for every model.
const managementPageOverviewSQL = `WITH wanted AS MATERIALIZED (
 SELECT w.account_id,w.group_id FROM jsonb_to_recordset($2::jsonb) AS w(account_id text,group_id bigint)
 JOIN console_sub_accounts a ON a.id=w.account_id WHERE a.owner=$1)
 SELECT w.account_id,w.group_id,
   ($4::boolean OR NOT EXISTS (SELECT 1 FROM console_sub_check_queue q WHERE q.account_id=w.account_id AND q.group_id=w.group_id))
     AND COALESCE(g.valid,true),
   g.total,g.builtins,g.astra,g.success,g.warning,g.failed,g.untested,g.pending,g.interrupted,g.rejected,g.skipped,g.checked_at,g.busy
 FROM wanted w
 CROSS JOIN LATERAL (
 SELECT bool_and(COALESCE(s.valid,false)) AS valid,
   count(*) AS total,count(*) FILTER (WHERE m.model=ANY($3::text[])) AS builtins,
   COALESCE(bool_or(m.model='gpt-6-astra'),false) AS astra,
   count(*) FILTER (WHERE m.state NOT IN ('queued','running','interrupted','rejected','skipped','error') AND s.present AND s.availability='success') AS success,
   count(*) FILTER (WHERE m.state NOT IN ('queued','running','interrupted','rejected','skipped') AND s.present AND s.availability='success' AND s.terminal NOT IN ('','complete')) AS warning,
   count(*) FILTER (WHERE m.state NOT IN ('queued','running','interrupted','rejected','skipped') AND (m.state='error' OR s.present AND s.availability='error')) AS failed,
   count(*) FILTER (WHERE m.state NOT IN ('queued','running','interrupted','rejected','skipped','error') AND NOT s.present) AS untested,
   count(*) FILTER (WHERE m.state IN ('queued','running')) AS pending,
   count(*) FILTER (WHERE m.state='interrupted') AS interrupted,
   count(*) FILTER (WHERE m.state='rejected') AS rejected,
   count(*) FILTER (WHERE m.state='skipped') AS skipped,
   greatest(max(s.checked_at) FILTER (WHERE s.present),0) AS checked_at,
   COALESCE(jsonb_agg(jsonb_build_object('model',m.model,'state',m.state) ORDER BY m.model COLLATE "C")
     FILTER (WHERE m.state IN ('queued','running')),'[]'::jsonb) AS busy
 FROM (SELECT model,CASE WHEN state='interrupted' AND message IN ` + legacySiteRejectionSQL + ` THEN 'rejected' ELSE state END AS state FROM console_sub_models
       WHERE account_id=w.account_id AND group_id=w.group_id OFFSET 0) m
 LEFT JOIN (SELECT model,valid,present,checked_at,availability,terminal FROM console_management_model_signal
            WHERE account_id=w.account_id AND group_id=w.group_id OFFSET 0) s USING(model)
 OFFSET 0) g`

type managementPageGroupOverview struct {
	counts managementOverview
	legacy bool
	busy   []subModelResult
}

func readManagementPageOverview(ctx context.Context, db controlQueryDB, owner string, wanted []subBoundKey) (map[string]managementPageGroupOverview, error) {
	// Keep both reads on the caller's PostgreSQL snapshot. Standalone readers
	// (including fixtures) can always compute the exact live summary instead.
	tx, ok := db.(*sql.Tx)
	if !ok || len(wanted) == 0 {
		return computeManagementPageOverview(ctx, db, owner, wanted, false)
	}
	builtins := managementUnique(subModels)
	hits, err := readStoredManagementOverviews(ctx, tx, owner, wanted, managementOverviewCatalog(builtins))
	if err != nil || len(hits) == len(wanted) {
		return hits, err
	}
	missing := make([]subBoundKey, 0, len(wanted)-len(hits))
	for _, key := range wanted {
		if _, found := hits[qualityGroupID(key.AccountID, key.GroupID)]; !found {
			missing = append(missing, key)
		}
	}
	exact, err := computeManagementPageOverview(ctx, db, owner, missing, false)
	if err != nil {
		return nil, err
	}
	maps.Copy(hits, exact)
	return hits, nil
}

func computeManagementPageOverview(ctx context.Context, db controlQueryDB, owner string, wanted []subBoundKey, ignoreQueue bool) (map[string]managementPageGroupOverview, error) {
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT complete FROM console_management_projection_state WHERE singleton`).Scan(&complete); err != nil || !complete {
		return nil, err
	}
	raw, err := json.Marshal(wanted)
	if err != nil {
		return nil, err
	}
	builtins := managementUnique(subModels)
	legacyAstra := slices.Contains(builtins, "gpt-6-astra")
	trace := newManagementRowsTraceDerivedDecode(ctx, "page_overview_signals")
	defer trace.finish()
	rows, err := db.QueryContext(ctx, managementPageOverviewSQL, owner, string(raw), builtins, ignoreQueue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	trace.queryReady()
	result := make(map[string]managementPageGroupOverview, len(wanted))
	for trace.next(rows) {
		var account string
		var group int64
		var valid, astra bool
		var seen int
		var busy []byte
		var overview managementPageGroupOverview
		o := &overview.counts
		if err := rows.Scan(&account, &group, &valid, &o.Total, &seen, &astra, &o.Success, &o.Warning, &o.Failed,
			&o.Untested, &o.Pending, &o.Interrupted, &o.Rejected, &o.Skipped, &o.CheckedAt, &busy); err != nil {
			return nil, err
		}
		trace.counted(len(busy))
		if !valid {
			continue // The caller uses current full models for just this group.
		}
		if err := json.Unmarshal(busy, &overview.busy); err != nil {
			return nil, err
		}
		// Match managementModelChecks: synthesize missing built-ins as
		// untested; the legacy target result can replace a missing Astra.
		absent := len(builtins) - seen
		o.Total += absent
		o.Untested += absent
		overview.legacy = legacyAstra && !astra
		result[qualityGroupID(account, group)] = overview
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, ctx.Err()
}

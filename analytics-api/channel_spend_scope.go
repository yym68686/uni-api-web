package main

import (
	"strings"
)

// Only an in-window billing fact or completion can introduce an identity into
// the result. Look up other completions/bills for those exact identities before
// applying the final cutoff. In particular a late completion outside the window
// must move (or make ambiguous) an earlier billing observation.
func seekBillingSnapshot(f QueryFilter, from, to int64) (string, []any) {
	where, args := billingWhere(f, from, to)
	match := make([]string, 0, 10)
	identities := make([]string, 0, 10)
	for _, column := range strings.Split(billingIdentityColumns, ",") {
		match = append(match, "c."+column+"=i."+column)
		identities = append(identities, "coalesce(b."+column+",a."+column+") AS "+column)
	}
	q := `WITH seed AS MATERIALIZED (
 SELECT ` + billingColumns + ` FROM billing_facts WHERE ` + where + `),
 seed_identities AS MATERIALIZED (
 SELECT DISTINCT ` + billingIdentityColumns + ` FROM seed),
 companions AS MATERIALIZED (
 SELECT found.* FROM seed_identities i CROSS JOIN LATERAL (
  SELECT ` + billingColumns + ` FROM billing_facts c WHERE ` + strings.Join(match, " AND ") + `
  AND (c.at_ms<? OR c.at_ms>=?) OFFSET 0
 ) found),
 candidates AS MATERIALIZED (SELECT * FROM seed UNION ALL SELECT * FROM companions),
 completions AS (
 SELECT ` + billingIdentityColumns + `,max(at_ms) AS completed_at,count(*) AS n,
 count(*) FILTER(WHERE at_ms>=? AND at_ms<?) AS legacy_count
 FROM candidates WHERE kind='attempt' GROUP BY ` + billingIdentityColumns + `),
 bills AS (SELECT * FROM candidates WHERE kind='billing')
 SELECT b.event_id,` + strings.Join(identities, ",") + `,
 coalesce(a.completed_at,b.at_ms) AS at_ms,b.started_ms,b.upstream_base,b.upstream_key_hash,b.billing_request_ids,b.status,b.upstream_error_sha256,
 coalesce(a.n,0) AS completions,CASE WHEN b.event_id IS NULL THEN a.legacy_count ELSE 0 END AS legacy_count
 FROM bills b FULL JOIN completions a USING(` + billingIdentityColumns + `)
 WHERE (b.event_id IS NOT NULL AND coalesce(a.completed_at,b.at_ms)>=? AND coalesce(a.completed_at,b.at_ms)<?)
 OR (b.event_id IS NULL AND a.legacy_count>0)`
	return q, append(args, from*1000, to*1000, from*1000, to*1000, from*1000, to*1000)
}

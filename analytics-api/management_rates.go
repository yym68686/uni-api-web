package main

// Each side has at most one current-key candidate per account/group/model.
// Pair those candidates instead of sorting and globally windowing both full
// payloads. PostgreSQL still compares receipt IDs with its own collation.
// A missing/unsupported raw projection requests the complete legacy decoder,
// even when newer retained metadata wins. Equal identities with different
// payloads also fall back: the previous query does not define a tie winner.
const managementPreparedRateSQL = `WITH owned_targets AS MATERIALIZED (
 SELECT t.account_id,t.group_id,t.remote_key_id FROM console_sub_targets t JOIN console_sub_accounts a ON a.id=t.account_id WHERE a.owner=$1 AND ($2='' OR a.id=$2)),
 raw_latest AS (
 SELECT DISTINCT ON (u.account_id,u.group_id,u.model) u.id,u.account_id,u.group_id,u.model,u.started_at
 FROM console_sub_usage u JOIN owned_targets t ON t.account_id=u.account_id AND t.group_id=u.group_id AND t.remote_key_id=u.key_id
 WHERE u.status='matched' ORDER BY u.account_id,u.group_id,u.model,u.started_at DESC,u.id DESC),
 raw_values AS (
 SELECT l.*,p.rate_result AS result,NOT COALESCE(p.valid,false) AS requires_raw
 FROM raw_latest l LEFT JOIN console_management_receipt_signal p ON p.id=l.id AND p.account_id=l.account_id),
 retained AS (
 SELECT r.receipt_id AS id,r.account_id,r.group_id,r.model,r.started_at,r.result
 FROM console_sub_observed_rates r JOIN owned_targets t ON t.account_id=r.account_id AND t.group_id=r.group_id AND t.remote_key_id=r.key_id),
 combined AS (
 SELECT COALESCE(l.account_id,r.account_id) AS account_id,COALESCE(l.group_id,r.group_id) AS group_id,COALESCE(l.model,r.model) AS model,
 l.started_at AS raw_started_at,r.started_at AS retained_started_at,l.result AS raw_result,r.result AS retained_result,
 l.id IS NOT NULL AND (r.id IS NULL OR (l.started_at,l.id)>=(r.started_at,r.id)) AS use_raw,
 COALESCE(l.requires_raw,false) OR (l.id=r.id AND l.started_at=r.started_at AND l.result IS DISTINCT FROM r.result) AS requires_raw
 FROM raw_values l FULL JOIN retained r USING(account_id,group_id,model))
 SELECT account_id,group_id,model,CASE WHEN use_raw THEN raw_started_at ELSE retained_started_at END,
 CASE WHEN use_raw THEN raw_result ELSE retained_result END,COALESCE(requires_raw,false) FROM combined`

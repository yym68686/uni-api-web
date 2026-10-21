# Account progress and bounded result refresh

Submitting sync/check tasks is independent from refreshing their results. On
2026-10-21 the sync-selected POST completed with HTTP 202 in 32 ms, followed by
an account summary reading 122,791 model rows (82 MB of model result JSON).
The 25-second server write deadline cut off the response; Nginx recorded an
upstream premature close. Other list reads reached the browser's 30-second
deadline. Repeating the mutation is not a valid response to this read failure.

The existing `GET /v1/sub2api/accounts` supports:

- `X-Console-View: accounts`: only account metadata and progress. Optional
  `active_groups`/`completed_groups` counters use target metadata, never model
  payloads, receipt enrichment, or shared quality history. Queued checks still
  make the account appear queued. `X-Console-Account-Scope: v2` advertises scoped
  summaries and the separate rate snapshot, including on conditional responses.
- `X-Console-View: summary` with `?account_id=...`: all target/model evidence for
  that owned account. Missing and foreign accounts return 404. Model reads,
  queued checks, quality totals, and observed receipt rates respect that scope.
  `include_rates=0` omits only target-level observed rates; current per-model
  receipt evidence remains included. The original unscoped protocol and group
  detail route remain compatible.
- `X-Console-View: rates`: one owner-scoped snapshot of observed receipt rates,
  using the same latest-receipt and current-key selection as the original list.
  Production EXPLAIN showed no matched-receipt account index, so repeating this
  query per account would multiply its history scan. No schema change is needed.

The browser reads the account directory and fetches at most two account
summaries concurrently, retaining independent conditional cursors, and then
reads observed rates once. Either phase can fail without publishing incomplete
results. Both rates and models are retained in the complete reload cache. Only a
complete refresh replaces the channel results; one failed part cancels sibling
reads and preserves the last complete list. Status polling runs independently
and remains usable while results load. These reads are not one database-wide
point-in-time snapshot: accounts may progress between requests. A deleted
account causes the current refresh to fail safely and the next directory read
to reconcile membership.

An older backend without the capability header uses the original full-summary
protocol. A backend that ignores an account filter cannot silently turn an
unscoped multi-account response into one valid part. Reload caches store only
complete assembled results; every part is revalidated after reload.

After a successful mutation, account state and result reads are refreshed
separately. A result-read error states that accepted background tasks continue;
retry only issues GET requests. No timeout, probe concurrency, routing setting,
database schema, or paid model invocation is changed.

Validation includes owned-scope equivalence, foreign-account rejection, status
reads while the model table is locked in an isolated test database, bounded
client concurrency, failed-part cancellation, old-server compatibility, and
accepted POST followed by failed GET without resubmission. The opt-in scale
fixture checks all 115,675 model rows survive per-account reads exactly once.
On the development machine the full response was 63.1 MB; the largest scoped
response was 1.38 MB and took 113 ms. These measurements are not production
latency guarantees. Very large individual accounts may require finer paging.

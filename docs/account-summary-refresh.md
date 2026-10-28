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

Account metadata alone cannot establish a channel count. If the first result
or channel-directory read fails, the page shows an unavailable state and an
unknown count, and disables batch checks. It only reports no matching channels
after both result sources have loaded. A failed refresh with previous results
continues to show those results with the existing stale-data notice.

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

## Editor reads and reload snapshots

Model editors use `GET /v1/sub2api/channels?view=bindings&source_id=...`
for a live binding/revision without the unrelated receipt-rate scan. The
response declares `scope_source_id`; a source-scoped result must not imply that
all other sources are empty. The original full endpoint remains unchanged.
Editors and model drawers load only their bound group summaries through the
existing detail route with the `X-Console-View: summary` header and the
`include_rates=0` query parameter. Missing groups are represented as missing;
other failures remain errors. They do not request all accounts first. Save-time
revision and ownership checks are unchanged.

The bindings-only view overlaps the independent owner-scoped configured-binding
read with gateway reads, after validating the requested source. It adds at most
one database reader per request; the existing four-source gateway limit stays
unchanged. Both branches are joined before responding, including on cancellation
or a configured-binding failure. No partial success, cache, new query, credential
scope or write is introduced. These were already independent non-transactional
reads, not a claimed atomic cross-system snapshot. The full billing endpoint
retains its original serial read order.

`bindings_configured_read` measures the overlapped database branch. Its duration
overlaps `bindings_gateways`; do not sum them. The existing
`bindings_configured` timeline measures merge and any remaining join wait after
the gateways complete, not the entire overlapped database read. A local test
holds all three gateway replies and the configured-inventory table separately,
then observes the real database lock wait to prove overlap without a timing
threshold. Success, invalid inventory, cancellation and complete output are
checked against the unchanged full endpoint.

Successful add/replace writes close the editor immediately. Query invalidation
runs in the background; its completion is not part of the save acknowledgement.
Failed writes keep the draft and release the close button.

Reloads require successful live reads before publishing channel results. Persistent
snapshots are not restored into these queries. The earlier local cache benchmark
does not satisfy the one-second current-data requirement and is not acceptance
evidence. Scoped editor reads and immediate close after successful saves remain.

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

Traced management GET transport failures emit
`management_source_transport_failed` with an allowlisted phase, a fixed error
class and the existing request ID. DNS, connection, TLS, timeout and cancellation
classes inspect the retained typed HTTP cause, never error text; unrelated
database EOFs and HTTP/JSON failures are not source transport faults. The final
`management_query` class uses the same source classification. No source URL,
credential, query value or raw error is logged. Unknown paths, mutations and
untraced calls do not emit this diagnostic. This adds no request, retry,
resolution attempt or response change. An independent DNS probe is not evidence
of an earlier request's cause; use the matching request ID.

## Bounded database wait observation

`node scripts/observe-management-read.mjs --ssh-host HOST --namespace NAMESPACE
--pod DATABASE_POD` passively samples at most 16 active management database
transactions for 35 seconds by default. It emits fixed query categories, waits,
and PID/transaction-start timestamps, never SQL, parameters or raw logs. The
observer joins only the same PID **and** transaction-start millisecond to a
`management_database_session` event. Missing or multiple matches remain explicit;
cumulative backend I/O can lag and is not a per-query measurement.

Fugue CLI logs are preferred. If its API is unavailable, an explicit optional
`--logs-pod ANALYTICS_POD` allows one read-only SSH log read on the same declared
host and namespace: five minutes, at most 2,000 lines per container, 4 MiB and
10 seconds. No pod discovery, credential read, business replay, retry loop or
production change occurs. Failed or truncated reads remain unavailable, not
matched. `logs_source` identifies `fugue`, `ssh` or `unavailable`; non-session log
contents and subprocess errors never leave the tool. No fallback runs without
that explicit pod, or when the CLI log source is available.

Successful add/replace writes close the editor immediately. Query invalidation
runs in the background; its completion is not part of the save acknowledgement.
Failed writes keep the draft and release the close button.

Paged snapshot reads can reuse verified receipt summaries already read by their
predicate stream. This is request-local evidence, not another response cache:
only the same owner and the exact pinned repeatable-read transaction can use
it. Valid summaries supply all five management usage fields; unsupported,
unread and absent IDs keep the original raw reader and its typed error behavior.
No receipt attaches until the remaining raw read and cancellation checks
succeed. Detail/action reads and paging-directory hydration keep their existing
path. The holder is cleared after hydration and never enters public JSON or
retained pages. `management_receipt_reuse` logs only reused/raw ID counts and
the request ID. A scoped model filter can overlap only a small fraction of the
page's complete model receipts; count reduction is not an end-to-end speed claim.

Reloads require successful live reads before publishing channel results. Persistent
snapshots are not restored into these queries. The earlier local cache benchmark
does not satisfy the one-second current-data requirement and is not acceptance
evidence. Scoped editor reads and immediate close after successful saves remain.

### Fresh management response JSON reuse

Fresh v3 pages serialize their complete column representation once for HTTP,
then independently decode those same uncompressed bytes for the existing
bounded computed-page store. Transport compression never mutates the input.
Current-validation headers may move `read_at_ms` to a response header; retention
restores only that typed top-level integer and accounts for its original exact
decimal byte length before the unchanged 16 MiB admission check. No JSON text
patching, shallow retention, additional lifetime, proof relaxation or SQL change
is involved. Missing proofs, invalid JSON, failed writes and canceled writes
retain their existing rejection behavior. V1/v2 clients and retained-page reads
keep their existing paths. This removes one O(response bytes) serialization,
not database reads or all management latency; local measurements are not E2E
acceptance.

The same transaction-local verified receipt lookup also applies to the compact
account-summary stage that precedes page hydration. Only the management query
supplies this reader; ordinary account/detail/action calls still read complete
raw usage. Catalog-only, fallback, unknown, unsupported and foreign evidence
retain their original path. Normalization still follows attachment, and any
receipt error keeps its existing wrapper and stops the rates stage. Reuse logs
include only a fixed `phase=account` or `phase=page` plus counts and request ID;
overlap and production latency must be measured separately in both stages.

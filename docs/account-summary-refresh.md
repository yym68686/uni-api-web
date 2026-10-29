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
succeed. Detail/action reads keep their existing path. The holder is cleared
after hydration and never enters public JSON or
retained pages. `management_receipt_reuse` logs only reused/raw ID counts and
the request ID. A scoped model filter can overlap only a small fraction of the
page's complete model receipts; count reduction is not an end-to-end speed claim.

Page hydration (including a verified paging directory) now resolves its remaining
receipt IDs from the same current summary projection before reading raw JSON.
It requires a pinned transaction and a complete projection, checks ownership in
each query, and batches at most 4,096 IDs to avoid the predicate reader's full-owner
scan. Only `valid` receipts qualify: that flag checks the entire original typed
payload, including fields omitted from the five-field management summary.
Absent, foreign, missing-projection and unsupported receipts still use the
original raw decoder; no partial attachment occurs on failure or cancellation.
This is not a cross-request cache or a relaxed freshness/permission check.
`hydrate_receipt_signals` measures this extra narrow read, while
`management_page_receipt_signals` records only requested/resolved counts and the
request ID. `page_receipts` then measures any remaining raw fallback, not all
page receipt work. Ordinary account/detail/full-action reads are unchanged.

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

### Complete conditional response delivery

After the unchanged live owner, database and gateway checks have completed,
a matching v3 validator returns an empty 304. Explicitly flush its headers
before synchronously retaining the independent page clone: Go's HTTP server
otherwise may buffer the entire no-body response until the handler returns.
This removes that retention tail from the response's server-side delivery path,
not the preceding current-data reads. There is no background retention, new
cache lifetime, partial response, or change to 200/v1/v2 compression and bodies.
Writers without flushing keep their previous path. Other flush failures propagate
and do not admit the failed response to the computed-page store; the observation
wrapper preserves the underlying transport error. Fixed `response_flush` timing
and `management_response_flush` flags correlate delivery with the same request ID
without logging transport error text. A local real-HTTP retention-barrier test
does not establish production proxy/browser latency or one-second acceptance.

Raw receipt timings distinguish `account_receipts` (before filtering/paging)
from `page_receipts` (page hydration), including catalog-only/fallback account
reads. Each raw fallback keeps its original SQL; projected page IDs no longer
enter that raw read. A traced management
snapshot also emits `management_receipt_raw_scope` after a successful nonempty
raw lookup: fixed phase, requested-ID count, prior account-ID count, overlap
count, and request ID only. The request-local ID set is bound to the same
transaction/owner and discarded with the existing receipt holder. It contains
no receipts and never resolves or suppresses a lookup, including absent IDs.
Failed, canceled and empty reads do not report successful scope. Overlap counts
identify a candidate for further measurement, not proof of skipped I/O, lower
latency, or permission to cache missing rows across reads.

The opt-in passive observer adds count-only `concurrent_activity` from the same
materialized `pg_stat_activity` snapshot: active control backends, known free
usage batches, those batches currently in `DataFileRead`, and their maximum
query age. Management candidates are filtered before the 16-backend limit, so
unrelated older backends cannot crowd them out. No background SQL, IDs, rows or
parameters leave the observer, and no business query is replayed. The fixed
`database_concurrent_not_causal` scope explicitly does not attribute the
management delay to this simultaneous work. Legacy/malformed context remains
`null`, not zero. Counts do not establish contention, E2E latency, or authority
to change worker concurrency/database settings. No recurring sampler is added.
The fixed `query_shape_not_request_type` kind scope is equally important:
ordinary ledger/details reads can share a SQL shape such as rates. An unmatched
sample cannot be attributed to channel management from its shape or timing.

### Source-only model directory reuse

Full management recomputation can reuse only the five-column source directory
(`account_id`, `group_id`, `model`, `state`, `message`). The existing statement
revision covers changes to exactly those model columns; the account view epoch
covers ownership changes. Both are read in the current pinned repeatable-read
transaction. Admission requires prepared, valid compact counters and the exact
database/owner/account-epoch/model-revision key. Missing preparation, a pool or
different transaction/owner, fallback and complete action selection retain the
original reads. No new query or database setting is introduced.

The private store retains at most two directories within a shared 16 MiB
admission budget. A string dictionary preserves every source byte and row order;
expansion creates independent mutable request envelopes. Eviction clears hidden
slice references. Result JSON, predicates, usage, receipts, prices, page data,
deadlines and gateway facts are not cached here: their original reads and fences
remain in place. A changed result or bill can therefore update the page while
the source-only directory remains reusable. An interleaved older transaction can
evict the newer key, causing a miss, never a hit on the wrong snapshot.

Fixed `management_model_directory_cache` logs contain hit, row count, retained
bytes (hit) or admission result (miss), and request ID only. A hit eliminates one
source-directory SQL/scan but still requires O(N) local expansion and indexing;
a miss adds O(N) dictionary packing. The opt-in real-PostgreSQL source-scale
test (`RUN_MANAGEMENT_DIRECTORY_SCALE=1`) compares complete values/order with
direct SQL; it is not browser E2E acceptance or proof of a production hit rate.

The same directory can retain an optional immutable `uint32` order index for
the existing account/group/model C-byte ordering. It is charged to the same
16 MiB budget; exact-budget directories can still be admitted without it. The
first request prepares this index instead of its old pointer sort, and later
requests validate every original-position identity before building pointers to
their own current envelopes in O(N). No mutable result pointers are retained.
Different versions/scopes, changed identities/positions, duplicate source keys
or insufficient index budget use the original sorter. Already ordered sources
need no index. `management_model_order reuse=true` logs fixed count/request-ID
fields only. Warm local ordering savings and one-time preparation cost must be
reported separately; neither establishes browser E2E latency.

### Distinguish independently triggered management reads

The server-derived `read_kind=management_first_page` describes a request shape,
not an action: mounting, changing any filter and refreshing after a write can
all read that first page. Route-kind timing alone cannot verify each operation.
Current clients therefore send a fixed `X-Console-Read-Trigger` diagnostic on
page reads. A hook-local tracker compares inputs of successive started reads:
new owner/mount, changed serialized filters, changed cursor, a preceding accepted
control-write event, otherwise refetch. Structural changes take precedence over
the write marker; the marker is consumed by the next started read. The early
navigation bootstrap uses `mount`. Direct/older reads can remain `unspecified`.

Only `mount`, `filter_change`, `page_change`, `accepted_write`, and `refetch`
are admitted into start/completion logs; missing, invalid or repeated headers
become `unspecified`. Selection, other routes and writes use `none`. This is
separate from the unchanged server-derived `read_kind` and always marked
`read_trigger_scope=client_declared_not_authoritative`. Neither the label nor
its marker authorizes anything or changes queries, requests, URLs, cache keys,
freshness, cancellation, retry behavior, response JSON, ETags or validation.

`management_page_current` also reports the fixed trigger beside its exact
request ID and request-to-paint measurement. Logs contain no owner, cursor,
filter values, credentials or raw header text, and there is no persistence or
new telemetry request. Concurrent hooks/owners do not share tracker state.
These labels describe read triggers, not an audited physical click, a count of
clicks, or a proof that a particular add completed. Multiple events can coalesce
into one read. `accepted_write` includes all accepted control writes, not just
additions. Existing import-bindings route timing still identifies the dialog's
bindings read, not its complete browser rendering. An end-to-end acceptance
still requires the actual operation and its browser paint to be correlated;
fast failure, a server-only duration or a label alone is not acceptance.

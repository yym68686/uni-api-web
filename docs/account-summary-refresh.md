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

Successful add/replace writes close the editor immediately. Query invalidation
runs in the background; its completion is not part of the save acknowledgement.
Failed writes keep the draft and release the close button.

The complete list was around 60 MB, but the old IndexedDB cache discarded
snapshots over 8 MB before compression. Large snapshots now use gzip with an
8 MB stored limit and 128 MB decompressed limit. Small existing records remain
compatible. Read-only inventory, binding, and configured-check snapshots restore
alongside account results under the authenticated origin/user. Cached results
are explicitly labeled with the account snapshot time and remain stale until
live validation; they do not grant write authorization. Expiration, corrupt
storage, cancellation, and logout retain the existing fallback to a live read.

A local in-app Chromium fixture with 123,648 models / 61,077,128 JSON bytes
restored every model in 196 ms and rendered the actual channel management table
(first 25 rows of 768 groups) in 676 ms with all live reads still pending. These
are authenticated cache-restoration measurements, not a claim about cold loads,
network/login latency, or production latency. Production reload timing remains
part of deployment acceptance.

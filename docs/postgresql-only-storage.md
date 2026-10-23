# PostgreSQL-only storage and release protocol

This document describes the implemented storage and preparation contracts. Actual
production activation, validation results and commit identities are recorded in
.codex/postgresql-migration-20261021/report.md; implementation alone is not
evidence that a new read projection has been activated.

The console API and static frontend deploy independently. Compatible additive APIs
and versioned database migrations allow old/new clients to overlap. Database state
has a lifecycle independent of application images. There is no coordinated image
startup gate, S3 history scan, embedded DuckDB catalog, or historical restore at
normal API startup. PostgreSQL contains the existing encrypted control/configuration
state plus prices, request facts, import deduplication and transactional aggregates.
No backup is configured as requested. Losing PostgreSQL loses its saved state;
uni-api's request serving remains independent of analytical telemetry availability.

## Capacity contract

The managed data volume must provide at least 10 GB. Application database budget:
10,000,000,000 bytes, measured using pg_database_size, including indexes and free
pages already allocated to this database. Begin eviction at 7.5 GB, reclaim toward
6.5 GB to reserve headroom for PostgreSQL WAL, temporary queries and control writes.
These watermarks are not a PostgreSQL filesystem quota. The existing managed-volume
quota remains the physical ceiling. A hard guarantee also requires operator limits
on WAL and query temp files and monitoring actual filesystem available space.
Do not claim pg_database_size includes WAL or other databases.

Oldest hourly request-fact partitions and expired aggregate partitions are dropped
transactionally. The remaining daily aggregate is rebuilt from committed hourly
aggregates, preserving sums, histograms and latest-value clocks without scanning
an entire day of minute rows while holding the ingestion admission lock.
No fixed number of days or requests determines retention. The
oldest-hour eviction boundary, loss counters and actual event coverage are surfaced.
Receipt caches follow the same floor through bounded idle-account transactions;
DELETE frees reusable heap/index pages but does not immediately shrink that table.
Receipt cleanup requires the account/time index prepared concurrently by
`--prepare-indexes`; normal startup never builds it over history. Until valid,
receipt cleanup defers without blocking fact admission. Each pass seeks at most
512 oldest rows per selected account, merges the oldest 512 overall and deletes
only that batch. This avoids traversing unrelated accounts through a global time
index while preserving the expiration floor and FIFO order within each pass.
Finalized per-request receipt tasks follow that floor too; pending tasks and
active account leases are preserved. Both receipt retention indexes are prepared
concurrently before releasing against a populated database.
A compact, capped retired receipt-claim index preserves ambiguity across eviction.
When complete ownership evidence is no longer retained, attribution is explicitly
history_limited. Automatic rules require full window coverage after collection,
exporter loss and eviction boundaries. Config/accounts/credentials/prices/pending
operations are never evicted to admit telemetry.

Admission is serialized with ingestion and retention. Batches have body/row limits;
no allocation or unbounded queue is allowed on a paid-request serving path. When
capacity cannot be safely recovered, telemetry receives a retryable error, eventually
bounded exporter spool loss is counted, and serving/configuration recovery continues.

## One-time cutover

1. Inspect existing control state, current saved prices, volume space and request
   history. Preserve configuration and keys. Do not print secrets in commands/logs.
2. Verify legacy object hashes and import retained history into an additive analytics
   schema while the old API serves. Import a validated price baseline without
   overwriting an existing baseline. Prepare receipt retention index CONCURRENTLY.
3. Validate dedup/counts/aggregates/coverage, disk reserve, schema checksum and price
   values. Record a prepared marker only after validation. An unprepared production
   environment carrying legacy bucket settings cannot activate the new API.
4. Release console via GitHub tracking; verify actual new image/readiness and
   existing configuration revisions. Then release source gateways with authenticated
   HTTP fact export. An old backend cannot acknowledge this protocol; the bounded
   spool retains unacknowledged batches. No manual gateway restart or route edit.
5. Read any final legacy batches produced by old gateway replicas; namespace IDs
   exactly as the new receiver does to preserve cross-transport deduplication.
   Verify producer heartbeat/sequence/pending/loss and natural request events.
6. After all old API writers drain, perform three-way final price merge. A concurrent
   divergent edit becomes a persisted conflict; no last-writer clock guess. Mark
   complete only after source continuity, values and prices are validated.
7. Verify real business read endpoints, not just health probes. Observe actual new
   console and gateway replicas for at least two hours. No paid probes, triggering
   detection/synchronization, replaying writes or changing real channels as tests.

Legacy objects are not deleted by migration. Keeping them unchanged is migration
evidence, not a newly configured backup. Once runtime has switched it does not read,
write or depend on them. Encrypted legacy storage metadata is retained inertly for
rollback; a broken legacy storage credential cannot block PostgreSQL reads.

## Transactional read projections and migration checks

Pricing reads compact usage rows committed with facts and rollups. Minute/hour/day
rollups use separate physical children so a current-hour query need not load minute
history. No browser snapshot substitutes for current committed data. Prepare the
existing data with `--prepare-analytics-indexes`, `--prepare-usage-projection`, and
`--prepare-rollup-layout`; then run `--analyze` once and `--verify-queries`. The
initial layout migration is rejected after source activation and compares every
column in both directions before committing each hour. Normal application startup
only checks the completed schema/layout and never rebuilds historical data.

Retired receipt ownership is preserved separately from disposable raw facts. Late
expired billing records retain idempotent ownership digests. Incomplete ownership
history omits a cost lower bound on the wire, including for already-open old clients,
so a partial dataset cannot silently become a financial claim.

Legacy ownership coverage must be recorded before setting a migration phase.
When its exact claim set exceeds the bounded index, use the explicit
`--mark-legacy-claims-incomplete` migration step; this preserves uncertainty
instead of falsely promoting retained history into complete financial evidence.
This limit affects exact historical receipt attribution, not saved prices,
account credentials or current transactional request counters.

Query acceptance includes real node measurements. Passing local fixtures or
health probes does not establish the earlier one-second UI target. Record
remaining latency and ingestion freshness in the release report.

Channel account reads publish receipt enrichment only after the full authorized
query succeeds. SQL cancellation, row-stream failure or an unusable typed receipt
fails the read instead of returning embedded older usage with HTTP 200 or 304.
Failure logs contain the error class and request ID, not receipt or credential
contents. A receipt that is genuinely absent remains absent; it is not fabricated
or confused with a failed lookup. This protects read correctness and does not by
itself meet the interactive latency target.

The unpublished management branch adds control migration `0006_management_signals`.
It installs empty typed model/receipt projections and transaction triggers, with
no historical scan or readiness shortcut at startup. Separate projections let
model and receipt writers commit independently; reads join the current probe ID
to an authorized receipt under one snapshot. Source deletion cascades to its
projection, and older writers also update it after deployment or code rollback.

After deploying that writer schema, the explicit
`--prepare-management-projection` command copies at most 512 source rows per
transaction, records a durable cursor, and then performs a second bounded pass
comparing every projected column. It locks source rows before computing their
derived values, so preparation cannot overwrite a newer concurrent writer.
Updates or key changes behind a cursor remain covered by the triggers. Each
copy batch yields to ingestion admission, has short lock/statement deadlines,
and checks the exact batch's projected size plus index overhead and a 32 MiB
reserve against the existing ingestion watermark. Insufficient space or a failed
comparison leaves the completion marker false and does not advance that batch.
Preparation does not alter the budget or manually evict data.

The fast reader requires this completion marker in the same transaction. Unsupported
typed evidence requests the original full typed reader rather than treating a
partial projection as valid. The conservative support check covers receipt fields
omitted from predicates too; unfamiliar casing, future fields or numeric forms
also request the legacy decoder, which decides whether the data is valid. This
management HTTP path now uses that fallback when necessary. With a complete
projection it filters compact current values, then hydrates the page's groups
and every bound alias member inside the same repeatable-read transaction. Full
action selection still reads all matching evidence. Neither path has been
deployed or prepared in production, and this does not establish the one-second
page target.

Model projection digests include the complete compact summary, while receipt
digests include current display/pricing values. A changed off-page model latency
or quality receipt invalidates the same signed revision used by page and full
selection. Routine receipt poll timestamps do not invalidate unchanged summary
values. Queue identities and states remain part of the revision. Gzip transport
is negotiated for complete JSON responses; it does not omit models or replace
current results with cached data.

Analytical snapshots lease a database connection for their full transaction and
retire that session after rollback, including cancellation and failed reads.
They do not return a backend with retained query allocations to the shared
idle pool. Ingestion and control transactions keep their existing pooling.
The analytical gate, repeatable-read isolation, statement deadlines and memory
settings are unchanged. Session acquisition/retirement logs include only backend
PID and the safe request ID so database memory incidents can be correlated.
Connection loss is classified separately from disk or retention capacity errors.

Narrow receipt attribution scopes use a partial GIN index on request identifiers
to check competing claims across all retained sources and keys. Prepare populated
databases explicitly with `--prepare-receipt-indexes`; each hourly child is built
concurrently and attached with a short metadata-lock timeout. No history scan
runs at application startup. The query selects this path only after the entire
parent index is valid, and broad scopes retain the bulk aggregation path.

Billing reads can use a compact transactional `billing_facts` projection containing
only completion identities and receipt correlation fields. Deploy dual-writing
code first, let every older API writer drain, then run
`--prepare-billing-projection`. Preparation copies at most 512 rows per transaction,
yields to live ingestion, checks capacity headroom, and verifies every column in
both directions for each retained hour. Verification walks the union of source
and projection identities in batches of 512, comparing both complete row sets in
one statement snapshot per batch. This bounds comparison sorts without raising
the 64 MiB temporary-file limit, and detects extra projection rows as well as
missing or altered ones. Reads continue using raw facts until the
explicit completion marker is written. FIFO removes matching projection partitions.

Provider-filtered billing reads use an additional partitioned lookup index on
`(source_id,key_id,model,provider,at_ms)`. It is not created at API startup.
Prepare it explicitly after billing projection activation; the command uses
`CREATE INDEX CONCURRENTLY` on hourly children and attaches a parent index, with
a conservative storage-headroom gate and bounded DDL timeouts. If FIFO has not
reclaimed enough space, preparation defers without changing any serving data.
New hourly projection partitions inherit the prepared index automatically.

The channel table passes its displayed search/status/balance scope to billing.
When those filters leave a single provider, the existing provider filter limits
the billing facts being reconciled. Receipt ownership checks still include
competing claims from every source, caller and provider; narrowing the table
cannot turn an ambiguous receipt into a confirmed charge. Clearing the filter
restores the broad query, and an empty result makes no billing request.

Billing alignment and missing-correlation accounting share one transaction-local
full join of the retained billing and completion identity scope. Completions are
counted across the retained history before applying the UI time window, while
missing-correlation counts include only completions inside that window. This
avoids a second historical anti-join and preserves late headers, reused IDs and
SQL NULL identity semantics. The result is recomputed from the current snapshot;
it is not a persisted or stale browser cache. Global competing receipt claims
and history-limited financial status still use their existing checks.

`billing_claims` expands each billing fact's receipt identifiers through a
statement trigger in the same ingestion transaction, including writes from an
older API after code rollback. It is indexed by a digest of the normalized site, upstream
credential hash and receipt ID. The digest only accelerates lookup: reads also
compare the entire tuple and count distinct event IDs across all sources and
hours. Duplicate headers and digest collisions cannot create false ownership.
The projection follows the same hourly FIFO boundaries as its source facts;
retired ownership evidence and incomplete-history handling remain separate.
Reads apply the committed FIFO floor even when an older writer leaves a derived
claim partition behind; capacity reclamation removes those orphan files before
evicting additional retained facts.

After all older API writers drain, explicitly run `--prepare-claim-projection`
once to populate an existing database. Each batch copies or verifies at most 512
fact identities (at most 1,536 claims), yields admission to live writers and
preserves a 32 MiB storage reserve. Verification walks the union of both identity
sets and compares every projected column in both directions. It also removes
only derived claim partitions orphaned by an older FIFO writer during rollout.
Until every retained hour passes, reads keep the prior global ownership query.
API startup creates only empty schema metadata and never backfills history.

Latest verified model multipliers are durable channel metadata. A control-table
trigger retains one compact receipt per account/group/model for the current
dedicated probe key when a receipt is matched, and atomically before request
FIFO removes it. Older writers and retention workers are covered too. Reads
still choose the latest evidence and require the current key; incomplete or
invalid newer evidence cannot reveal an older valid rate. Deleting an account
or group removes its metadata. This does not retain the raw request history.

If historical cleanup already removed that evidence, `recover-observed-rates`
reads a bounded first page of existing usage receipts for each explicitly
selected current probe key. It never invokes a model, changes channels, or
creates keys. Preview is the default; `--apply` records validated receipt
evidence, with account/group/key checks and a transaction-time key recheck.
Each invocation accepts at most twelve groups and supports an account/group
resume cursor. Unavailable receipts remain unknown; published group rates are
not substituted for verified model multipliers.

# PostgreSQL-only storage and release protocol

Status: migration implementation and isolated validation in progress. This document
is not evidence that production has switched. Actual cutover results and commit
identities are recorded in .codex/postgresql-migration-20261021/report.md.

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

# Analytics startup: verified replay and warm cache

Normal deployment previously downloaded a full checkpoint and waited for a new full S3 listing before opening its socket. Production evidence on 2026-10-16 showed a 6.4 GiB checkpoint taking 162–211 seconds, plus an approximately 1.06 million object scan taking 20–26 minutes. These times include validation and ingestion, not just network transfer.

## Implemented paths

1. **Bounded discovery/ingestion pipeline.** S3 listing and database import overlap. Metadata buffering is capped at 128 objects; imports retain the 64 object/4 MiB budget and global four-download limit. A 250 ms flush deadline keeps discoveries visible even if the next listing page stalls. Every object read uses its listed ETag as `If-Match`.
2. **Durable discovery journal in the existing control PostgreSQL.** Source identity is a hash of source ID, endpoint, bucket and prefix. The per-source row lock serializes both sequence allocation and commit, so a later commit cannot hide an earlier transaction. Entries are deduplicated by object key and reject ETag changes. The journal contains metadata, never facts/prompts/keys. It is not an uploader outbox: data not discovered by a scan may not yet be in it.
3. **Checkpoint-bound replay cursors.** Physical checkpoint metadata carries source journal generation, applied sequence and the last successful full audit time. A replica opens verified history only when all configured source identities match, audits are at most an hour old and replay is contiguous through the captured journal head. Replay has an eight-second bound; incomplete replay falls back to the existing full audit. Missing, legacy or incompatible metadata also falls back. No successful replay claims that previously undiscovered uploads are caught up.
4. **Background full audit.** On fast startup, `audit_pending=true` and `audit_complete=false` are visible in import metadata. The UI explicitly shows incomplete history while that audit runs. Late uploads remain discoverable by the full scan; automation's existing caught-up guard stays in effect. This avoids promising event-log completeness from a discovery index.
5. **Optional verified warm cache.** `ANALYTICS_WARM_CACHE_DIR` names a local or mounted cache directory, separate from `DATA_DIR`. It contains immutable digest-named DuckDB snapshots and an atomically replaced pointer. Readers copy or reflink to their private database and verify all bytes, version, schema and row counts. No process attaches the shared file for writing. A corrupt, incompatible, missing or contended warm cache falls back to S3. Publishing rejects older object/cursor watermarks and retains the previous generation.
6. **Timings.** `checkpoint_timing` reports successful restore stages as well as failures. Source scan logs distinguish list, lookup, journal and ingestion time; batch logs distinguish download/decode from the whole database transaction. Existing `db_operation stage=commit duration_ms` is total transaction duration, not isolated fsync time.

## Rollout and compatibility

If `/warm-cache` exists as a platform-provided directory, it is detected automatically. `ANALYTICS_WARM_CACHE_DIR` can select a different directory. Deployments without either continue using S3. The directory must be trusted and writable by the analytics service user.

The first rollout still uses the existing full recovery because old checkpoints do not contain journal audit evidence. A complete audited scan establishes the journal baseline; a subsequent checkpoint includes it. Later compatible replicas can replay after restore without blocking on another full archive scan. PostgreSQL changes are additive and old binaries ignore the tables/metadata; failed code rollout does not modify user configuration.

Warm caching is opt-in. Use a storage class whose platform supports same-node dual mounting during zero-downtime rollout; mount **only the immutable cache**, e.g. `/warm-cache`, never the active `/data` database. Old and new replicas retain their own writable databases. An empty persistent volume is not a prewarmed cache; the first population must finish before measuring warm startup. Node loss/cache eviction still uses the existing S3 recovery path. Do not disable `REQUIRE_INITIAL_IMPORT` to fake readiness.

The journal currently accelerates startup/replay of discovered objects. Replacing regular full scans with true upload-driven deltas requires a producer outbox/index protocol that covers every successful upload, spool retry and failure gap. It is intentionally not simulated by a time cursor over hash-named S3 objects. Full audit remains enabled in this release.

## Validation and performance boundaries

Focused tests cover concurrent sequence allocation, duplicate/change rejection, missing journal sequence, checkpoint/cursor recovery, source identity changes, false caught-up prevention, pipeline cancellation/slow-page visibility, independent cache files and corruption fallback. Run with an isolated `TEST_CONTROL_DATABASE_URL`.

`RUN_STARTUP_SCALE=1 go test -run '^TestWarmCheckpointMillionObjectScale$' -count=1 -v` builds 1.2 million synthetic object index rows and performs 20 independent warm restores. Local result: 221,523,968-byte file, p50 1.234 s, p95 1.721 s, maximum 3.379 s. This is not production-sized fact history.

`RUN_STARTUP_COPY_SCALE=1 go test -run '^TestWarmSixGiBVerifiedCopyScale$' -count=1 -v` measures a 6,710,886,400-byte best-case sparse local file, including full verification. Local result: 29.968 s. This proves that the current local copy path does **not** meet a blanket ten-second full restore promise. Linux reflink avoids copying blocks where supported, but verification still reads the entire file.

A ten-second deployment activation target requires prewarming before that interval, bounded journal lag and storage measurements. Cold disaster recovery is measured separately. To make every frontend/control API rollout independent of historical data size, use a separate long-running analytics query service; that topology change is not required or silently enabled by this patch.

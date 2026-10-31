# Read-only management read observation

Use this bounded tool to correlate a natural management request with its actual
PostgreSQL wait samples. It does not create a browser session, replay a request,
run model checks/syncs, add channels, change routes, or modify database settings.
It uses existing Fugue CLI authorization and an explicit SSH/database target.

```sh
node scripts/observe-management-read.mjs \
  --ssh-host CONTROL_PLANE_ALIAS \
  --namespace DATABASE_NAMESPACE \
  --pod DATABASE_POD
```

Resolve and verify those exact targets read-only before running. Defaults are
database `console`, role `postgres`, and Fugue app `uni-api-analytics`; these can
be overridden with `--database`, `--user`, and `--app`. SSH aliases must be simple
identifiers, not connection strings or shell options. Never pass credentials.

The default observation lasts 35 seconds at 250 ms intervals. `--duration` is
bounded to 1–45 seconds and `--interval-ms` to 250–1000 ms. One read-only database
transaction observes at most 16 control backends per tick, then rolls back. Its
timeout is bounded; no business data SQL or execution plan is replayed. Only
session statistics are read. One Fugue log read follows observation.

The backend logs its already-read `transaction_timestamp()` beside request-ID
and PID. The tool joins **PID + transaction-start milliseconds**, not PID alone:
a pooled backend can serve many successive requests. Legacy PID-only logs stay
unmatched. Multiple request IDs for the same key are explicitly ambiguous. When
the log source is unavailable, the tool cannot attest request correlation.

`receipt_read` classifies the shared receipt query, not necessarily page
hydration: only request/transaction and existing stage logs establish its role.
`idle in transaction` samples are retained as that state, not treated as active
database work. `wait` is an instantaneous sample, not measured wait duration.

For those retained backends, `blockers` describes only the direct blocking PIDs
reported by PostgreSQL's `pg_blocking_pids`. The function runs once per retained
backend per tick; the result also supplies `reported_blockers`. Up to eight
blocking sessions are described using fixed application/query classes and
PID + transaction-start correlation where matching management logs exist.
`global_advisory_lock` names a shared lock query, not a particular caller or
operation. A slow concurrent query does not establish a blocking relationship.
This observes heavyweight/advisory lock dependencies, not the cause of an I/O
wait, storage contention, a complete dependency chain or elapsed lock duration.
The activity and lock views can change during sampling: exited sessions,
parallel-query duplicate PIDs or prepared transactions (reported as PID 0) can
make `reported_blockers` differ from the bounded session details. Missing or
invalid details remain `null`; an empty list describes only that observation.

On PostgreSQL 18+ with `pg_stat_get_backend_io`, samples also include cumulative
backend reads, bytes and hits. These counters may lag and include earlier work
on that backend; they are **not per-query I/O counters or I/O timings**. Older
servers report `backend_io: null`. The tool does not enable `track_io_timing`,
force a statistics flush, install extensions, or change execution modes.

The request's `management_rows_read stage=target_rows` separates query-ready,
row-advance and JSON-decoding time for target metadata. Its byte count covers
result, billing, compaction and tool-use JSON from successfully decoded rows;
it is not a wire-byte or disk-read counter. The trace finishes before later
model, receipt and rate readers. Query-ready and advance include database,
driver and network work and must not be described as disk-I/O durations.
The model-directory and model-signal streams now emit the same bounded
`management_rows_read` counters as `model_directory_rows` and
`model_signal_rows`. These are diagnostic-only: they do not change the
selection, projection, decoder, error or cancellation path, and they still
cannot distinguish PostgreSQL, driver or network waiting without a correlated
external wait sample.
For these streams, `decode_ms` is derived once per query as elapsed minus
query-ready and row-advance time; it approximates successful row scanning and
in-memory assembly rather than JSON decoding. This avoids extra clock reads
per row. Directory `bytes` counts the returned account/model/state/message
strings; model-signal `bytes` counts evidence digests. Neither is a complete
wire-byte or disk-read counter.
When the exact model scope has no missing-model fallback groups, its predicate
SQL omits the always-false JSON-recordset branch. Nonempty fallback groups
retain the existing complete branch; owner checks, projection verification,
aliases, compact revision proof and full-directory assembly are unchanged.

`management_signal_read primary_key_seek=true candidates=N` identifies a narrow
built-in model predicate read through the existing `(account_id,group_id,model)`
primary key. It requires the exact current versioned directory index, at most
4096 identities, at most one eighth of the source rows, and a conservative
1 MiB JSON-parameter bound. Ownership is checked once inside a materialized
authorized set before parameterized probes. This changes a full projection
scan into O(k log n) index lookups (plus O(k log k) request-local ordering), not
the complete directory, revision proof, receipt checks, facets or hydration.
Missing/invalid required evidence still falls back; unprepared, broad, legacy
and non-built-in scopes retain the original SQL. No index migration or database
setting change is required. The mode counter and `model_signal_rows` timings
must be correlated with a natural request before claiming production gains.

The opt-in local synthetic test `TestManagementSignalSeekProductionCardinality`
uses 190464 rows, including a foreign owner, to compare full predicate columns
and execution plans. Enable it with `RUN_MANAGEMENT_SIGNAL_SEEK_SCALE=1` and an
isolated local `TEST_CONTROL_DATABASE_URL`. Its results are not HTTP/browser
latency acceptance or a reason to replay production business requests.

`management_predicate_cache` reuses only successful narrow built-in model SQL
projections. The current RR transaction must prove the same database, owner,
account epoch, raw-model revision, model-signal revision and exact source-key
set. The separate signal revision is essential: result-only and direct
projection writes need not change the raw directory revision. Incomplete
proofs, invalid/missing evidence, broad/legacy/non-strict reads and custom-model
fallbacks retain the original SQL. At most two entries share 4 MiB; each hit
decodes independent typed evidence and usage pointers. Receipts, live gateway
authorization, filters, page hydration and current response validation still
run outside this cache. A hit skips `model_signal_rows`, not the whole request.

The read-only browser investigation on 2026-10-30 matched these request IDs:

| Operation | Request ID | Request to table paint | Backend evidence |
| --- | --- | --- | --- |
| Open management | `ee030d88-9b36-4037-b0d8-caf0c85ca32a` | 15179 ms | Total 13399 ms; model predicate stream 2036 ms for 1535 rows |
| Next page | `8cb7d50e-2648-4b59-b29b-6137909fede2` | 7254 ms | Total 6079 ms; selected receipt stream 2904 ms |
| Group-status filter | `75ae7f97-afbe-44c1-81d1-9e049c867cd8` | 10432 ms | Total 8056 ms; unchanged directory reused, model predicate stream repeated for 2811 ms |
| Open add dialog | `cc02e80a-d315-4b30-9b78-94efa32622ac` | Not instrumented | Network response headers 1284 ms; import-bindings handler 846 ms |

The paging request's 17 database wait samples matched its backend PID and
transaction start; 11 were in the selected receipt query, waiting on
`DataFileRead` or `AioIoCompletion`. These samples establish I/O waiting during
that query, not the cause of storage latency or a full duration breakdown.
The receipt table already has its ID primary key; a generic (not actual
executed) plan uses it. Further work must measure the uncached receipt read
and response delivery rather than infer a missing index. These pre-change
samples and cache hits do not establish sub-second end-to-end acceptance.

Management may reuse raw target metadata only when the current repeatable-read
transaction proves the same owner, database, account, target and capability
projection epochs. `management_target_cache hit=true` and
`management_rows_read stage=target_cached_rows` identify this replay; it performs
the original typed JSON decoding but no target SQL query. `target_rows` remains
the database-read stage. Retention is bounded to two entries and 8 MiB, and it
contains no expanded models, receipts or attached quality evidence. Those inputs
and gateway/source fences are still read and checked for every fresh snapshot.
Cache hits alone do not establish end-to-end latency or current page validity.

The separate raw page-model stream still shares its original 128-group/8 MiB
budget and exact database/owner/account/model/summary revision proof. Large
completed groups now retain a lossless gzip-compressed, length-prefixed byte
representation instead of the expanded row graph. All source strings and raw
summary bytes (including nil/empty/invalid JSON) remain intact; no public field,
decoder error, SQL ordering, deadline, selection or authorization rule changes.
Small or incompressible groups keep the prior representation. Admission still
rejects oversized/failed/canceled batches before publication. Every reuse
reconstructs request-local rows; damaged length/count/CRC/group metadata causes
a complete same-transaction SQL fallback, never a partial cached group. The
decompressed byte and row allocations have explicit caps. No compressor pool
or decoded row graph survives alongside compressed storage.
`management_page_model_cache` adds `raw_batch_bytes`, `stored_batch_bytes` and
`packed_groups` for the newly admitted batch, not the whole cache. These scalar
counts measure retention, not elapsed time or first-read latency. A local
25x288-model benchmark is not production sub-second acceptance; first-ever
groups and changed revisions still use the original SQL reader.

`management_rate_cache` identifies reuse of receipt-derived model multipliers.
It is enabled only after current projection completeness and the same RR
transaction's complete `ExportStamp` proof. The proof covers raw receipt rate
inputs, membership, ownership, current keys and retained-rate revisions; it is
not a time-based cache. Unsupported prepared reads keep the original decoder
and are never retained. Two entries share a 4 MiB budget; every hit reconstructs
independent typed maps. `rate_rows` and `rate_raw_rows` split actual prepared and
fallback SQL timing. A cache hit skips only the rate query, not any page,
permission, receipt, gateway or source validation.

`management_receipt_set_cache` may reuse one completed selected-ID receipt
projection only for the exact deduplicated ID set, strictness, owner, database
and same current read-only RR proof. It additionally requires the projection's
`complete` flag in that transaction, then checks cancellation normally. Only
the bounded (up to 4096 IDs) selected-ID query is eligible; larger owner scans,
detail endpoints and unpinned reads keep their original SQL. Missing IDs remain
missing and still flow through the original raw fallback. It uses at most 32
entries and 8 MiB; responses are independently reconstructed typed maps.
Paging may install the same scope only after comparing its fresh proof,
headers and deadline in the hydration transaction; the page reader still
checks that transaction's projection completeness before any reuse.
Pages needing more than 4096 receipt IDs sort the complete requested set
before splitting it into bounded batches. This fixes batch membership across
map iterations; it does not relax exact-set invalidation or cache missing IDs
as resolved receipts. Smaller pages retain their original batching path.

The scalar `management_receipt_cache_lookup` reports the lookup's locked
snapshot: entry/byte totals, entries under the current owner/database/proof,
and exact-ID/strictness/kind sets retained under a different proof. A miss does
not by itself distinguish never-read from previously evicted sets.
`management_receipt_cache_store` reports actual admission totals and disjoint
removal counts: `evicted_version`, `replaced`, `evicted_count`, `evicted_bytes`,
and `evicted_both`. The last three describe the limit(s) exceeded immediately
before each removal, not guessed causes inferred from a later SQL duration.
Projection and raw-absence certificates are marked separately; all still
share the 32-entry/8MiB budget. Logging happens after unlocking,
with no IDs, owners, proof stamps or payloads. These observations do not change
authorization, proof validation, cache lifetime, SQL or business behavior.

The entry ceiling is now 32, with the same 8 MiB accounted data budget.
Production admission observations identified count-only evictions at eight
entries and about 3.6 MiB, while all entries still matched the current receipt
proof. A wide page admits two projection batches and a small raw-absence set;
three pages therefore exhausted the previous entry ceiling and evicted still
current evidence. A later page read repeated 4,062 ms of receipt hydration,
with zero version or byte-limit evictions. The larger ceiling lets these small
certificates coexist without relaxing the byte budget, owner/transaction proof,
strictness, SQL fallback or cancellation checks. It does not accelerate a first
uncached read and is not evidence that end-to-end operations meet one second.

Overlapping selected-ID sets can also reuse valid rows and certified projection
absences under that same exact current receipt/ownership proof. A successful,
uncanceled read records only requested IDs missing from its returned map; the
encoded certificate shares the 32-entry/8 MiB budget. Unknown IDs
and invalid rows still query SQL, including strict fallback. Insert, delete,
reassignment, projection writes and ownership changes invalidate the proof;
incomplete/unprepared projections, other transactions without their own complete
proof, and large owner scans cannot use it. An absent projection is never a
resolved raw receipt: both account and hydrated-page raw decoders remain intact.
`management_receipt_subset_cache absent=N` counts certified projection lookups
skipped alongside the existing present-row `reused` and remaining `raw_ids`
counters. These counts are not saved milliseconds or full-operation timings.

Raw lookup absences have a separate certificate kind. Only a successful original
owner-scoped raw SQL read and typed decoder may create one; a projection absence
cannot seed it. A later bounded management read may skip those missing raw IDs
only after its own RR transaction proves the same receipt revisions and account
ownership, and confirms projection completeness. Unknown/present IDs retain the
raw SQL/decoder, while ordinary, detail, selection, unprepared and over-4096-ID
reads keep their original path. No raw payload is retained, and missing rows keep
their previous embedded usage semantics. The certificates share the existing
32-entry/8MiB receipt budget, including their encoded IDs and version keys.
`management_raw_receipt_absence reused=N raw_ids=M stored=BOOL phase=...` counts
this separate step. A hit skips only a proven empty raw lookup, not page validity,
source fences, current headers, response delivery or rendering.

On image `1445564`, request `192c0a79-de7f-44c8-b5ff-ca20772d2a31` was a single
next-page operation: browser request-to-paint 2717 ms, response headers 2431 ms,
body/JSON 192 ms, backend handler 1704 ms. Its page raw receipt query returned
zero rows for 478 unresolved IDs in 235 ms after valid projected receipts and
model rows had already been reused. This establishes a remaining empty SQL
lookup, not a storage root cause or a sub-second operation. The raw-absence
change must still be verified on its deployed image; first reads and changed
receipt/ownership versions deliberately perform SQL again.

Ordinary management queries can reuse only the configuration-versioned parts
of a previously fully verified atomic gateway snapshot: keys, catalog, routes,
providers and export. Every reuse still reads the complete current controls
document and independently authorizes the administrative credential using
`management-revision`; both replies must agree on revision and config revision.
Controls are never retained: `bootstrap_restore` can change without increasing
the gateway's controls sequence. The versioned fields rely on the gateway
contract that revisions include process instance, controls mutation sequence
and the complete base-configuration digest, including credentials and roles.
The original post-database gateway/source fences remain required.

This private store admits only after all original fences succeed, isolates
owners and complete source/credential identities, and retains at most two
entries under a combined 2 MiB serialized payload/key budget. Every read gets
independent decoded values. Version changes use a fresh full snapshot; failures
never return stale facts. Only a genuine missing revision endpoint with a
successful observation read negotiates the original fresh protocol. Selection,
action, account and unscoped readers keep the original reads. There is no TTL,
background refresh, retry of failed reads or public snapshot serialization.
`management_gateway_parts hit=true bytes=N` and `gateway_parts_read` identify
this step; a hit skips the full snapshot download, not live authorization,
controls, database hydration, source validation or browser rendering. It is
not evidence of sub-second end-to-end acceptance.

The add dialog's full `channel-options` read logs `read_kind=import_options`;
its separate `keys_only=true` directory read logs `read_kind=import_keys`.
Both log fast completions as well as failures, without query parameters.
The full read reports `options_controls`, `options_keys`, `options_catalog`
(only when a valid key was selected), and total `options_gateway` durations.
Controls and key labels are independent GETs and now overlap, with at most two
upstream reads at once. The model catalog still starts after controls and keys
complete: an old position catalog must never be paired with a newer accepted
configuration revision. Error precedence, exclusions, ordering and empty/null
data behavior are preserved; cancellation aborts outstanding upstream reads.
There is no new cache, database setting, mutation or speculative group-access
request. Catalog filtering remains O(n); only an independent network wait was
removed from the serial critical path.

Before this change, a read-only IAB key-selection request
`446c9038-58ca-47a4-b55d-63af739f3969` completed in 1321 ms (response headers
1017 ms). No add was submitted. A local synthetic gateway delaying each GET by
10 ms measured the serial reader at 33.4–34.3 ms and overlapping prerequisites
at 22.3–22.6 ms; those fixture numbers are not production improvement or
click-to-paint acceptance. Compare deployed stages and complete browser timing
before drawing those conclusions. Opening the dialog, selecting a key and
refreshing after an accepted real add remain distinct operations.

### Optional receipt plan-mode observation

An authenticated management query can explicitly add `observe_receipt_plan=1`.
This does not alter filters, cursor/selection identity, evidence, SQL parameters,
cache validity or the response. Invalid or duplicate values are rejected. No
ordinary page request enables this flag. At most one selected-ID receipt read
per request and one per process per minute can observe metadata; cache hits can
therefore produce no observation at all.

Use `observe_receipt_plan=hydrate` to reserve that same single observation for
the first uncached page-summary receipt SELECT. Earlier predicate reads do not
consume the request or process admission. Its logs include `phase=hydrate`.
This does not bypass any cache or retry a query, so a retained page or fully
cached receipt set can still produce no observation. The existing `1` value
continues to observe the first eligible SELECT in any phase. Both modes retain
the same authentication, limits and unchanged public response.

Immediately before and after the original SELECT, the same pinned database
connection reads only the names and generic/custom execution counters from
`pg_prepared_statements` for that exact SQL. An exact one-execution delta logs
`management_receipt_plan cache_mode=generic|custom`; absent/ambiguous counters
remain `unavailable`. Only the fixed mode, numeric deltas, ID/row counts,
diagnostic metadata time and request ID are logged. Statement names, SQL,
parameters, owner and receipt IDs are never logged. Metadata failure is isolated
with a savepoint; the business SELECT is never retried or recovered by this
diagnostic, and cancellation retains the existing rollback path.

This identifies the plan mode actually used by that SELECT. It does not expose
its execution tree, estimate physical reads, establish storage contention, or
prove that a generic plan caused the delay. A separate `EXPLAIN (GENERIC_PLAN)`
on another connection describes a possible generic plan only; it cannot prove
which plan an earlier slow request used. No production `EXPLAIN ANALYZE`,
plan-cache setting, extension, business query replay or persistent database
setting is required. Diagnostic requests perform extra metadata round trips;
their timings must not be presented as ordinary-operation performance acceptance.

After an unambiguous counter delta, the optional observation also runs a plain
`EXPLAIN (FORMAT JSON)` of the same SQL with the original bound parameters on
that connection. It uses an unnamed description, without `ANALYZE` and without
executing the SELECT. Only fixed receipt scan-type counts, account scan count,
estimated result rows and node count are logged. Every condition, index/relation
name and parameter value is discarded. This is explicitly a parameterized
estimate after the read, **not the captured execution tree or actual buffer
counts**. It does not run `EXPLAIN EXECUTE` against the named business statement:
that would advance its plan-use history and could affect automatic plan choice.

The same optional read also brackets the original SELECT with the receipt
relation's `pg_stat_xact_user_tables` counters. These are local to the current
transaction and update within it: deltas record actual sequential scans/rows
and index scans/fetched tuples without replaying the SELECT. Metadata reads
do not touch the receipt relation. Missing or regressing counters are explicitly
unavailable. These are tuple-access counters, **not physical buffer reads,
storage latency or the execution tree**. PostgreSQL versions may count separate
array index searches individually; `index_scans` is not an EXPLAIN node count.
No extension, statistics reset/flush, timing setting or database change is needed.

The same metadata SELECT also reads `pg_stat_get_xact_blocks_fetched/hit` for
the receipt heap and sums these counters across its indexes. Before/after
deltas emit `heap_buffer_hits`, `heap_buffer_reads`, `index_buffer_hits` and
`index_buffer_reads`. A read is a shared-buffer miss (`fetched - hit`), which
may still be satisfied by the operating system's page cache; it does **not**
prove a physical-disk operation or measure I/O duration. These counts cover
the current backend's transaction, not concurrent requests or whole-server
cumulative traffic. Metadata reads do not access the observed heap or indexes.
Disabled `track_counts`, missing data or any regressing access/hit/miss counter
make the optional observation unavailable. No additional SQL is added to
ordinary requests, and no timing option is enabled. Interpret these counts
alongside the matching SQL ready/advance durations and wait samples before
attributing a slow read to storage.

Only whitelisted counters, state/wait labels, fixed query classes and correlated
request IDs are emitted. No raw SQL, query parameters, business identities,
payloads, subprocess errors, connection strings or credentials are printed.
`samples: 0`, unmatched samples, or a healthy deployment do not prove an
operation is below one second. Inspect full HTTP/browser timing separately for
filtering, paging, opening an add dialog and refreshing after a real add. The
tool does not perform those operations.

Management's conditional v3 reads also emit `management_validator_response`
with `source=retained_page` or `source=evicted_page`. A retained-page match skips
encoding the complete unchanged columns; an evicted-page match skips hydration.
Both still require the existing live database/gateway proof, exact owner,
representation/encoding, cursor revision, complete public account header digest
and unexpired deadline. The existing proof still brackets the header epoch;
private login rotation between requests is not a public response change.
Only an exact server-produced ETag from a prior successful complete response can
match. Validator metadata is separately bounded to 32 records / 256KiB; it
contains no response body and is lost at process restart/deployment. Revoked
authorization or failed proof never confirms an old response. First reads and
changed data retain their original paths. A fast validator sample alone is not
acceptance of uncached first-page, filtering or pagination latency.

Tests (no production access):

```sh
node --test scripts/observe-management-read.node-tests.mjs
```

Optional integration tests accept `TEST_MANAGEMENT_OBSERVER_DATABASE_URL`,
but refuse non-local hosts. Use a fresh isolated local database, not production:
the fixtures create temporary rows, run short synthetic receipt-query and advisory
lock waits, and roll back. The lock fixture verifies the actual blocking
transaction; the concurrent-activity fixture verifies that unrelated activity
does not acquire a false blocking relationship.

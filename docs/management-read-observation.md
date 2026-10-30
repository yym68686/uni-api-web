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

Management may reuse raw target metadata only when the current repeatable-read
transaction proves the same owner, database, account, target and capability
projection epochs. `management_target_cache hit=true` and
`management_rows_read stage=target_cached_rows` identify this replay; it performs
the original typed JSON decoding but no target SQL query. `target_rows` remains
the database-read stage. Retention is bounded to two entries and 8 MiB, and it
contains no expanded models, receipts or attached quality evidence. Those inputs
and gateway/source fences are still read and checked for every fresh snapshot.
Cache hits alone do not establish end-to-end latency or current page validity.

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
missing and still flow through the original raw fallback. It uses at most eight
entries and 8 MiB; responses are independently reconstructed typed maps.
Paging may install the same scope only after comparing its fresh proof,
headers and deadline in the hydration transaction; the page reader still
checks that transaction's projection completeness before any reuse.
Pages needing more than 4096 receipt IDs sort the complete requested set
before splitting it into bounded batches. This fixes batch membership across
map iterations; it does not relax exact-set invalidation or cache missing IDs
as resolved receipts. Smaller pages retain their original batching path.

Only whitelisted counters, state/wait labels, fixed query classes and correlated
request IDs are emitted. No raw SQL, query parameters, business identities,
payloads, subprocess errors, connection strings or credentials are printed.
`samples: 0`, unmatched samples, or a healthy deployment do not prove an
operation is below one second. Inspect full HTTP/browser timing separately for
filtering, paging, opening an add dialog and refreshing after a real add. The
tool does not perform those operations.

Tests (no production access):

```sh
node --test scripts/observe-management-read.node-tests.mjs
```

An optional integration test accepts `TEST_MANAGEMENT_OBSERVER_DATABASE_URL`,
but refuses non-local hosts. Use a fresh isolated local database, not production:
the fixture creates temporary rows, runs a short synthetic wait and rolls back.

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

Management may reuse raw target metadata only when the current repeatable-read
transaction proves the same owner, database, account, target and capability
projection epochs. `management_target_cache hit=true` and
`management_rows_read stage=target_cached_rows` identify this replay; it performs
the original typed JSON decoding but no target SQL query. `target_rows` remains
the database-read stage. Retention is bounded to two entries and 8 MiB, and it
contains no expanded models, receipts or attached quality evidence. Those inputs
and gateway/source fences are still read and checked for every fresh snapshot.
Cache hits alone do not establish end-to-end latency or current page validity.

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

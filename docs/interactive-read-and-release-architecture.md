# Interactive reads and coordinated releases

## Confirmed incident, 2026-10-21

At 10:48:26 UTC the console analytics request with trace
`76f1a0f33eea098624b3525b6bfcd227` ended as HTTP 499 after 19,983 ms.
The serving `a73426a` backend reported `analytics_query/start` until 10:48:34,
27,777 ms after starting. Its checkpoint copy completed at the same instant:
51,915 ms, 9,993,990,144 bytes. During the wait the SQL pool had free connections.

The code path was `Query -> Prices -> Engine.mu.Lock`. Physical checkpoint
copying held that same mutex. The mutex wait did not observe request cancellation.
An isolated regression holding that writer lock reproduces the blocked query;
after this change, current statistics, price reads and price writes finish while
the lock remains held. This establishes this failure mechanism, not a claim that
every historical timeout had the same cause.

The fix separates settings transaction serialization and history catalog
replacement from history copying. Queries use MVCC reads, and only load configured
prices plus the static catalog; discovering all historical model names remains a
price-directory operation. The history writer lock still protects checkpoint
consistency. No fact, price, routing intent or checkpoint format is changed.

Timeouts now include a console request ID. Matching server logs record only route
templates, outcome, byte count, queue wait and database stage durations. Query
strings, account IDs, credentials and driver error text are excluded. Navigation
cancellation stays cancellation. Mutations are never automatically retried after
an uncertain response.

## One-second current channel management

The PostgreSQL-only migration described in `postgresql-only-storage.md` is now
active; ordinary API releases no longer restore DuckDB or S3 history. That
storage change does not remove the management page's full-dataset read contract.

The management query foundation includes a browser-verified row/filter model,
exact count/facets, 25-row signed cursors, explicit transaction-aware control
readers and version-fenced gateway reads. The in-progress management branch now
registers `GET /v1/channel-management/query`, plus a separate complete-selection
read fenced by an owner-signed token. Both use a fresh read-only repeatable-read
transaction enclosed by live gateway revision checks. Source membership and
credentials are checked before and after that read. Current provider hashes are
matched against the owner's published PostgreSQL key index; the periodic binding
inventory is not used as proof of current ownership. Temporary imports also
require matching site paths and unchanged effective identity.

`Sub2apiChecks` now uses the page reader on the unpublished management branch.
It invalidates the page after successful writes and reads a separate complete
selection for batch actions and optimization. Engine-origin classification uses
the current export and transaction-local override evidence. These branch changes
have not been published. The database reader still loads all model results and
computes a revision over all matching rows. Before activation, scope reads while
preserving public aliases, complete facets and batch semantics, then measure
database work and browser latency at production cardinality.

A bounded read-only diagnostic on the serving node measured 1.165 seconds for
gateway reads, 10.411 seconds for the database snapshot and 3.2-3.4 seconds for
assembly and serialization. Its 25-row response was 7.8 MB because those rows
still carried all model evidence. These measurements disprove the one-second
target for the current candidate; pagination alone does not satisfy it.

The unpublished uni-api companion branch adds an administrator-only current
management snapshot and a small revision endpoint. One captured configuration
provides controls, key directories, effective provider identities, routes and
exported intent. Directory expansion runs after releasing the serving lock.
Analytics validates the complete response and rechecks its revision after the
database read; when observation and administration use different credentials,
both remain authorized. Only a 404 negotiates the older multi-request protocol.
This reduces remote round trips without using a timed cache, but its production
latency has not yet been verified and it does not remove the database full scan.

The new snapshot read does not create quality-history associations. Previously
linked history keeps its original account/group. An unlinked old channel check
without credential provenance must not be attributed using today's credentials.
The existing writer/legacy read behavior remains unchanged; exact writer-time
attribution must be addressed before replacing all legacy consumers. Optimization
reads now include current route revisions, batch capabilities and verified legacy
engine-origin classification. Missing current definition evidence remains unknown.

Local row assembly at 749 groups / 119,840 model results / 276 configured channels
improved from 133–143 ms to 36–37 ms by indexing model positions within the request.
Allocation rose from 81.2 MB to 84.9 MB. Browser-generated row/filter contracts
remain equal. This isolated measurement excludes SQL, transfer and rendering.

The current read contract cannot provide a one-second complete initial view:
48 account requests assemble about 120,000 model records before publishing any
row. Recent serving-node samples spend 3–5 seconds per account: shared quality
0.8–2.3 seconds, model rows 0.7–2.7 seconds and receipts 0.6–2.9 seconds. Bounded
client concurrency prevents overload but does not change the total work.
Persisted browser snapshots are no longer restored into channel management.

The replacement contract should be a server-filtered, cursor-paginated management
query. The response contains the requested 25 rows, exact filtered count, filter
options, current committed revision and read timestamp. It must preserve the
existing owner, source, account, alias, credential fingerprint, quality, model
match and billing rules. Missing receipt evidence remains unknown. The model
drawer reads only the selected group; exports and batch planning use separate
bounded endpoints and validate the current revision before any write.

Keep a compact PostgreSQL read model updated in the same transaction as the
corresponding model result, queue state, binding or receipt change. This is a
transactional projection of current stored facts, not a periodically refreshed
cache. Backfill it in bounded batches, maintain old and new forms concurrently,
and compare results before selecting the new read path. Cross-system gateway
configuration needs an explicit version fence/acknowledgement; a local projection
cannot silently claim it observed an unacknowledged remote change.

Read all page data under one database snapshot. Pagination cursors include the
filter and revision; a changed revision invalidates the cursor instead of mixing
pages. Benchmark cold and warm reads at production cardinality and concurrent
sync load, then measure authenticated browser refresh through the public route.
The acceptance metric is complete current first-page results in less than one
second, including query, transfer and rendering, not a skeleton or stale rows.
No production measurement has yet established this target.

## Ten-second frontend/backend activation skew

Two independent GitHub trackers and two unrelated deployment operations cannot
guarantee this constraint. Compose dependency ordering alone also cannot provide
atomic activation or a bound on scheduler delay. A frontend startup script waiting
on its backend is unsafe with the current Fugue capacity-domain lock: that lock
is retained throughout candidate readiness, so the frontend can block the very
backend it awaits. Do not add that gate or disable backend readiness.

Use a coordinated immutable release manifest containing both image digests and
their API compatibility contract. The lifecycle is:

1. Build and validate both artifacts; the prior release continues serving.
2. Prepare both candidates with explicit capacity reservations. Backend history
   recovery and replay happen here, before the activation interval. A reservation
   must not hold a controller mutex that prevents its dependency from preparing.
3. Validate the candidate database watermark, configuration revision, representative
   read-only business query and API contract, not just `/healthz`.
4. Publish one release-set activation with an epoch fencing both frontend and API
   origins. Browser assets and API calls remain associated with that release.
5. Require frontend/backend serving acknowledgements with absolute activation skew
   at most 10 seconds. An incomplete activation preserves or restores the prior
   positive LKG as a pair; never disable readiness to meet a clock.

This is a platform release feature, separate from serving configuration. Code
failure must leave the existing traffic artifact and configuration recovery
usable. Existing browser tabs require a compatibility window or explicit release
pinning; switching HTML alone does not upgrade already loaded JavaScript.

For sustained improvement, make the history ingestion/query engine a durable
service with its own release cycle. Stateless console control APIs and frontend
can then release together without restoring DuckDB. Keep ingestion, receipt
reconciliation, checkpoint creation and interactive reads in separate resource
budgets. History upgrades still use validated candidates and LKG rollback; no
two writers open the same DuckDB file. A future storage migration must run shadow
queries and preserve fact totals/cost semantics before activation.

The initial safe migration is additive: deploy observability and a versioned read
contract, shadow the transactional projection, then add release-set coordination,
and finally separate the history service behind that contract. Each phase has an
independent rollback. Production route/DNS/source configuration changes are not
part of this patch. The ten-second activation guarantee is not implemented or
verified by this patch.

# Interactive reads and coordinated releases

## Staged rollout status, 2026-10-23

The user authorized incremental production releases and measurement; a complete
end-to-end one-second result is an acceptance target, not a prerequisite for
publishing a compatible observation candidate. The gateway snapshot contract
and console management page are now deployed. Earlier unpublished descriptions
below describe their development history and do not override this status.

Management projection preparation passed all copy and verification phases on
2026-10-23. It is active; do not repeat preparation. The completed projection
still took roughly 10–11 seconds for a real filtered browser page. Trace stages
identified 5.8 seconds in predicate/revision reads and 1.2 seconds in hydration.
Pagination and projection activation alone did not meet the one-second target.

The next incremental read change separates current model headers, model
predicates and compact receipt predicates inside the same repeatable-read
transaction. A request-local identity index resolves receipt roles and preserves
the previous byte-exact evidence revision, including off-scope/quality evidence.
This avoids per-model index probes, duplicate receipt hash joins and sorting wide
joined rows on disk. Only currently referenced, owner-authorized receipts are
retained; selected-model reads construct predicates only for their exact scope.
All visible rows are still fully hydrated, and action selections stay complete.
No cache, schema, preparation, timeout or production setting changes are needed.

Read-only production plans measured separate model-header and model-predicate
reads at 164 and 149 ms, and one complete owner receipt scan at 362 ms. These are
SQL-stage measurements, not browser acceptance. The prior narrow joined model
query took 3.36 seconds with 123,704 projection index probes. Request timing and
real browser checks determine the deployed effect. Historical unpublished notes
below describe development stages, not the current rollout state.

The streamed evidence change is deployed as `e2d6782`. With the same saved
filter, a browser read improved from 10.523 seconds to 8.907 seconds; the API
predicate/revision stage decreased from 4.465 to 2.095 seconds. Default all-model
reads still took 10.1–12.2 seconds for 25 rows of 721. This is a partial gain,
not one-second acceptance. Target details then took 2.001 seconds; measured
stored tool-use JSON alone is 13,952,815 bytes before summary compaction.

The next additive migration contains pure capability-summary functions only.
Management reads omit attempt bodies which the existing wire summary already
removes. The function validates all typed fields first and returns the original
JSON on unsupported representations, preserving full-decoder failures. Detail
reads remain complete. It does not alter any row, writer, historical projection,
or require preparation. Local 749-group equal-output reads decreased from
13,580,119 to 51,681 bytes (150–174 ms to 41–44 ms). Production effects require
measurement after rollout.

The initial capability compaction is deployed as `4fd317d`. It reduced
normalization CPU but current target queries still measured 2.2–2.6 seconds;
a saved-filter browser response took 8.666 seconds. Computing the summary on
every read remains expensive. A subsequent additive target projection stores
those same capability summaries in the source writer's transaction, including
old writers, updates, rollback and identity changes. Missing rows keep the
current calculation fallback. Its explicit preparation fills at most 32 missing
targets per transaction with source locks, complete value comparison, ingestion
admission and capacity reserve; there is no global readiness flag or startup
backfill. This preparation is distinct from the already completed management
model/receipt projection and must never rerun that older operation.

The next current-read contract uses transactional revision counters rather than
hashing every model and receipt on each page. Four relation counters include
source/projection row counts and unsupported-value counts; an explicit bounded
repeatable-read baseline must complete before use. Concurrent writers force a
serialization failure while establishing that baseline. Afterward insert,
update, delete and truncate change the counters in the source transaction.
Missing or unsupported projections retain the complete-reader fallback.

Model and receipt projections are maintained by statement triggers so a bulk
write advances a counter in batches. Equal semantic projections do not rewrite
rows when only receipt polling clocks change. Default pages without model-result
filters read the complete directory and hydrate visible rows; selected-model
pages read only their current predicates, aliases and required fallback groups.
All other filters and full operation selections retain complete current inputs.
The new revision family invalidates older cursors through the existing conflict
path; page and selection use the same new version and queue fence. This is not
a retained result cache. Gateway administration and observation authorization
reads now overlap, with both revisions still required to agree before and after
the database snapshot.

At 119,840 models the isolated new selected directory/revision path took 150 ms,
and local complete HTTP reads took 600–667 ms excluding gateway/public transport.
Actual production/browser acceptance remains required. Initial row-counter and
filtered-update prototypes were rejected by scale/repair tests and are not the
implemented statement-batch path.

Repeated refreshes can reuse a completed computation only after proving it is
still current. The proposed reader checks live source credentials and gateway
revisions on both sides of a fresh PostgreSQL snapshot. That snapshot checks
semantic model/receipt revisions, all management-input table epochs, current
prices, queued work and time-dependent running-task deadlines. Any changed input,
expired deadline, permission error or unavailable source prevents reuse. It
never publishes a prior result while checking or recomputing in the background.
Each successful response has a newly verified read timestamp. Full operation
selections always perform their independent complete read.

The bounded process-local computation store holds at most four complete pages,
16 MiB of encoded page values and only minimal gateway proof metadata. It retains
no browser snapshot and has no timed-staleness exception. Per-relation epoch
triggers advance at most once per writing transaction, with irrelevant account
lease and receipt polling clocks excluded. Changed values and rollback follow
PostgreSQL commit visibility. This targets repeated refresh cost; first reads,
new filters and changed revisions still take the full path and must be measured
separately rather than described as sub-second by a reuse sample.

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
have not been published. The database reader now uses prepared typed model
predicates when available and hydrates complete evidence only for page groups
and their bound aliases. Unsupported evidence uses the existing full typed
reader. Before activation, further scope the compact read and measure all
database work, transfer and browser latency at production cardinality.

The page path now streams predicate columns and complete evidence digests from
one model/receipt join. Previously the page performed that same authorized join
once for its revision and again for filtering. Full action selections use the
same digest and queue encoding; malformed or unprepared projections still take
the complete typed-reader path. The combined query removes one scan without
weakening freshness or the revision fence. It remains unpublished and does not
by itself meet the end-to-end latency target.

The unpublished revision writer also preserves the previous JSON-line bytes
while reusing its output buffer, quoted identity strings and base64 encoding
space inside a single request. It reads all current evidence as before and
changes neither SQL nor signed cursor hashes. Byte comparisons cover Unicode,
escaping, null versus empty digests, long messages and integer limits; database
cases compare the old and new hashes after model, receipt and queue changes.
At 119,840 synthetic rows, revision encoding alone decreased from about 80 ms
and 30.4 MB allocated to 26 ms and 66 KB. This reduces CPU/allocation during the
existing scan; it is not a transactional aggregate or an end-to-end latency claim.

For a selected model, the unpublished page reader now keeps the full current
model directory and global evidence revision but reads predicates only for that
model, its current upstream aliases, and Astra quality evidence. Site groups
where the selected non-default model is absent keep the original all-check
filter behavior. Unsupported evidence anywhere still requests the complete
typed reader. The private directory entries are never published as test results:
all visible page groups and bound alias members receive full hydration, and
batch/optimization selections remain complete. Exact facet/model choices and
page/selection revisions are compared against the full reader.

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

The unpublished storage implementation separates model predicates from receipt
predicates. Each raw writer updates its own typed projection in the same
transaction; the read joins the current model's probe ID to the authorized
receipt under one repeatable-read snapshot. It does not copy receipt values into
model rows, so independently committing model and receipt writers cannot miss
each other's updates. A receipt deletion removes its projection in the same
transaction and preserves the existing missing-receipt behavior. Migration
`0006_management_signals` creates empty projections and writer triggers;
explicit preparation copies and verifies at most 512 identities per transaction
before setting a completion marker. It has no startup backfill. These changes
are local. The HTTP reader uses the projection only after its explicit verified
completion marker; otherwise it continues reading the original source data.

At 48 synthetic accounts, 749 groups, 119,840 models and 59,920 receipts, a local
comparison found identical predicate values for every model. Reading the
existing model summaries and attaching receipts took 1,471 ms in the latest
storage implementation run; precomputing typed predicates at write time and
joining them took 447 ms. The earlier prototype measured 1,410 ms versus 361 ms,
while extracting JSON predicates during a read still exceeded one second.
The two projection tables and their
indexes occupied 32,210,944 bytes in this fixture. The 260 browser filter cases
retain their row IDs, facets, eligibility and complete model choices. Independent
writer commits, repeatable-read isolation, rollback, owner changes, deletion and
invalid predicate values have isolated regression coverage.

These are local database-stage measurements, not a browser latency result or a
production capacity estimate. In particular, the fixture has fewer receipt rows
than production. Before activation, the projection needs a bounded, concurrency-
safe backfill and completeness/capacity gate in production, and page hydration
under the same snapshot. The implemented conservative type-support check requests
the original decoder for unsupported evidence, including non-predicate receipt
fields; this fallback is now integrated into the HTTP path. Selected-model
aliases, queued jobs, shared quality, full action selections and revision changes
must remain exact. The 7.8 MB wire response and the complete end-to-end read still
need separate measurement and reduction; a sub-second projection scan does not
satisfy the one-second page requirement.

The integrated local HTTP fixture now includes authentication, a current
transaction, exact count/facets, page hydration and response encoding at the
same 119,840-model scale. It took 2,100 ms without a remote gateway, public
network or browser rendering. The complete 7,692,053-byte JSON page compressed
to 244,693 bytes. Model predicates took 583 ms and observed-rate reading another
863 ms in that run. This still fails the one-second requirement; transport
compression and a faster model scan alone are insufficient. Page and full
selection share model/receipt evidence digests, queue identities and current
header/configuration data, so hydrating different payload sizes does not break
their revision fence.

The unpublished management page can negotiate a second transport format. It
stores byte-identical complete test results once per response and uses references
where the row, model list and selected check repeat them. The browser restores
independent complete objects before the existing page validation and rendering;
no evidence, diagnostic, facet or operation selection is removed. Older clients
receive the original format and new clients still accept it, so this change does
not require simultaneous frontend/backend activation. Invalid references fail
the read before publishing, and the existing revision is unchanged.

At the same synthetic cardinality, the repeated 25-row fixture decreased from
7,692,053 JSON bytes / 244,692 gzip bytes to 735,960 / 28,897. A second fixture with
different results per group decreased from 7,734,739 / 245,162 to 4,195,046 /
124,751 bytes, with complete round-trip equality. Local Node parsing and object
restoration took 33–39 ms versus 16–23 ms for the original JSON; fewer transfer
bytes do not imply lower decoding CPU. The latest local HTTP sample still took
1,071 ms, excluding gateway, network and browser rendering. This transport change
has not been deployed and does not establish the one-second target.

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
2. Prepare both candidates with explicit capacity reservations. Database
   connectivity, schema compatibility and business-read validation happen here,
   before the activation interval. A reservation
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

PostgreSQL already owns durable state independently of API releases. Ordinary
backend releases no longer restore DuckDB or S3 history. Keep ingestion, receipt
reconciliation, FIFO maintenance and interactive reads in separate resource
budgets; changing a stateless API image must not rebuild stored history. The
remaining release coordination problem is candidate build, scheduling and
activation, not database restoration. Compatible old and new API versions must
continue to read and write the same durable state during rolling releases.

The remaining rollout is additive: validate the versioned read contract and
transactional projection, then add release-set coordination. Each phase needs an
independent rollback that preserves configuration and stored facts. Production
route/DNS/source configuration changes are not part of this patch. The ten-second
activation guarantee is not implemented or verified by this patch.


Account balance refreshes are independent of channel filtering and routing
eligibility. The next reader fetches fresh account headers in the same validation
transaction and overlays those headers on the verified page, so balance updates
need not rerun the channel query. Balance values remain current. They are removed
from channel cursor/action revisions because they do not affect the operation
selection. A separate account-header epoch continues invalidating older whole-
response readers during rolling deployment. Account identity, state, password
availability, configured keys and all channel inputs still invalidate channel
computations; site login-session rotation is not a channel-result dependency.


The current repeat-read verification uses two fresh PostgreSQL proofs around one
live gateway authorization/revision pair. Both database proofs must have identical
semantic revisions, prices, queue state, deadlines and account-header values, and
the live gateway revision must match the retained computation. This establishes
one common current read point while removing a remote round trip. A concurrent
committed mutation or expired deadline forces the complete read; no old result is
shown during verification. Both administrator and observer credentials are checked.

For repeated browser navigation, a tab-local username hint can select saved filter
preferences while the current session request runs in parallel. The server still
authorizes the page from its cookie and attests the response owner. The UI consumes
that early request only after fresh authentication confirms the same owner and
filters; old backends without owner attestation use a normal authenticated read.
No private response is stored in browser persistence. The current page's included
account balances seed the independent balance queries so mounting 48 accounts
need not immediately repeat 48 already-satisfied balance reads; periodic refresh
and missing-balance behavior remain active.

Current gateway verification negotiates GET /v1/channel-controls/revision for
observation credentials. It returns only the live configuration/runtime versions
under the same catalog authorization; admin verification remains independent.
Only404 uses the old full control document. Permission, format, conflict and
transport failures cannot downgrade or authorize a retained result.

The negotiated v3 page transport uses lossless columns for result metadata,
availability and quality. Missing properties have empty cells; present null,
zero, false and empty values remain present. Browser reconstruction restores
the full v1 object contract and independent result objects before validation.
Older v1/v2 clients and backends remain accepted. The bounded computation store
counts both the complete page and its prepared wire representation against its
16 MiB limit; each read still validates current revisions and overlays live
account headers. Static result columns need not be reassembled on every refresh.

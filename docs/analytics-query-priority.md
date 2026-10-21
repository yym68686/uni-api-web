# Interactive analytics priority

The channel table's historical statistics and optional receipt reconciliation
share one analytical execution slot to bound DuckDB memory. Previously billing
held that slot until all receipt pages completed, including PostgreSQL waits.
Splitting the HTTP requests in the browser did not isolate this server resource.

An observed failure on 2026-10-21 had billing hold the slot for 20,002 ms until
its deadline, while statistics waited 18,016 ms and then ran for 2,337 ms. The
browser's 20-second deadline expired before statistics returned. Health checks
remained successful because they do not acquire the analytical slot.

Interactive statistics now register priority before waiting for the same slot.
They cancel registered billing contexts and prevent newly arriving billing from
entering until all queued/running statistics reads finish. Canceled billing
unwinds its existing transactions before releasing the slot. Billing requests
do not cancel other billing requests. Reader cancellation releases priority, and
an already-canceled reader does not interrupt billing.

Priority deferral returns HTTP 503 with `code=billing_deferred` and `Retry-After`,
separate from an actual receipt-query failure. The existing browser retry path
resumes reconciliation; incomplete amounts stay unknown. Deadlines, the single
analytical slot, fact ingestion, configuration, and gateway routing are unchanged.
Continuous statistics traffic can defer optional billing; this policy does not
promise bounded billing latency or resolve every slow ledger query.

`analytics billing_deferred reason=interactive_analytics` and
`event=channel_spend_deferred` identify deliberate priority deferral. Existing
`analytics db_wait` and `analytics db_operation` events retain queue/execution
timings. These events contain no SQL, credentials, or customer payloads.

Regression coverage includes a real DuckDB/PostgreSQL test that blocks receipt
lookup using a lock on an isolated test ledger, checks that statistics complete
before that lock is released, and verifies exact billing recovery afterward.
The same critical-read assertion fails against the preceding implementation.
Scheduler tests cover multiple billing requests, late arrivals, cancellation,
and subsequent billing admission. Use `TEST_CONTROL_DATABASE_URL` pointing to
an isolated local test database when running the integration test.

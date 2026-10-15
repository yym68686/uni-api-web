# Channel account summary storage

The channel-management list must retain every model used for filtering, model
selection and result counts. Fetching all original probe JSON and deleting large
fields in SQL still reads PostgreSQL TOAST data. In the October 2026 incident,
115,675 models occupied 293 MB of TOAST storage. A bounded production read of
1,024 rows took 3.4 ms for metadata, versus 420 ms with result projection. Real
summary requests were canceled at 30 seconds before model reads completed.

Migration `0003_sub_model_summaries` adds a separate, compact result projection.
An `AFTER INSERT OR UPDATE OF result` trigger maintains it in the same transaction
as the original result, including writes from older application replicas. State
and message still come directly from `console_sub_models`. Foreign keys cascade
model deletions and identity changes. Details always read the original result.
The projection retains the probe ID internally so current billing receipts can
still replace the receipt recorded at detection time; the list response omits
that ID as before. Unknown fields and private payloads are not retained.

The schema migration does not scan or rewrite historical results. The background
worker fills at most 128 missing projections per transaction, waits 100 ms between
batches, and uses a database-wide advisory lock to prevent concurrent backfill
batches across replicas. Each statement has a 2-second limit and a 100-ms lock
limit. A keyset cursor avoids repeatedly scanning the filled prefix. A failed
batch releases all locks before retrying. `ON CONFLICT DO NOTHING` ensures a
concurrent result writer wins over an older backfill snapshot. Newly inserted
models are always covered by the trigger, including models before the cursor.

Until a legacy row has a projection, a summary read uses the same projection
function on its original result. Missing projections therefore never suppress
models or manufacture results. `COALESCE` avoids accessing the original TOAST
value when a projection exists. No application-memory cache, expiration window,
schema backfill lock, or frontend timeout extension is involved.

Deployment acceptance requires:

- The new migration completes and `sub_model_summary_backfill complete=true` is
  observed. A missing projection count can be checked without reading results.
- Actual browser summary GET completes within its existing deadline and the list
  contains the complete account/channel scope.
- `sub_accounts_read` confirms model/serialization timings and `canceled=false`.
- Full detail, owner isolation, current receipt association, rolling writes and
  concurrent backfill tests pass. Synthetic timings are not production timings.

An older binary can run against this additive schema. Keep the projection and
trigger on rollback; do not manually remove production data or bypass migration
checksums. A future change to the summary contract needs a new versioned
projection and migration rather than silently changing an applied function.

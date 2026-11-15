# Channel attempt total time

The channel observation table shows **总时间 p50 / p95** immediately after first
token latency. It uses the gateway's existing `attempt.duration_ms`: the elapsed
time from starting that particular channel attempt until leaving it. Streaming
attempts end when the stream finishes. Successes, failures and cancellations with
valid durations are included; skipped, still-running and missing/invalid samples
are excluded. Earlier retries, pre-attempt queue time, later channels and the
end-to-end request duration are not substituted.

The API adds `stats.duration` with `sample_count`, `mean_ms`, `p50_ms`, `p95_ms`
and `last_ms`. As with existing timing statistics, quantiles merge histogram
populations before taking bucket upper bounds, not averages of quantiles. The
finite buckets end at 1,200,000 ms; a quantile in the overflow bucket is null.
No samples show **未记录**; partial samples remain labelled in the tooltip.
Source, calling key, provider, requested/upstream model, endpoint, stream and
time-range filters retain the existing analytics scope.

## Additive deployment and retained history

Analytics migration `0006` adds five nullable/defaulted rollup columns, including
the histogram. Existing rows are not rewritten or scanned at startup. The
previous server uses explicit insert/select column lists, so it can continue
serving during the rolling release and after code rollback. New fact admission,
deduplication, duration rollups and the shared revision commit together. FIFO
minute/hour/day rebuilding retains the new statistics. Queries read preaggregated
rollups only, never a new raw-history scan on the HTTP path.

New attempts work immediately. Existing rollup rows initially have zero duration
samples, not fabricated measurements. Only after every older analytics writer has
drained, operators can explicitly prepare retained raw history with the new
binary in the existing analytics environment:

```sh
analytics-api migrate-postgresql --prepare-duration-rollups --batch-delay=100ms
```

This command updates only the five duration fields; it does not change raw
facts, existing counts/tokens/costs/latency, configuration, account data or tasks.
It processes newest retained hours first: one minute per transaction, then hour
and day summaries. Each batch takes the ingest/FIFO admission lock only if free,
uses 100 ms lock and 1 s statement deadlines, preserves 32 MiB below the existing
ingestion watermark, and verifies the exact number of target identities before
committing. Busy ingestion is retried at most ten seconds without holding the
lock. Timeouts, insufficient headroom or identity mismatches stop preparation;
do not increase production timeouts, force an eviction or interrupt live work to
make it pass. Previously committed batches are safe and the entire command is
idempotent/restartable. There is no global completeness claim: expired raw facts
and genuinely missing durations cannot be recovered. An interrupted preparation
can leave partial duration coverage, which remains visible through sample counts.

Check real read latency, health and naturally arriving samples after release.
Never create synchronization/detection/model requests merely to verify this
display feature.

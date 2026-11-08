# Compression failure visibility

Gateway attempt facts preserve `compaction_invalid_output`,
`compaction_stream_failed` and `compaction_unsupported` through aggregation.
The success-rate details and request traces show their Chinese labels. Failed
attempts remain failures when another channel completes the same request.

The channel failure-reason filter selects channels with matching failures in the
current source/key/model/time/endpoint/stream scope. It does not change the
success-rate denominator or permanently block channels. Administrators can use
the existing channel controls to exclude compression after reviewing evidence.

Active compression detection now requires exactly one usable compaction done
event and the same encrypted state in the completed output. Historical active
results are retained until the next explicit detection; no automatic paid probe
is added. Opaque state is compared only in memory, never stored in result details.

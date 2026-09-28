# Responses success rate

Channel success rate is successful attempts divided by successful plus failed
attempts. Retried providers count separately; explicitly cancelled and skipped
attempts remain excluded. Request usage is not counted again as an attempt.

Streaming `/v1/responses`, including Codex traffic, requires the upstream
`response.completed` SSE terminal event. HTTP 200 or a text delta alone does not
establish success. `response.incomplete` (including `max_output_tokens`), EOF or
disconnection without completion count as failed, even after partial output.
Valid tool-only completed responses remain successful.

The gateway exports `terminal_kind` and nullable `response_completed` in its
immutable facts, and reports incomplete attempts as failed in its live metrics.
On import the console uses explicit negative completion evidence to demote any
reported success. It never promotes an existing failure. This classification is
stored in the rebuildable query cache; immutable upstream facts remain untouched.
Channel rows, minute/day trends and automation use the same success outcomes:
`success` and `completed`, never `incomplete`.

The change needs both updated gateway exporters and the console. Old
`outcome=success` facts with no terminal evidence retain the legacy recorded
classification: missing historical telemetry does not prove a missing upstream
event. In particular, previously flattened incomplete requests cannot be
recovered from output token counts or null first-output measurements. Explicit
historical `incomplete` outcomes are classified as failures, including cached
rollups, without a data rewrite. Success-rate tooltips show scoped success/failure counts and failure reasons.

No database schema or configuration migration is needed. Settings, retained
routing intent and rollup/checkpoint formats remain independent of this policy.

## Failure counts

The channel drawer and all success-rate tooltips display counts instead of policy prose. Counts use the same filtered attempt rollups as success rate, including day rollups. `failure_reason` is validated against fixed codes and encoded into the derived failed outcome, so cache/checkpoint schemas remain compatible. Older generic failed facts appear as “历史失败／原因未记录”; no causes are inferred from token counts. The summary never double-counts request copies as attempts.

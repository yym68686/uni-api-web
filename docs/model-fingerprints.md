# Model fingerprints

Channel management adds a **模型指纹** column and filter. The live API advertises
supported request models in `GET /v1/model-fingerprints` (`supported_models`).
Click a multi-model cell to show every model. The dialog expands to fit its
content and scrolls only if the list exceeds the available height. Models with
detection records appear first, ordered by their latest update time. Hover or
focus a model row to see the requested-class probability and the recorded
reference-library candidate probabilities. A retained score is labeled as the
previous result; incomplete or missing measurements are never shown as 0%.
The bundled and dynamically verified catalogs include these references:

| Requested model | Upstream reference class |
| --- | --- |
| `gpt-6-astra`, `gpt-6.1-sol` | `gpt-6-astra/6.1-sol` (indistinguishable) |
| `gemini-3.1-pro` | `gemini-3.1-pro-preview` |
| `gemini-3.8-flash` | `gemini-3.8-flash` |
| `gemini-4-argon` | `gemini-4-argon` |
| `gemini-4-carbon` | `gemini-4-carbon` |
| `gemini-4-hydrogen` | `gemini-4-hydrogen` |
| `deepseek-v4.1-flash-1017` | `deepseek-v4.1-flash-1017` |

Scoring uses the full lm-detector bank for the active generation; unsupported
models are not probed. The first signed catalog includes separate `gpt-6.2-sol`
and `gpt-6.2-astra` classes, both using Responses with low effort.
DeepSeek 1017 uses its existing independent low-effort class, not the older
`deepseek-v4.1-flash` class or a mutable `deepseek-flash` alias. Its model/price
catalog entry already exists; enabling fingerprints does not edit prices, model
mappings or gateway configuration.

- **匹配**: the highest-ranked class is the requested model's reference class.
- **不匹配**: another reference class ranks first.
- **样本不足 / 检测失败 / 已中断**: no match judgment or probability is displayed.
- With **1/3 or 2/3 valid samples**, the table and detail tooltip show the
  ordered **本轮候选暂定排名**, including when the requested model provisionally
  mismatches. The top five candidates use the
  upstream `allowPartial` ranker; all probabilities remain `null`. The result has
  `decision: "partial"` and `status: "insufficient"`, so it stays out of the
  match/mismatch filters. A later failed attempt does not hide this ranking;
  request failures remain visible in the sample details.
- The separately aligned probability column is **the requested class's probability**,
  including when a different class ranks first. It uses the same colors as
  quality probability: exactly 100% green, at least 90% amber, otherwise red.
  Unknown probability is `—`. This is a closed-set calibrated relative probability,
  not proof of identity or a measured probability of reasoning degradation.
  A tooltip labels any retained scored verdict as the previous round and
  separates it from the current partial ranking.

One-click synchronization/model checks enqueue fingerprint work after successful
availability checks for the supported models. Standalone legacy quality
checks do not trigger fingerprints. Existing quality prompts, pass/fail rules,
results and historical probabilities remain unchanged. Fingerprints may also be
started/cancelled in channel details, independently of other checks.

Channel detail and management consume the same result feed. Exact, fully matched
single-account/group bindings, including live generated imports, with an unchanged model name share the newest valid
result and pending state in both directions. The original job target is retained
for cancellation; no duplicate database result is created. Changed credentials,
unavailable sources, ambiguous or multi-group providers and public aliases mapped
to another model do not share evidence. The management binding remains scoped to
the authenticated owner. Imported providers are joined by exact source/provider
and verified account/group identity from the imports endpoint; their current probe
fingerprint comes from the effective catalog, including temporary channels. Result timestamps include milliseconds so a newer error
can replace an older success consistently. Explicit target rejections stay with
the channel that received them instead of replacing another alias's result.

During rolling updates, the frontend disables actions the backend does not
advertise and shows “指纹检测服务待更新” in details. Periodic reads and an explicit
recheck pick up newly advertised models without a frontend release. The legacy
GPT fallback remains available when an older response omits `supported_models`.

Each fingerprint round collects three separately generated challenges through the
channel's own credentials, with the reference's reasoning setting: low for
Astra/Sol, Argon, Carbon, Hydrogen and DeepSeek 1017; provider default for Gemini
3.1 Pro and 3.8 Flash. Requests are sequential, not retried on failure, and require complete
output. Group and configured-channel probes select the active catalog's wire
contract, independently of ordinary availability checks:

| Model | Probe endpoint | Sampling field |
| --- | --- | --- |
| Astra / Sol | `/v1/responses` | `reasoning.effort: low` |
| Gemini 3.1 Pro / 3.8 Flash | `/v1beta/models/{model}:streamGenerateContent?alt=sse` | no thinking override |
| Gemini 4 Argon / Carbon | same native Gemini endpoint | `generationConfig.thinkingConfig.thinkingLevel: low` |
| Gemini 4 Hydrogen | `/v1/chat/completions` | `reasoning_effort: low` |
| DeepSeek 1017 | `/v1/chat/completions` | `reasoning_effort: low` |

Configured channels always use the named provider through the gateway. For
Gemini and Chat the gateway must advertise that specific targeted fingerprint
protocol before sampling. Older gateways stop at preflight, without sending
a paid request. A relay's normal Responses serving URL is not changed.
Protocol failure is a failed check, never a mismatched identity.

Older runs could reach a Responses compatibility layer that explicitly rejected
`reasoning` for Gemini even though ordinary requests succeeded. For the observed HTTP 400
`invalid_request_error` / `invalid_request` rejection, low-effort Gemini sampling
stops after that attempt and checkpoints a static `reference_effort_unsupported`
diagnostic. No arbitrary error body is exposed. The UI shows “采样参数不支持”.
Both the direct error and the gateway's single JSON-in-`error.message` envelope
are recognized; substring matching and recursive unwrapping are not used.
The collector never removes the reference effort, switches protocol, or retries
to manufacture a score. A structured 403 for a deleted/disabled group, revoked
group access or insufficient balance, a structured 404 for an unsupported
model, HTTP 410, or the targeted gateway's explicit temporary-disable 503
ends the round after checkpointing the failed sample. Generic 403/404/503,
429, 502 and incomplete streams still allow the next independent challenge.
The table and drawer expose safe reasons and HTTP status, not arbitrary provider
messages. HTTP 410 does not prove whether the endpoint or a group was withdrawn.

Observation shows **分组已停用** for an explicit upstream group deletion or
disable only when the result matches the exact source, provider and current
single upstream credential. It is included in the unavailable filter and
skipped by observation batch fingerprinting, together with gateway-disabled or
keyless rows. Individual rechecks remain available. This is an observation, not
a gateway rule change, and it does not remove the channel. Older configured
results can recover a safe error code from matching stored request facts
without issuing a new request.

Every attempt is checkpointed before the next request. The UI reads the latest
ranking while collection is still running, and retains it when queued, cancelled
or interrupted. An explicit continuation requests only the missing samples;
after three valid samples, the unchanged full scorer supplies the match judgment
and calibrated probabilities (`decision: "scored"`). Starting another completed
round clears its previous result. No additional requests are sent just to display
partial results.

`console_model_fingerprints` persists jobs and latest results separately from
availability/quality. Database leases permit up to **50 sites concurrently across
all replicas**, with one job per upstream site. Channels, accounts and all
supported models on the same host share a lease; paths, HTTP/HTTPS and default
ports do not split a site, while non-default ports remain distinct. Configured
channels use their actual provider's upstream host, not the common gateway host.
Fresh configuration is resolved before paid I/O; if it resolves to a busy site,
the job returns to the queue without sending a probe. Busy sites do not block
later queued jobs for other sites. During rolling releases, a running legacy
singleton finishes before the new scheduler starts; the legacy worker stays
fenced while any new site lease exists.

Each job has a 780-second overall deadline. One shared Node process per backend
collects concurrently for up to 50 sites, using one validated reference bank and
a 192 MiB old-generation heap limit and 4 MiB semi-spaces. Scoring runs sequentially on its event loop without extra
scoring heaps. The local worker only calls Go's authenticated loopback bridges;
Go retains upstream credentials and network I/O. Cancelling one job aborts only
its collector. A worker crash fails in-flight jobs without replaying them; future
jobs can start a fresh worker. Accepted numeric evidence is stored privately in
`sample_state` for continuation; upstream response text is never persisted or
returned. Saved evidence is bound to the channel credentials/configuration,
model, protocol and reference version. Each request is bounded to 250 seconds
and 2 MiB output. Lease expiry and
cancellation do not replay paid requests. Site credential changes invalidate old
results; configured results are fenced by the current probe fingerprint in the
management table. There is no route/configuration mutation.

The native Gemini adapter uses `streamGenerateContent`, `x-goog-api-key`, and
`thinkingConfig.thinkingLevel` for the reference effort. It scores only visible
text from one candidate with a `STOP` finish, rejecting reasoning-only, blocked,
truncated, tool-call and malformed responses without retries. This adapter changes
transport only; challenge generation and ranking remain upstream code. No tokenizer
probes or automatic paid backfill are enabled. Existing stored results keep their
original reference commit until the user runs a new check.

The backend image includes Node 24 and the pinned MIT-licensed scorer and fallback
data under `analytics-api/fingerprint`. Go retains
upstream credentials and the existing network restrictions; an authenticated,
short-lived loopback bridge permits only the missing requests (at most three) for the fixed model and
endpoint. Node receives only the temporary local bridge token. Logs/results never
contain upstream credentials or arbitrary upstream error text. Data SHA-256
integrity and detector/bank compatibility are checked before paid requests.

Validation includes scoring parity with upstream reference samples (the older
Gemini 3.x source samples have unknown completion metadata and are only offline
scoring fixtures, never accepted live completions) (not an independent
accuracy benchmark), merged-class semantics, mismatched-class probability,
partial ranking parity without probabilities, invalid/error output rejection,
progressive API checkpoints, durable continuation, exact targeting, duplicate queue requests,
account isolation, 50-site capacity and concurrent claiming across replicas,
same-site serialization across accounts/models/configured channels, actual HTTP
overlap across different sites, rolling-release fencing, cancellation fencing,
interrupted-job handling, key rotation,
automatic batch integration and legacy quality independence. Browser verification
uses isolated fixtures; no production model probes are necessary for deployment.

## Catalog operations

In **设置 → 模型指纹**, choose the exact model IDs that should automatically
queue a fingerprint after a successful channel availability check or site
synchronization. The selection is saved per console user in PostgreSQL. Before
a selection is saved, the pre-existing models keep their previous automatic
behavior; newly supported models require opt-in. An empty saved list disables
automatic fingerprinting. Saving settings does not start new probes or backfill
old checks. Manual fingerprint checks and jobs already submitted are unaffected.

The analytics service checks lm-detector at startup and every 15 minutes
(`FINGERPRINT_CATALOG_SYNC=1`). It reads data at an immutable upstream commit
and runs the bundled validator against the pinned scorer and request logic.
New exact IDs need consistent metadata for a supported request format and
effort, and three complete samples that pass offline scoring and transport
tests. The upstream `chat_completions` label maps only to the existing Chat
Completions adapter; Gemini's `google/` alias is accepted there only when the
source declares the exact requested ID as a response model. Incompatible new
profiles stay out of the list. Invalid candidates, missing existing profiles,
changes to an existing request contract, and
upstream outages leave the last known good catalog in use. Changing the scorer
or supporting a new request format requires a reviewed code change.

There is no scheduled GitHub Action, private signing key, or GitHub write
access in the normal refresh path. No downloaded code is executed. A catalog
refresh does not enqueue or replay any requests. Current rounds finish with
their original reference; a continuation resets partial samples if the
reference changed. A newer catalog builder revalidates the current upstream
commit even when it has not changed. `/healthz` reports
`fingerprint_reference_commit`, `fingerprint_catalog_generation`,
`fingerprint_catalog_builder_version`, and `fingerprint_model_count`.

Set `FINGERPRINT_CATALOG_SYNC=0` to keep the saved catalog without polling.
For signed-catalog recovery, use `FINGERPRINT_CATALOG_SYNC=legacy` and the
manual **Publish fingerprint catalog** workflow (`restore_revision` selects
an exact previously signed data-branch commit). Legacy mode deliberately uses
the separate signed catalog in `fingerprint-data-v1`, rather than the locally
validated upstream catalog. Set the mode back to `1` to resume direct updates.

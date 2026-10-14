# Model fingerprints

Channel management adds a **模型指纹** column and filter. The supported request
models are listed in the shared `fingerprint/models.json` mapping:

| Requested model | Upstream reference class |
| --- | --- |
| `gpt-6-astra`, `gpt-6.1-sol` | `gpt-6-astra/6.1-sol` (indistinguishable) |
| `gemini-3.1-pro` | `gemini-3.1-pro-preview` |
| `gemini-3.8-flash` | `gemini-3.8-flash` |
| `gemini-4-argon` | `gemini-4-argon` |

These Gemini models are the intersection of the built-in console catalog and
lm-detector's reference classes. Scoring retains all 59 classes from upstream
commit `5fcfb4cd34da10915daa38d3ec04d573d946c97e`; unsupported models are not probed.

- **匹配**: the highest-ranked class is the requested model's reference class.
- **不匹配**: another reference class ranks first.
- **样本不足 / 检测失败 / 已中断**: no match judgment or probability is displayed.
- The probability below the status is **the requested class's probability**,
  including when a different class ranks first. It uses the same colors as
  quality probability: exactly 100% green, at least 90% amber, otherwise red.
  Unknown probability is `—`. This is a closed-set calibrated relative probability,
  not proof of identity or a measured probability of reasoning degradation.

One-click synchronization/model checks enqueue fingerprint work after successful
availability checks for the supported models. Standalone legacy quality
checks do not trigger fingerprints. Existing quality prompts, pass/fail rules,
results and historical probabilities remain unchanged. Fingerprints may also be
started/cancelled in channel details, independently of other checks.

Each fingerprint run sends three separately generated challenges through the
channel's own credentials, with the reference's reasoning setting: low for
Astra/Sol and Argon, provider default for Gemini 3.1 Pro and 3.8 Flash. Requests are sequential,
not retried on failure, and require complete output. Group probes use their
recorded Responses/Messages/Chat/Gemini protocol (Gemini native when no saved
protocol exists for a Gemini model); configured channels go through the gateway's
Responses endpoint and its explicit provider-targeting capability. Protocol
failure is a failed check, never a mismatched identity.

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
jobs can start a fresh worker. Completed samples stay in memory and are never
persisted. Each request is bounded to 250 seconds
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

The backend image includes Node 24 and the pinned MIT-licensed upstream code/data
under `analytics-api/fingerprint`. Runtime downloads are unnecessary. Go retains
upstream credentials and the existing network restrictions; an authenticated,
short-lived loopback bridge permits only three requests for the fixed model and
endpoint. Node receives only the temporary local bridge token. Logs/results never
contain upstream credentials or arbitrary upstream error text. Data SHA-256
integrity and detector/bank compatibility are checked before paid requests.

Validation includes scoring parity with upstream reference samples (the older
Gemini 3.x source samples have unknown completion metadata and are only offline
scoring fixtures, never accepted live completions) (not an independent
accuracy benchmark), merged-class semantics, mismatched-class probability,
partial/error output rejection, exact targeting, duplicate queue requests,
account isolation, 50-site capacity and concurrent claiming across replicas,
same-site serialization across accounts/models/configured channels, actual HTTP
overlap across different sites, rolling-release fencing, cancellation fencing,
interrupted-job handling, key rotation,
automatic batch integration and legacy quality independence. Browser verification
uses isolated fixtures; no production model probes are necessary for deployment.

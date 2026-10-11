# Model fingerprints

Channel management adds a **模型指纹** column and filter. The supported request
models are `gpt-6-astra` and `gpt-6.1-sol`. Both map to the upstream reference
class **gpt-6-astra/6.1-sol**; the detector cannot distinguish these two names.
Other request models are not probed. Scoring retains all 57 reference classes.

- **匹配**: the highest-ranked class is the requested model's reference class.
- **不匹配**: another reference class ranks first.
- **样本不足 / 检测失败 / 已中断**: no match judgment or probability is displayed.
- The probability below the status is **the requested class's probability**,
  including when a different class ranks first. It uses the same colors as
  quality probability: exactly 100% green, at least 90% amber, otherwise red.
  Unknown probability is `—`. This is a closed-set calibrated relative probability,
  not proof of identity or a measured probability of reasoning degradation.

One-click synchronization/model checks enqueue fingerprint work after successful
availability checks for the two supported models. Standalone legacy quality
checks do not trigger fingerprints. Existing quality prompts, pass/fail rules,
results and historical probabilities remain unchanged. Fingerprints may also be
started/cancelled in channel details, independently of other checks.

Each fingerprint run sends three separately generated challenges through the
channel's own credentials, with low reasoning effort. Requests are sequential,
not retried on failure, and require complete output. Group probes use their
recorded Responses/Messages protocol; configured channels go through the gateway's
Responses endpoint and its explicit provider-targeting capability. Protocol
failure is a failed check, never a mismatched identity.

`console_model_fingerprints` persists jobs and latest results separately from
availability/quality. A database lease allows one fingerprint worker across
replicas. The worker has a 780-second overall deadline and a 256 MiB Node heap
limit; each request is bounded to 250 seconds and 2 MiB output. Lease expiry and
cancellation do not replay paid requests. Site credential changes invalidate old
results; configured results are fenced by the current probe fingerprint in the
management table. There is no route/configuration mutation.

The backend image includes Node 24 and the pinned MIT-licensed upstream code/data
under `analytics-api/fingerprint`. Runtime downloads are unnecessary. Go retains
upstream credentials and the existing network restrictions; an authenticated,
short-lived loopback bridge permits only three requests for the fixed model and
endpoint. Node receives only the temporary local bridge token. Logs/results never
contain upstream credentials or arbitrary upstream error text. Data SHA-256
integrity and detector/bank compatibility are checked before paid requests.

Validation includes scoring parity with upstream fixtures (not an independent
accuracy benchmark), merged-class semantics, mismatched-class probability,
partial/error output rejection, exact targeting, duplicate queue requests,
account isolation, cancellation fencing, interrupted-job handling, key rotation,
automatic batch integration and legacy quality independence. Browser verification
uses isolated fixtures; no production model probes are necessary for deployment.

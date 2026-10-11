# Model fingerprints

Vendored from [Ikaleio/lm-detector](https://github.com/Ikaleio/lm-detector),
commit `97eb41f78722d32524265bb259fe6258856ca470`, under the included MIT license.
`manifest.json` records original source/data SHA-256 digests. Upstream relative
TypeScript imports have explicit `.ts` extensions for Node 24; algorithms are
unchanged. Reference JSON is gzip-compressed without changing its contents.

The full reference bank is used, but requests are restricted to `gpt-6-astra`
and `gpt-6.1-sol`. Both names map to the shared `gpt-6-astra/6.1-sol` class.
Three independent challenges, Responses streaming, low reasoning effort,
strict complete-output validation; no partial-score confidence or paid retries.
Probability is the requested class's closed-set calibrated probability, not
proof of model identity, and is independent of legacy reasoning-quality history.

Run with Node 24+. The Go worker supplies credentials through stdin, bounds the
process lifetime/heap, and serializes fingerprint work across replicas. No
runtime dependency download or public proxy is involved.

Offline verification: `node --test fingerprint/runner.test.mjs` (from analytics-api).

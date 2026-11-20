# Model fingerprints

Vendored from [Ikaleio/lm-detector](https://github.com/Ikaleio/lm-detector) under
the included MIT license. `manifest.json` describes the bundled fallback data;
`scorer-sources.json` pins the compatible upstream source digests, including the
scoring correction from `33fb43f26670be0ed7817ab13e5c4c4731ccb76a`. Relative
TypeScript imports have explicit `.ts` extensions for Node 24. The full bank
and binary detector are gzip-compressed without changing their decoded contents.

`models.json` seeds the Go API and Node scorer; the UI reads model support from
the API. The seed includes Astra/Sol
(the merged class), Gemini 3.1 Pro (the 3.1 Pro Preview class), Gemini 3.8 Flash,
Gemini 4 Argon, Gemini 4 Carbon and DeepSeek V4.1 Flash 1017. Three independent
challenges and strict complete-output validation are required. Low effort is used
for Astra/Sol, Argon, Carbon and DeepSeek 1017; Gemini 3.1 Pro and 3.8 Flash use
the provider default, matching their reference collection metadata.
`gemini.mjs` adapts native Gemini streaming only; Responses, Messages and Chat
Completions use the upstream parsers. Go selects the wire contract for each model
from `protocols.json`: Responses for Astra/Sol, native Gemini for all four Gemini
models, Chat Completions for DeepSeek 1017. Site availability results do not
override it. Configured probes stay pinned to one gateway provider; native Gemini
and Chat require the matching targeted fingerprint capability. No tokenizer
probes, automatic retries or route edits.

Each explicit run now fills only the missing slots in a three-valid-sample
round. Every completed attempt is checkpointed before another upstream request;
only accepted numeric runs (not provider text or credentials) enter the private
`console_model_fingerprints.sample_state` column. Partial rounds survive retry,
cancellation and worker replacement. Three completed samples are cleared by the
next explicit enqueue, never by an automatic retry. The minimum-number acceptance
test is unchanged; the scorer follows the pinned upstream version described below.
The resume proof binds the exact
channel configuration, model, protocol, model catalog and reference manifest.
Routine encryption-nonce changes for the same site test key do not reset it;
actual changes start a fresh round rather than combine incompatible evidence. Public
result JSON contains only the existing score/sample diagnostics, not vectors.
Legacy partial records lack the numeric evidence and cannot be reconstructed;
their next explicit detection starts the first durable round.

With one or two valid samples, the runner enables upstream `allowPartial` and
returns the top five candidates with `decision: "partial"`,
`status: "insufficient"` and null calibrated probabilities. Each candidate
and the requested class carry a separate `estimated_probability`, computed
by unit-temperature softmax over the full reference bank before clipping the
list. Failed attempts retain their diagnostics without hiding that ranking.
Each checkpoint exposes the ranking while collection continues.
Three valid samples still use the unchanged full
scorer and calibration, marked `decision: "scored"`; only this complete result
can become match/mismatch. Empty or invalid evidence produces no candidates.

One shared Node 24 runtime with a 192 MiB old-space / 4 MiB semi-space cap serves
up to 50 site collectors. Go keeps all upstream credentials, network restrictions,
provider targeting and database site leases; Node only receives ephemeral loopback
bridge tokens. Same-site channels/models serialize across backend replicas.

`reference-fixture.json` contains three reference texts per tested identity for
scoring parity, not an independent accuracy benchmark. Gemini 3.x fixture texts
have unknown historical completion metadata; live samples must still finish
explicitly. Probabilities describe the closed reference set and are independent
of legacy reasoning-quality history.

Offline verification: `node --test fingerprint/runner.test.mjs` (from analytics-api).

The analytics service checks upstream directly for exact models with a supported
wire contract, then runs the checked-in offline scoring and request-contract
tests before saving the last known good catalog in Postgres. GitHub Actions are
not needed for routine updates. See [catalog operations](../../docs/model-fingerprints.md#catalog-operations).

DeepSeek 1017 uses the existing `deepseek-v4.1-flash-1017` class in this pinned
69-class bank; no scorer, detector, calibration or reference manifest update is
needed. Its 36 accepted reference answers were collected through OpenRouter's
DeepSeek route at low effort. The fixture adds the first three complete answers
from `data/collections/deepseek-v4.1-flash-1017-openrouter-deepseek-low-20261017/result.json`
at the same pinned commit. Older Flash classes and the `deepseek-flash` alias do
not inherit this identity. The exact console model is already in the shared
price/model catalog. Successful model checks enqueue it through the existing
configured-channel and site/group paths; availability-only and legacy quality
checks do not. There is no automatic historical backfill. Verification uses
offline/local fixtures, not production probes.
Updating `models.json` changes the existing resume proof, so earlier partial
rounds restart on their next explicit detection; completed historical results
and serving configuration are unchanged.

Carbon has its own calibrated class, with 36 accepted reference answers from
OpenRouter's pinned Google Vertex US route at low effort. The same upstream
revision refreshes Argon's references to that route; both fixtures use the first
three accepted collection answers. Scores are checked against the original
upstream scorer with the complete matching bank/detector, never a subset.
The updated upstream scorer rejects non-model-like numeric sequences; those
results are inconclusive and carry no identity probability. Changing this pinned
manifest invalidates partial-round resume proofs; old samples are not mixed
with the new reference or automatically replayed.

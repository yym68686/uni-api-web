# Model fingerprints

Vendored from [Ikaleio/lm-detector](https://github.com/Ikaleio/lm-detector),
commit `5fcfb4cd34da10915daa38d3ec04d573d946c97e`, under the included MIT license.
`manifest.json` records original source/data SHA-256 digests. Relative TypeScript
imports have explicit `.ts` extensions for Node 24. Algorithms are unchanged;
trailing source whitespace is normalized. The complete 59-class bank and upstream
binary detector are gzip-compressed without changing their decompressed contents.
The upstream `detector-codec.ts` loads the binary representation.

`models.json` is shared by the UI, Go API and Node scorer. It enables Astra/Sol
(the merged class), Gemini 3.1 Pro (the 3.1 Pro Preview class), Gemini 3.8 Flash and
Gemini 4 Argon. Three independent challenges and strict complete-output validation
are required. Low effort is used for Astra/Sol and Argon; Gemini 3.1 Pro and 3.8
Flash use the provider default, matching their reference collection metadata.
`gemini.mjs` adapts native Gemini streaming only; Responses, Messages and Chat
Completions use the upstream parsers. Configured gateway channels remain explicitly
targeted through Responses. No tokenizer probes, automatic retries or route edits.

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

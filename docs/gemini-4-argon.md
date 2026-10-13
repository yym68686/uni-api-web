# Gemini 4 Argon

Added `gemini-4-argon` to the shared frontend/backend model catalog. Channel
management filters, detection settings, channel model selection and price settings
consume this catalog. Observation and log model lists still describe configured
channels and recorded traffic; a catalog entry does not add a production route.

Availability checks use Gemini's native
`/v1beta/models/gemini-4-argon:streamGenerateContent?alt=sse` endpoint and
`x-goog-api-key`, matching the other Gemini models. Adding the catalog entry does
not itself start detection or change saved model exclusions.

Google's [official pricing](https://ai.google.dev/gemini-api/docs/pricing#gemini-4-argon)
was checked on 2026-10-14. Standard prices per million tokens are input $2, output
(including thinking) $10 and cached input $0.10 through 2026-11-30. The published
rates starting 2026-12-01 are $4, $20 and $0.20. Only implicit caching is supported;
the existing catalog convention prices any reported cache-write tokens as ordinary
input. Search/tool charges are not included.

These are current reference prices, not an automatic date-based billing schedule.
The price-settings note records the expiry so prices can be reviewed at that time.
Saved operator prices remain authoritative; unconfirmed discovery placeholders
receive the reference prices without rewriting saved state.

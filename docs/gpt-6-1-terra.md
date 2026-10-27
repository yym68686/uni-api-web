# GPT-6.1 Terra

Added `gpt-6.1-terra` to the shared model catalog on 2026-10-27. Channel
management filters, built-in detection choices, batch selection, import model
lists and price settings consume this same catalog. Existing operator prices
and saved model exclusions remain authoritative. Adding the model does not
start a sync or a probe.

The official [model page](https://developers.openai.com/api/docs/models/gpt-6.1-terra)
confirms the exact model ID and requires Responses for tool calling. The
existing GPT probe path uses Responses and sends the requested model unchanged.

The official [pricing page](https://developers.openai.com/api/docs/pricing)
lists Standard prices per million tokens: input $1, output $5, cached input
$0.05, 30-minute cache write $1.25, and 12-hour write $2.50. Prompts above
272,000 input tokens use 2x input/cache and 1.5x output rates.

Consistent with existing GPT entries, the catalog stores the Standard short
context rates and the 30-minute write baseline. GPT cache-write billing and
the optional 272k premium remain off by default and can be enabled per model.
The current price schema does not identify OpenAI's 12-hour retention; the
price note states this limitation rather than labeling it as a 1-hour rate.
Batch/Flex, Fast and regional adjustments are not applied automatically.

No model fingerprint reference for Terra is added: the existing fingerprint
supported-model list remains independent from the general model catalog.

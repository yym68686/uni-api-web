# Protocol visibility during channel optimization

A model passing availability detection does not prove it can be added to every
existing binding of that site group. A single group may have separate Responses,
Messages, Gemini or Chat Completions channels. The optimization preview previously
omitted this distinction and offered incompatible additions, which the atomic
batch endpoint rejected with a generic error.

The channel list now shows each actual engine/configuration mode and the observed
probe protocol. Optimization previews show the effective caller-key engine, probe
protocol and provider identifier for each binding. Actual route-catalog metadata
takes precedence over imported-channel summaries; model names and provider-name
suffixes are not protocol evidence.

For site imports, incompatible or unknown-protocol additions remain visible with
an explanation, but cannot be selected. Select-all includes eligible changes
only. Compatible changes remain independently selectable. Existing-model removals
do not require matching protocol evidence. Automatic protocol conversion or
splitting is not performed: it could change how existing models are requested.
Use the channel editor's protocol partitions when such a change is intended.

The backend still checks fresh evidence before a single atomic restore. If it
rejects changed/incompatible protocol evidence, its error identifies all affected
provider identifiers, models, current protocols and detected protocols. Nothing
from that source batch is submitted on validation failure.

Regression coverage includes mixed compatible/incompatible preview selection,
caller-specific engine overrides, unknown protocols, unchanged models/removals,
per-channel diagnostics, route metadata propagation and all-conflict reporting.

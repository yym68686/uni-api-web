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

Explicit engine fields do not prove a user chose them: the old site importer
wrote `engine:gpt` with a `/v1/responses` URL. The server classifies an import as a
legacy default only when its generated root identity, caller-key owner and exact
site URL match that old format. Native/copy/protocol-suffixed channels, unknown
origins and engine/address overrides do not qualify. Settings audit history also
protects overrides later folded into a channel definition. If evidence cannot be
read, the preview does not offer migration. Labels distinguish legacy defaults,
recorded manual overrides and explicit configurations of unconfirmed origin.

Optimization offers a separately selectable "upgrade legacy default protocol"
change. Selecting a dependent Responses addition selects that upgrade as well;
deselecting the upgrade removes dependent selections. Applying the batch rereads
origin evidence under the source lock and deletes only the old fixed `engine`
field, allowing the existing Responses URL to resolve to codex. This shares the
single atomic restore with model changes. Existing model mappings, credentials,
URL, settings, unrelated keys, relative order and disabled flags are preserved.
A migration-only selection is a real change even when memberships are identical.
Unselected migration is never implicit in the backend.

Other incompatible or unknown-protocol additions remain visible with an
explanation, but cannot be selected. Select-all includes eligible changes only.
Compatible changes remain independently selectable. Existing-model removals do
not require matching protocol evidence. Protocol splitting remains an explicit
channel-editor action.

The backend still checks fresh evidence before a single atomic restore. If it
rejects changed/incompatible protocol evidence, its error identifies all affected
provider identifiers, models, current protocols and detected protocols. Nothing
from that source batch is submitted on validation failure.

Regression coverage includes mixed compatible/incompatible preview selection,
caller-specific engine overrides, unknown protocols, unchanged models/removals,
per-channel diagnostics, route metadata propagation and all-conflict reporting.
PostgreSQL-backed gateway tests cover legacy classification, historical overrides,
atomic migration plus model addition, preserved aliases/settings/order, stale
revisions and obsolete migration flags after a completed upgrade.

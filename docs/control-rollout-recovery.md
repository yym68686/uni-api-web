# Retained controls during rolling deployments

The console PostgreSQL snapshot is the saved routing intent. Gateway memory is
an instance-specific application of that intent. A rolling deployment can leave
old keep-alive connections serving control requests after a replacement booted;
the replacement may therefore have restored an earlier saved version.

The authenticated runtime-restore response includes an opaque `snapshot_id`.
The exact issued snapshot remains encrypted in `console_control_bootstraps`,
scoped to the source and its target URL/credential identity. uni-api records the
receipt before listening and exposes `bootstrap_restore` on its administrator
control endpoint. Its `unchanged` flag compares the current full revision,
including base configuration, with the revision applied at startup. Runtime
edits, resets and restores cannot renew this receipt.

When the instance changes, the console can catch up a nonempty earlier bootstrap
only if its receipt is known, its revision is unchanged, and its complete public
controls plus settings/definition digests match the issued snapshot. The write
still requires the gateway's exact revision. Independent changes, unknown
receipts and legacy gateways with conflicting nonempty state remain protected.

Adopting the replacement retires the prior instance in the same PostgreSQL
transaction. Delayed responses from that instance cannot replace saved intent.
UI drafts based on a different revision return a refresh-required conflict
before forwarding a mutation. A brief conflict while traffic still reaches a
retired instance is expected; it no longer becomes a permanent restoration
conflict once traffic reaches the replacement.

Diagnostics: `/v1/sources/{id}/control-persistence` exposes `saved_instance_id`,
`saved_revision` and opaque `snapshot_id`; `/v1/channel-controls` on the gateway
exposes the startup receipt. No routing credentials are included. The boot
event `channel_controls_bootstrapped` records the same receipt and revision.

Deploy the console first, then uni-api. Older gateways ignore the additional
bootstrap field; new gateways remain compatible with older consoles but do not
claim a verified receipt. Recovery never disables retention or clears saved
configuration to bypass a conflict. Read-only model catalogs are key-scoped:
availability for one API key does not imply availability for another key.

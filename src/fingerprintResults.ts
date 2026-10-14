import type { InstalledChannel } from "./sub2apiImports";
import { boundGroups } from "./sub2apiImports";
import { channelMembers } from "./channelManagement";
import type { ManagedChannel } from "./channelManagement";
import type { FingerprintRecord, FingerprintTarget } from "./ModelFingerprints";

export const fingerprintKey = (t: FingerprintTarget) => JSON.stringify([t.source_id || "", t.provider || "", t.account_id || "", t.group_id || 0, t.model]);
const pending = (r: FingerprintRecord) => r.state === "queued" || r.state === "running";
const newer = (a: FingerprintRecord, b: FingerprintRecord) =>
  Number(pending(b)) - Number(pending(a)) ||
  (b.updated_at_ms ?? (b.result?.checked_at ?? 0) * 1000) - (a.updated_at_ms ?? (a.result?.checked_at ?? 0) * 1000);

// Read-only projections: original job identity is retained for cancellation.
// A name/host match alone cannot link accounts, groups, aliases or credentials.
export function sharedFingerprintIndex(records: FingerprintRecord[], channels: ManagedChannel[], unavailableSources: string[] = [], imports: InstalledChannel[] = [], unavailableImports: string[] = []) {
  const index = new Map(records.map(r => [fingerprintKey(r), r]));
  const groups = new Map<string, { keys: Set<string>; records: FingerprintRecord[] }>();
  const unavailable = new Set(unavailableSources);
  const unavailableImport = new Set(unavailableImports);
  const imported = new Map<string, InstalledChannel[]>();
  for (const item of imports) {
    if (item.kind === "configured" || unavailableImport.has(item.source_id) || unavailableImport.has(item.source_name)) continue;
    const key = JSON.stringify([item.source_id,item.provider]);
    imported.set(key,[...(imported.get(key) || []),item]);
  }
  for (const member of channels.flatMap(channelMembers)) {
    if (unavailable.has(member.source_id) || unavailable.has(member.source_name) || !member.probe_fingerprint) continue;
    // The imports endpoint validates generated IDs against the owner's exact
    // account/group and live identity_changed flag. Never infer from a label.
    const copies = imported.get(JSON.stringify([member.source_id,member.provider])) || [];
    const identities = new Set(copies.map(item => JSON.stringify([item.account_id,item.group_id])));
    if (identities.size > 1) continue;
    const importedChannel = copies[0];
    const bindings = boundGroups(importedChannel || member);
    if (bindings.length !== 1) continue;
    const binding = bindings[0];
    for (const model of member.models) {
      if ((member.model_mappings?.[model] || model) !== model) continue;
      if (importedChannel && (!importedChannel.models.includes(model) || (importedChannel.model_mappings?.[model] || model) !== model)) continue;
      const sourceKey = fingerprintKey({source_id:member.source_id,provider:member.provider,model});
      const groupKey = fingerprintKey({account_id:binding.account_id,group_id:binding.group_id,model});
      let group = groups.get(groupKey);
      if (!group) {
        const site = index.get(groupKey);
        group = {keys:new Set([groupKey]),records:site ? [site] : []};
        groups.set(groupKey,group);
      }
      group.keys.add(sourceKey);
      const record = index.get(sourceKey);
      if (record && (record.state !== "done" || record.revision === member.probe_fingerprint)) group.records.push(record);
      else if (record?.state === "done") index.delete(sourceKey);
    }
  }
  for (const group of groups.values()) {
    const latest = group.records.sort(newer)[0];
    if (latest) for (const key of group.keys) index.set(key,latest);
  }
  return index;
}

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import { channelMembers, type ManagementRow } from "./channelManagement";
import { fencePreviousFingerprint, fingerprintKey } from "./fingerprintResults";
import type { ManagementPage } from "./managementQuery";
import type { FingerprintRecord, FingerprintTarget } from "./ModelFingerprints";

type Response = { scoped?: boolean; read_at_ms?: number; data: FingerprintRecord[] };
type FingerprintChannel = Omit<FingerprintTarget, "model">;
const channelKey = ({source_id, provider, account_id, group_id}: FingerprintChannel) =>
  JSON.stringify([source_id || "", provider || "", account_id || "", group_id || 0]);

export function useVisibleFingerprints(owner: string, page?: ManagementPage) {
  const scopes = useMemo(() => {
    const channels = new Map<string, FingerprintChannel>();
    for (const entries of Object.values(page?.context.fingerprints || {})) {
      for (const { target, record } of entries) {
        for (const scope of record ? [target, record] : [target]) {
          // Pick scope fields only; records may carry the full verdict.
          const { source_id, provider, account_id, group_id } = scope;
          channels.set(channelKey(scope), { source_id, provider, account_id, group_id });
        }
      }
    }
    return [...channels.values()];
  }, [page]);
  const query = useQuery({
    queryKey: ["model-fingerprints", "management", owner, scopes],
    enabled: scopes.length > 0,
    queryFn: async ({signal}) => {
      const batches = [];
      let batch: FingerprintChannel[] = [];
      const path = (scopes: FingerprintChannel[]) =>
        "/v1/model-fingerprints?scopes=" + encodeURIComponent(JSON.stringify(scopes));
      for (const scope of scopes) {
        if (batch.length && (batch.length === 16 || path([...batch, scope]).length > 6000)) {
          batches.push(batch);
          batch = [];
        }
        batch.push(scope);
      }
      if (batch.length) batches.push(batch);
      const response = await Promise.all(batches.map(batch => controlRequest<Response>(
        path(batch), {signal, cache:"no-store"},
      )));
      return response.every(part => part.scoped && part.read_at_ms && Array.isArray(part.data))
        ? { supported: true, readAt: Math.min(...response.map(part => part.read_at_ms!)), records: response.flatMap(part => part.data) }
        : { supported: false, readAt: 0, records: [] };
    },
    refetchInterval: query => query.state.data?.supported === false ? false
      : query.state.data?.records.some(r => r.state === "queued" || r.state === "running") ? 3000 : 8000,
    refetchOnWindowFocus: false,
    retry: false,
  });
  const index = useMemo(() => new Map(query.data?.records.map(record => [fingerprintKey(record), record])), [query.data]);
  return (row: ManagementRow, displayed?: ManagementPage) => {
    const entries = displayed?.context.fingerprints[row.id] || [];
    if (!page || displayed !== page || !query.data?.supported || query.data.readAt < page.read_at_ms) return entries;
    if (!entries.length) return entries;
    const members = row.configured ? channelMembers(row.configured) : [];
    return entries.map(entry => {
      const key = fingerprintKey(entry.target);
      const old = entry.record;
      const shared = old && fingerprintKey(old) !== key ? index.get(fingerprintKey(old)) : undefined;
      const direct = index.get(key);
      const member = members.find(m => m.source_id === entry.target.source_id && m.provider === entry.target.provider);
      const candidate = direct && (!direct.source_id || direct.state !== "done" || direct.revision === member?.probe_fingerprint)
        ? (direct.source_id ? fencePreviousFingerprint(direct, member?.probe_fingerprint) : direct)
        : undefined;
      const record = candidate && (!shared || (candidate.updated_at_ms || 0) >= (shared.updated_at_ms || 0))
        ? candidate
        : shared && old
          // Reuse the existing sharing decision; do not infer a new binding.
          ? (shared.source_id ? fencePreviousFingerprint(shared,
              old.revision || old.previous_revision) : shared)
          : undefined;
      return { ...entry, record };
    });
  };
}

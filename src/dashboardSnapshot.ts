import type { QueryClient, QueryKey } from "@tanstack/react-query";

// Only authenticated, read-only dashboard results may be persisted. Never store
// login credentials, revealed keys, editable configuration, or probe requests.
const names = new Set([
  "sources", "keys", "catalog", "metrics", "live-metrics", "key-request-stats",
  "channel-checks", "sub-quality-summary", "sub2api-imports", "channel-sites",
  "site-account-balance", "balance", "channel-spend",
]);
const prefix = "uni-console-dashboard:v1:";
const ttl = 30 * 60_000;
const maxBytes = 3_000_000;
type Entry = { key: QueryKey; data: unknown; at: number };
type Snapshot = { version: 1; user: string; at: number; metricsKey: QueryKey; entries: Entry[] };
const restored = new WeakMap<QueryClient, Map<string, number>>();
const key = (user: string) => prefix + JSON.stringify([location.origin, user]);
const secretFields = new Set(["api_key", "api_keys", "encrypted_key", "password", "key", "token", "curl_token", "authorization", "cookie", "access_token", "refresh_token"]);
function projection(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(projection);
  if (value && typeof value === "object") return Object.fromEntries(
    Object.entries(value).filter(([name]) => !secretFields.has(name.toLowerCase())).map(([name, item]) => [name, projection(item)]),
  );
  return value;
}
function validKey(value: unknown): value is QueryKey {
  return Array.isArray(value) && names.has(value[0]) && value.every(v => v == null || ["string", "number", "boolean"].includes(typeof v));
}
export function restoreDashboardSnapshot(client: QueryClient, user: string) {
  if (!user) return;
  try {
    const raw = localStorage.getItem(key(user));
    if (!raw || raw.length > maxBytes) return;
    const saved: Snapshot = JSON.parse(raw);
    if (saved.version !== 1 || saved.user !== user || !Number.isFinite(saved.at) || saved.at > Date.now() || Date.now() - saved.at > ttl || !validKey(saved.metricsKey) || saved.metricsKey[0] !== "metrics" || !Array.isArray(saved.entries) || saved.entries.length > 512) return;
    if (!saved.entries.every(e => validKey(e.key) && e.data && Number.isFinite(e.at) && e.at > 0 && e.at <= saved.at)) return;
    if (!saved.entries.some(e => JSON.stringify(e.key) === JSON.stringify(saved.metricsKey))) return;
    const restoredKeys = new Map<string, number>();
    for (const entry of saved.entries) {
      if ((client.getQueryState(entry.key)?.dataUpdatedAt || 0) >= entry.at) continue;
      client.setQueryData(entry.key, entry.data, { updatedAt: entry.at });
      // Force background validation even when reopening within staleTime.
      void client.invalidateQueries({ queryKey: entry.key, exact: true, refetchType: "none" });
      restoredKeys.set(JSON.stringify(entry.key), entry.at);
    }
    restored.set(client, restoredKeys);
  } catch { /* A corrupt or unavailable cache must not block a live read. */ }
}
export function restoredDashboardAt(client: QueryClient, metricsKey: QueryKey) {
  const at = restored.get(client)?.get(JSON.stringify(metricsKey));
  return at && (client.getQueryState(metricsKey)?.dataUpdatedAt || 0) <= at ? at : undefined;
}
export function saveDashboardSnapshot(client: QueryClient, user: string, metricsKey: QueryKey) {
  if (!user || !validKey(metricsKey) || metricsKey[0] !== "metrics") return;
  const metrics = client.getQueryState(metricsKey);
  if (!metrics?.data || metrics.status !== "success" || metrics.fetchStatus !== "idle" || restoredDashboardAt(client, metricsKey)) return;
  const selected = client.getQueryCache().getAll().filter(q => q.isActive() && validKey(q.queryKey));
  // Save only a fully settled view. Never replace a previous complete snapshot
  // with skeletons, an intermediate filtered table, or failed refresh results.
  if (!selected.some(q => q.queryKey[0] === "catalog") || !selected.some(q => q.queryKey[0] === "keys") || selected.some(q => q.state.status !== "success" || !q.state.data || q.state.fetchStatus !== "idle")) return;
  const entries: Entry[] = selected.map(q => ({ key: q.queryKey, data: projection(q.state.data), at: q.state.dataUpdatedAt }));
  const snapshot: Snapshot = { version: 1, user, at: Date.now(), metricsKey, entries };
  try {
    const raw = JSON.stringify(snapshot);
    if (raw.length <= maxBytes) localStorage.setItem(key(user), raw);
  } catch { /* Optional storage never changes rendering or live requests. */ }
}
export function clearDashboardSnapshots(client?: QueryClient) {
  if (client) restored.delete(client);
  try {
    for (const name of Object.keys(localStorage)) if (name.startsWith(prefix)) localStorage.removeItem(name);
  } catch {}
}

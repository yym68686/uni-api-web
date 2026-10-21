import type { QueryClient, QueryKey } from "@tanstack/react-query";
import { loadSummaryCache, saveSummaryCache } from "./summaryCache";

// Hydrate authenticated read-only data while a live query is pending. Persisted
// results are visibly stale and never substitute for save-time revision checks.
export async function cachedControlRead<T extends object>(
  client: QueryClient,
  queryKey: QueryKey,
  signal: AbortSignal,
  read: () => Promise<T>,
): Promise<T & { cached?: boolean; cachedAt?: number }> {
  const auth = client.getQueryData<{
    authenticated: boolean;
    username: string;
  }>(["account"]);
  const user = auth?.authenticated ? auth.username : "";
  const cacheKey = "control-read:" + JSON.stringify(queryKey);
  if (!client.getQueryData(queryKey) && user) {
    const saved = await loadSummaryCache<T>(user, cacheKey);
    signal.throwIfAborted();
    if (saved?.rows.length === 1 && !client.getQueryData(queryKey)) {
      client.setQueryData(queryKey, {
        ...saved.rows[0].value,
        cached: true,
        cachedAt: saved.cachedAt,
      });
    }
  }
  const data = await read();
  signal.throwIfAborted();
  void saveSummaryCache(user, cacheKey, {
    version: "read",
    rows: [{ id: "data", value: data }],
  });
  return { ...data, cached: false };
}

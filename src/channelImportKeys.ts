import { useQueries, useQueryClient } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { controlRequest } from "./api";
import type { KeyInfo } from "./types";

export interface ChannelImportKeys {
  keys: KeyInfo[];
}

// Only the authoritative key directory can seed a selector. Route inventories
// omit keys with no routes, so they must never masquerade as a complete list.
function cachedKeys(client: QueryClient, source: string) {
  const directories = client
    .getQueryCache()
    .findAll({ queryKey: ["keys"] })
    .filter((query) => query.state.data && !query.state.isInvalidated)
    .sort((a, b) => b.state.dataUpdatedAt - a.state.dataUpdatedAt);
  for (const query of directories) {
    const data = query.state.data as {
      data?: KeyInfo[];
      unavailable_sources?: string[];
    };
    if (data.unavailable_sources?.length || !Array.isArray(data.data)) continue;
    const keys = data.data.filter(
      (key) => key.source_id === source || key.key_id.startsWith(`${source}::`),
    );
    if (!keys.length) continue;
    return {
      data: {
        keys: keys.map((key) => ({
          ...key,
          key_id: key.key_id.replace(`${source}::`, ""),
        })),
      },
      updatedAt: query.state.dataUpdatedAt,
    };
  }
}

// Shared by the page and both channel editors; React Query deduplicates reads.
// Data stays in the login's QueryClient and is cleared on logout, never on disk.
export function useChannelImportKeys(sources: string[]) {
  const client = useQueryClient();
  return useQueries({
    queries: sources.map((source) => {
      const cached = cachedKeys(client, source);
      return {
        queryKey: ["channel-import-keys", source],
        queryFn: ({ signal }: { signal: AbortSignal }) =>
          controlRequest<ChannelImportKeys>(
            "/v1/sub2api/channel-options?" +
              new URLSearchParams({ source_id: source, keys_only: "true" }),
            { signal: AbortSignal.any([signal, AbortSignal.timeout(10_000)]) },
          ).then((data) => {
            if (!Array.isArray(data.keys))
              throw new Error("来源返回了无效的 API key 列表。");
            return data;
          }),
        initialData: cached?.data,
        initialDataUpdatedAt: cached?.updatedAt,
        staleTime: 30_000,
        refetchInterval: 60_000,
        retry: false,
        refetchOnWindowFocus: false,
      };
    }),
  });
}

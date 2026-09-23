import type { QueryClient } from "@tanstack/react-query";
import type { Routes } from "./channelRouteData";
import type { SubImports } from "./sub2apiImports";
import { buildChannelBatch } from "./channelBatch";
import type { BatchDraft, BatchInventory, BatchSnapshot } from "./channelBatch";

// The management page already loads these session-scoped queries. They are
// cleared on logout; never persist permission-scoped previews in localStorage.
export function cachedChannelBatch(client: QueryClient, draft: BatchDraft) {
  const inventory = client.getQueryData<BatchInventory>(["channel-management"]);
  const imports = client.getQueryData<SubImports>(["sub2api-imports"]);
  if (!inventory || !imports) return null;
  const routes: Record<string, Routes> = {};
  for (const [key, data] of client.getQueriesData<Routes>({
    queryKey: ["channel-routes"],
  })) {
    if (typeof key[1] === "string" && data) routes[key[1]] = data;
  }
  try {
    // Cached data authorizes only a preview. Confirm re-reads the entire scope.
    return buildChannelBatch(draft, { inventory, imports, routes }, false);
  } catch {
    return null;
  }
}

export function rememberBatchSnapshot(
  client: QueryClient,
  snapshot: BatchSnapshot,
) {
  client.setQueryData(["channel-management"], snapshot.inventory);
  client.setQueryData(["sub2api-imports"], snapshot.imports);
  for (const [source, routes] of Object.entries(snapshot.routes)) {
    client.setQueryData(["channel-routes", source], routes);
  }
}

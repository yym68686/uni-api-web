import { useQueries, useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import { channelMembers } from "./channelManagement";
import type { ManagedChannel } from "./channelManagement";
export interface ChannelRoute {
  provider: string;
  origin_provider?: string;
  model: string;
  upstream_model?: string;
  api_key_id: string;
  key_prefix: string;
  key_position: number;
  position: number;
}
export interface Routes {
  data: ChannelRoute[];
  unavailable_keys: string[];
}
export const routeOptions = (source: string) => ({
  queryKey: ["channel-routes", source],
  queryFn: ({ signal }: { signal: AbortSignal }) =>
    controlRequest<Routes>(
      `/v1/sources/${encodeURIComponent(source)}/channel-routes`,
      { signal },
    ),
  staleTime: 15000,
  retry: false,
  refetchInterval: 30000,
});
export const useChannelRoutes = (source: string) =>
  useQuery(routeOptions(source));
export const useAllChannelRoutes = (sources: string[]) =>
  useQueries({ queries: sources.map(routeOptions) });
export function providerRoutes(routes: ChannelRoute[], providers: string[]) {
  return routes.filter(
    (r) =>
      providers.includes(r.provider) ||
      (!!r.origin_provider && providers.includes(r.origin_provider)),
  );
}
export function routeKeyCount(routes: ChannelRoute[]) {
  return new Set(routes.map((r) => r.api_key_id)).size;
}
export function managedRouteCount(
  item: ManagedChannel,
  sources: string[],
  queries: { data?: Routes; isError?: boolean }[],
) {
  let count = 0,
    loaded = false,
    partial = false;
  for (const member of channelMembers(item)) {
    const query = queries[sources.indexOf(member.source_id)];
    if (!query?.data) {
      partial = true;
      continue;
    }
    loaded = true;
    count += routeKeyCount(providerRoutes(query.data.data, [member.provider]));
    partial ||= !!query.isError || !!query.data.unavailable_keys?.length;
  }
  return loaded ? { count, partial } : null;
}

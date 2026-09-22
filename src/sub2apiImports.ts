import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
export interface InstalledChannel {
  model_mappings?: Record<string,string>;
  kind?: "configured";
  binding_status?:
    "matched" | "partial" | "unmatched" | "ambiguous" | "no_account";
  binding_checked_at?: number;
  bound_keys?: BoundKey[];
  base?: string;
  account_id: string;
  group_id: number;
  source_id: string;
  source_name: string;
  api_key_id: string;
  key_position: number;
  key_prefix: string;
  provider: string;
  name: string;
  models: string[];
  positions: Record<string, number>;
  revision: string;
  manageable: boolean;
}
export interface BoundKey {
  account_id: string;
  account_name: string;
  base: string;
  group_id: number;
  remote_key_id: number;
}
export function boundGroups(item: InstalledChannel | undefined) {
  if (!item) return [];
  const groups =
    item.kind === "configured"
      ? item.binding_status === "matched"
        ? item.bound_keys || []
        : []
      : [item];
  return [
    ...new Map(
      groups
        .filter((g) => g.account_id && g.group_id > 0)
        .map((g) => [`${g.account_id}:${g.group_id}`, g]),
    ).values(),
  ];
}
export interface SubImports {
  data: InstalledChannel[];
  labels: Record<string, Record<string, string>>;
  unavailable_sources: string[];
}
export function useSubImports(enabled = true) {
  return useQuery({
    queryKey: ["sub2api-imports"],
    queryFn: ({ signal }) =>
      controlRequest<SubImports>("/v1/sub2api/channels", { signal }),
    enabled,
    retry: false,
    staleTime: 5000,
    refetchInterval: enabled ? 15000 : false,
  });
}
export type SubImportsQuery = ReturnType<typeof useSubImports>;

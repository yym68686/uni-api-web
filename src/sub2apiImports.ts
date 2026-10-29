import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
export interface InstalledChannel {
  protocol?: string;
  engine?: string;
  engine_mode?: string;
  model_mappings?: Record<string, string>;
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
  scope_source_id?: string;
  data: InstalledChannel[];
  labels: Record<string, Record<string, string>>;
  multipliers?: Record<string, Record<string, number | null>>;
  model_billing?: Record<
    string,
    Record<
      string,
      Record<
        string,
        {
          name: string;
          rate: number | null;
          observed_at?: number;
        }
      >
    >
  >;
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
    refetchInterval: enabled ? 60000 : false,
  });
}

// Import dialogs consume bindings and revisions, not the billing-rate ledger.
// Keep this cache distinct so a narrow response cannot replace a full listing.
export function useImportBindings(enabled = true, group?: { accountId: string; groupId: number }) {
  return useQuery({
    // Keep the shared invalidation prefix, but never let one group's narrow
    // response replace another group's or the complete bindings directory.
    queryKey: group
      ? ["sub2api-imports", "bindings", "group", group.accountId, group.groupId]
      : ["sub2api-imports", "bindings"],
    queryFn: ({ signal }) =>
      controlRequest<SubImports>("/v1/sub2api/channels?" + new URLSearchParams({
        view: "bindings",
        ...(group ? { account_id: group.accountId, group_id: String(group.groupId) } : {}),
      }), { signal }),
    enabled,
    retry: false,
    staleTime: 5000,
    refetchInterval: enabled ? 60000 : false,
  });
}
export type SubImportsQuery = ReturnType<typeof useSubImports>;

// Model editors need the selected source's binding/revision, not the global
// billing-rate ledger or unrelated gateways. Saving still validates live state.
export function useChannelBindings(sourceId: string) {
  return useQuery({
    queryKey: ["sub2api-imports", "bindings", sourceId],
    queryFn: ({ signal }) =>
      controlRequest<SubImports>(
        "/v1/sub2api/channels?" +
          new URLSearchParams({ source_id: sourceId, view: "bindings" }),
        { signal },
      ),
    enabled: !!sourceId,
    staleTime: 5000,
    retry: false,
  });
}

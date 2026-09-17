import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
export interface InstalledChannel {
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

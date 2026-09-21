import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import type { ConsoleSource } from "./SourceSettings";

export function useConsoleSources(session: string, enabled = true) {
  return useQuery({
    queryKey: ["sources", session],
    queryFn: ({ signal }) =>
      controlRequest<{ data: ConsoleSource[] }>("/v1/sources", {
        signal: AbortSignal.any([signal, AbortSignal.timeout(10_000)]),
      }),
    enabled,
    staleTime: 30_000,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

export type ConsoleSourcesQuery = ReturnType<typeof useConsoleSources>;

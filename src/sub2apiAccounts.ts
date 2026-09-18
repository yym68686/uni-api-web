import { useQuery } from "@tanstack/react-query";
import { controlRequest } from "./api";
import type { SubAccount } from "./Sub2apiChecks";

export function useSubAccounts(enabled = true) {
  return useQuery({
    queryKey: ["sub2api"],
    queryFn: ({ signal }) =>
      controlRequest<{ data: SubAccount[] }>("/v1/sub2api/accounts", {
        signal,
      }),
    enabled,
    retry: false,
    refetchInterval: enabled
      ? (query) =>
          query.state.data?.data.some((account) =>
            ["queued", "running"].includes(account.state),
          )
            ? 1500
            : 15000
      : false,
  });
}

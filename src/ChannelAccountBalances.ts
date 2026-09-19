import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import { controlRequest, makeLimiter } from "./api";
import type { InstalledChannel } from "./sub2apiImports";
import type { Balance } from "./types";
import { providerId } from "./format";

interface AccountBalance {
  amount: number | null;
  status: string;
  checked_at: number;
}
export function useChannelAccountBalances(
  providers: string[],
  channels: InstalledChannel[],
  session: string,
  enabled: boolean,
  auto: boolean,
) {
  const limit = useMemo(() => makeLimiter(3), []);
  const related = channels.filter(
    (item) =>
      providers.includes(providerId(item)) &&
      item.kind === "configured" &&
      item.binding_status === "matched",
  );
  const accounts = [
    ...new Map(
      related
        .flatMap((item) => item.bound_keys || [])
        .map((key) => [key.account_id, key]),
    ).values(),
  ];
  const queries = useQueries({
    queries: accounts.map((account) => ({
      queryKey: ["bound-account-balance", session, account.account_id],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        limit(
          () =>
            controlRequest<AccountBalance>(
              `/v1/sub2api/accounts/${encodeURIComponent(account.account_id)}/balance`,
              { signal },
            ),
          signal,
        ),
      enabled,
      retry: false,
      staleTime: 60000,
      refetchInterval: auto ? 60000 : false,
    })),
  });
  const byAccount = new Map(
    accounts.map((account, index) => [account.account_id, queries[index]]),
  );
  return new Map(
    related.map((channel) => {
      const unique = [
        ...new Map(
          (channel.bound_keys || []).map((key) => [key.account_id, key]),
        ).values(),
      ];
      const pending = unique.some(
        (key) => byAccount.get(key.account_id)?.isPending,
      );
      const errors = unique.some(
        (key) => byAccount.get(key.account_id)?.isError,
      );
      const balance: Balance = {
        provider: channel.provider,
        status: "complete",
        key_count: unique.length,
        keys: unique.map((key, index) => {
          const query = byAccount.get(key.account_id);
          return {
            position: index + 1,
            label: key.account_name,
            kind: "account_wallet",
            currency: "USD",
            status: query?.data?.status || "unavailable",
            amount: query?.data?.amount,
            checked_at: query?.data?.checked_at,
          };
        }),
      };
      return [
        providerId(channel),
        {
          data: pending ? undefined : balance,
          isPending: pending,
          isError: errors,
          refetch: () =>
            Promise.all(
              unique.map((key) =>
                byAccount
                  .get(key.account_id)!
                  .refetch({ cancelRefetch: false }),
              ),
            ),
        },
      ];
    }),
  );
}

import { useQueries } from "@tanstack/react-query";
import { accountBalanceQuery } from "./accountBalance";
import type { InstalledChannel } from "./sub2apiImports";
import type { SiteWallet } from "./types";
import { providerId } from "./format";

interface WalletAccount {
  account_id: string;
  account_name?: string;
  base?: string;
}

export interface SiteBalanceResult {
  data: SiteWallet[];
  isPending: boolean;
  refetch: () => Promise<unknown>;
}

function walletAccounts(channel: InstalledChannel): WalletAccount[] {
  if (channel.kind === "configured") {
    // Partial and ambiguous matches cannot identify every credential's owner.
    return channel.binding_status === "matched" ? channel.bound_keys || [] : [];
  }
  // Site-created channels already carry the account proven by their import.
  return channel.account_id && channel.group_id > 0
    ? [{ account_id: channel.account_id, base: channel.base }]
    : [];
}
export function useChannelAccountBalances(
  providers: string[],
  channels: InstalledChannel[],
  session: string,
  enabled: boolean,
  auto: boolean,
) {
  const selected = new Set(providers);
  const related = channels.filter((item) => selected.has(providerId(item)) && walletAccounts(item).length > 0);
  const accountNames = new Map(channels.flatMap(walletAccounts)
    .filter((account) => account.account_name)
    .map((account) => [account.account_id, account.account_name]));
  const accounts = [
    ...new Map(
      related
        .flatMap(walletAccounts)
        .map((key) => [key.account_id, key]),
    ).values(),
  ];
  const queries = useQueries({
    queries: accounts.map((account) => ({
      ...accountBalanceQuery(session, account.account_id, account.base),
      enabled,
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
          walletAccounts(channel).map((key) => [key.account_id, key]),
        ).values(),
      ];
      const pending = unique.some(
        (key) => byAccount.get(key.account_id)?.isPending,
      );
      const wallets: SiteWallet[] = unique.map(({ account_id }) => {
        const query = byAccount.get(account_id);
        return {
          label: accountNames.get(account_id),
          account_id,
          status: query?.isError ? "error" : query?.isPending ? "pending" : query?.data?.status || "missing",
          amount: query?.data?.amount,
          checked_at: query?.data?.checked_at,
        };
      });
      return [
        providerId(channel),
        {
          data: wallets,
          isPending: pending,
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

export function walletGroups(rows: Iterable<[string, SiteBalanceResult]>) {
  const groups = new Map<string,{label:string; wallet:SiteWallet; providers:Set<string>}>();
  for (const [provider, result] of rows) {
    for (const wallet of result.data) {
      const found = groups.get(wallet.account_id);
      if (found) found.providers.add(provider);
      else groups.set(wallet.account_id,{label:wallet.label || "站点账号",wallet,providers:new Set([provider])});
    }
  }
  return groups;
}

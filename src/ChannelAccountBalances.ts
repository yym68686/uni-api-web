import { useQueries } from "@tanstack/react-query";
import { accountBalanceQuery } from "./accountBalance";
import type { InstalledChannel } from "./sub2apiImports";
import type { Balance } from "./types";
import { providerId } from "./format";

interface WalletAccount {
  account_id: string;
  account_name?: string;
  base?: string;
}

interface ChannelBalanceResult {
  data?: Balance;
  isPending: boolean;
  isError: boolean;
  refetch: () => Promise<unknown>;
}

interface AccountBalanceResult extends ChannelBalanceResult {
  needsChannelBalance: boolean;
}

// Imported channels may expose a key quota or subscription instead of a wallet.
// Only a proven wallet can be replaced by the shared account snapshot.
export function withAccountWallet(
  raw: ChannelBalanceResult,
  account?: AccountBalanceResult,
): ChannelBalanceResult {
  if (!account) return raw;
  if (!account.needsChannelBalance) return account;
  const isWallet = (kind?: string) => kind === "wallet" || kind === "account_wallet";
  if (!raw.data?.keys?.some((key) => isWallet(key.kind))) return raw;
  const refetch = () => Promise.all([account.refetch(), raw.refetch()]);
  const wallet = account.data?.keys?.[0];
  if (account.isPending || account.isError || !wallet) {
    // An old gateway wallet must not reappear while the account refresh fails
    // or is still loading. All views should reflect the same account state.
    return { data: undefined, isPending: account.isPending, isError: account.isError, refetch };
  }
  return {
    data: {
      ...raw.data,
      keys: raw.data.keys.map((key) => isWallet(key.kind)
        ? { ...key, ...wallet, position: key.position }
        : key),
    },
    isPending: false,
    isError: false,
    refetch,
  };
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
            label: accountNames.get(key.account_id),
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
          needsChannelBalance: channel.kind !== "configured",
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

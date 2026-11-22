import { useQueries } from "@tanstack/react-query";
import { accountBalanceQuery } from "./accountBalance";
import type { InstalledChannel } from "./sub2apiImports";
import type { SiteWallet } from "./types";
import { providerId } from "./format";
import { controlRequest } from "./api";
import type { AccountBalance } from "./accountBalance";

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
  siteFor?: (row: { source_id: string; provider: string }) => string | undefined,
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
  const openRouter = providers.flatMap(id => {
    const [source_id, provider] = JSON.parse(id) as [string, string];
    const base = siteFor?.({ source_id, provider });
    if (!source_id || !base) return [];
    try {
      const url = new URL(base);
      return url.origin === "https://openrouter.ai" && !url.username && !url.password ? [{ id, source_id, provider }] : [];
    } catch { return []; }
  });
  const credits = useQueries({ queries: openRouter.map(({ source_id, provider }) => ({
    queryKey: ["openrouter-balance", session, source_id, provider],
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const query = new URLSearchParams({ provider });
      const balance = await controlRequest<AccountBalance>(`/v1/sources/${encodeURIComponent(source_id)}/openrouter-balance?${query}`, { signal });
      // Keep the prior known balance on transient errors only. A refused key
      // or unverified channel must not keep showing a different wallet.
      if (balance.error_reason === "openrouter_fetch_failed" || balance.error_reason === "rate_limited") {
        throw new OpenRouterBalanceError(balance.error_reason);
      }
      return balance;
    },
    enabled,
    staleTime: 60_000,
    refetchInterval: auto ? 60_000 : false,
    retry: false,
  })) });
  const result = new Map<string, SiteBalanceResult>(
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
          ...(query?.data?.status === "error" && !query.isError && query.data.error_reason ? { error_reason: query.data.error_reason } : {}),
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
  openRouter.forEach(({ id }, index) => {
    const query = credits[index];
    result.set(id, {
      data: [{
        account_id: `openrouter:${id}`,
        label: "OpenRouter",
        provider_kind: "openrouter",
        status: query.isError ? "error" : query.isPending ? "pending" : query.data?.status || "missing",
        amount: query.data?.amount,
        checked_at: query.data?.checked_at,
        error_reason: query.error instanceof OpenRouterBalanceError ? query.error.reason : query.data?.error_reason,
      }],
      isPending: query.isPending,
      refetch: () => query.refetch({ cancelRefetch: false }),
    });
  });
  return result;
}

class OpenRouterBalanceError extends Error {
  readonly reason: string;
  constructor(reason: string) {
    super(reason);
    this.reason = reason;
  }
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

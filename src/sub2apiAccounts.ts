import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import { readSummary } from "./summaryFeed";
import type { SummaryState } from "./summaryFeed";
import { loadSummaryCache, saveSummaryCache } from "./summaryCache";
import { ApiError } from "./api";

type AccountRow = {
  account?: SubAccount;
  account_id?: string;
  target?: SubTarget;
  position: number;
};
type AccountRates = NonNullable<SubTarget["observed_rates"]>;
export type AccountData = {
  data: SubAccount[];
  summary?: SummaryState<AccountRow>;
  cached?: boolean;
  cachedAt?: number;
  accountSummaries?: Record<string, SummaryState<AccountRow>>;
  rateSummary?: SummaryState<AccountRates>;
};

// Publish only a complete refresh. A failed part leaves the prior complete
// list in React Query; neither cancellation nor retry can replay a mutation.
export async function readAccountData(
  previous: AccountData | undefined,
  signal: AbortSignal,
): Promise<AccountData> {
  const directory = await readSummary<AccountRow, AccountData>(
    "/v1/sub2api/accounts",
    undefined,
    signal,
    "accounts",
  );
  if (!directory.accountScope) {
    const full = await readSummary<AccountRow, AccountData>(
      "/v1/sub2api/accounts",
      previous?.summary,
      signal,
    );
    return full.summary
      ? { data: summaryAccounts(full.summary), summary: full.summary }
      : full.legacy!;
  }
  if (!directory.summary) throw Error("账号目录数据不完整，请重新读取");
  const headers = summaryAccounts(directory.summary!);
  const accountSummaries: Record<string, SummaryState<AccountRow>> = {};
  const parts: SubAccount[][] = new Array(headers.length);
  const controller = new AbortController();
  const combined = AbortSignal.any([signal, controller.signal]);
  let next = 0;
  const worker = async () => {
    while (next < headers.length) {
      combined.throwIfAborted();
      const index = next++;
      const id = headers[index].id;
      const result = await readSummary<AccountRow, AccountData>(
        "/v1/sub2api/accounts?include_rates=0&account_id=" +
          encodeURIComponent(id),
        previous?.accountSummaries?.[id],
        combined,
      );
      const accounts = result.summary
        ? summaryAccounts(result.summary)
        : result.legacy?.data;
      if (!accounts || accounts.length !== 1 || accounts[0].id !== id)
        throw Error("账号数据不完整，请重新读取");
      parts[index] = accounts;
      if (result.summary) accountSummaries[id] = result.summary;
    }
  };
  try {
    await Promise.all(
      Array.from({ length: Math.min(2, headers.length) }, worker),
    );
    const rateResult = headers.length
      ? await readSummary<AccountRates, never>(
          "/v1/sub2api/accounts",
          previous?.rateSummary,
          combined,
          "rates",
        )
      : undefined;
    if (headers.length && !rateResult?.summary)
      throw Error("账单倍率数据不完整，请重新读取");
    signal.throwIfAborted();
    const rates = new Map(
      rateResult?.summary?.rows.map((row) => [row.id, row.value]),
    );
    const data = parts.flat().map((account) => ({
      ...account,
      targets: account.targets.map((target) => ({
        ...target,
        observed_rates: rates.get(`${account.id}:${target.group_id}`),
      })),
    }));
    return {
      data,
      accountSummaries,
      rateSummary: rateResult?.summary,
      cached: false,
    };
  } finally {
    controller.abort();
  }
}
export function summaryAccounts(
  summary: SummaryState<AccountRow>,
): SubAccount[] {
  const rows = [...summary.rows].sort(
    (a, b) => a.value.position - b.value.position,
  );
  const accounts = new Map<string, SubAccount>();
  for (const { value } of rows)
    if (value.account)
      accounts.set(value.account.id, { ...value.account, targets: [] });
  for (const { value } of rows)
    if (value.target && value.account_id) {
      const account = accounts.get(value.account_id);
      if (!account) throw Error("账号数据不完整，请重新读取");
      account.targets.push({ ...value.target, details_omitted: true });
    }
  return [...accounts.values()];
}

export function useSubGroupAccounts(
  groups: { account_id: string; group_id: number }[],
  enabled = true,
) {
  const client = useQueryClient();
  const scope = [
    ...new Set(
      groups
        .filter((g) => g.account_id && g.group_id > 0)
        .map((g) => JSON.stringify([g.account_id, g.group_id])),
    ),
  ].sort();
  const queryKey = ["sub2api", "groups", ...scope];
  return useQuery({
    queryKey,
    queryFn: async ({ signal }): Promise<AccountData> => {
      if (!scope.length) return { data: [] };
      const previous = client.getQueryData<AccountData>(queryKey);
      const accountSummaries: Record<string, SummaryState<AccountRow>> = {};
      const parts: SubAccount[][] = new Array(scope.length);
      const controller = new AbortController();
      const combined = AbortSignal.any([signal, controller.signal]);
      let next = 0;
      const worker = async () => {
        while (next < scope.length) {
          const i = next++;
          const [id, group] = JSON.parse(scope[i]) as [string, number];
          let result;
          try {
            result = await readSummary<AccountRow, AccountData>(
              `/v1/sub2api/accounts/${encodeURIComponent(id)}/groups/${group}/details?include_rates=0`,
              previous?.accountSummaries?.[scope[i]], combined,
            );
          } catch (error) {
            if (!(error instanceof ApiError) || error.status !== 404) throw error;
            parts[i] = [];
            continue;
          }
          const data = result.summary
            ? summaryAccounts(result.summary)
            : result.legacy?.data;
          if (
            !data ||
            data.length !== 1 ||
            data[0].id !== id ||
            data[0].targets.length !== 1 ||
            data[0].targets[0].group_id !== group
          )
            throw Error("分组检测结果不完整，请重新读取");
          parts[i] = data;
          if (result.summary) accountSummaries[scope[i]] = result.summary;
        }
      };
      try {
        await Promise.all(
          Array.from({ length: Math.min(2, scope.length) }, worker),
        );
        signal.throwIfAborted();
        const accounts = new Map<string, SubAccount>();
        for (const a of parts.flat()) {
          const prior = accounts.get(a.id);
          accounts.set(a.id, {
            ...a,
            targets: [...(prior?.targets || []), ...a.targets],
          });
        }
        return { data: [...accounts.values()], accountSummaries };
      } finally {
        controller.abort();
      }
    },
    enabled,
    staleTime: 15_000,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

export function useSubAccounts(enabled = true, headersOnly = false) {
  const client = useQueryClient();
  const queryKey = headersOnly ? ["sub2api-headers"] : ["sub2api"];
  return useQuery({
    queryKey,
    queryFn: async ({ signal }): Promise<AccountData> => {
      let previous = client.getQueryData<AccountData>(queryKey);
      const auth = client.getQueryData<{
        authenticated: boolean;
        username: string;
      }>(["account"]);
      const user = auth?.authenticated ? auth.username : "";
      const cacheKey = headersOnly ? "account-headers" : "account-summary";
      if (!previous && user) {
        const summary = await loadSummaryCache<AccountRow>(user, cacheKey);
        signal.throwIfAborted();
        if (summary) {
          previous = {
            data: summaryAccounts(summary),
            summary,
            cached: true,
            cachedAt: summary.cachedAt,
          };
          client.setQueryData(queryKey, previous);
        }
      }
      if (!headersOnly) {
        const result = await readAccountData(previous, signal);
        if (result.summary)
          void saveSummaryCache(user, cacheKey, result.summary);
        else if (result.accountSummaries) {
          // Store only a complete assembled snapshot. Per-account server
          // cursors remain session-local; a reload revalidates every part.
          const rows = Object.values(result.accountSummaries).flatMap(
            (part) => part.rows,
          );
          const rates = new Map(
            result.rateSummary?.rows.map((row) => [row.id, row.value]),
          );
          const positions = new Map(
            result.data.map((account, i) => [account.id, i]),
          );
          void saveSummaryCache(user, cacheKey, {
            version: "assembled",
            rows: rows.map((row) =>
              row.value.account
                ? {
                    ...row,
                    value: {
                      ...row.value,
                      position: positions.get(row.value.account.id)!,
                    },
                  }
                : row.value.target
                  ? {
                      ...row,
                      value: {
                        ...row.value,
                        target: {
                          ...row.value.target,
                          observed_rates: rates.get(row.id),
                        },
                      },
                    }
                  : row,
            ),
          });
        }
        return result;
      }
      const { summary, legacy } = await readSummary<AccountRow, AccountData>(
        "/v1/sub2api/accounts",
        previous?.summary,
        signal,
        headersOnly ? "accounts" : "summary",
      );
      if (!summary) return legacy!;
      signal.throwIfAborted();
      void saveSummaryCache(user, cacheKey, summary);
      if (summary === previous?.summary) return { ...previous, cached: false };
      return { data: summaryAccounts(summary), summary, cached: false };
    },
    enabled,
    retry: false,
    staleTime: 30_000,
    refetchInterval: enabled
      ? (query) =>
          query.state.data?.data.some((account) =>
            ["queued", "running"].includes(account.state),
          )
            ? 5000
            : 60000
      : false,
  });
}

import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { SubAccount, SubTarget } from "./Sub2apiChecks";
import { readSummary } from "./summaryFeed";
import type { SummaryState } from "./summaryFeed";
import { loadSummaryCache, saveSummaryCache } from "./summaryCache";

type AccountRow = {
  account?: SubAccount;
  account_id?: string;
  target?: SubTarget;
  position: number;
};
export type AccountData = {
  data: SubAccount[];
  summary?: SummaryState<AccountRow>;
  cached?: boolean;
};
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
          previous = { data: summaryAccounts(summary), summary, cached: true };
          client.setQueryData(queryKey, previous);
        }
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
